package gateway

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sort"
	"sync"
	"time"

	"runpilot/internal/config"
	"runpilot/internal/model"
)

// Server — runtime-запись сервера (раздел 4/6 ТЗ). Конфиг + состояние
// в памяти координатора; источник истины по слотам — БД (lease).
type Server struct {
	Cfg config.Server

	mu     sync.Mutex
	state  model.ServerState
	key    string // из key_env (разрешается один раз при старте)
	health *http.Client
}

// State — текущее состояние сервера.
func (s *Server) State() model.ServerState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// SetState — смена состояния + уведомление наблюдателей.
func (s *Server) SetState(st model.ServerState) {
	s.mu.Lock()
	s.state = st
	s.mu.Unlock()
}

// Key — ключ апстрима (Bearer).
func (s *Server) Key() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.key
}

// HealthOK — внеочередная проверка /health (раздел 6 ТЗ): один запрос,
// таймаут monitor.health_timeout_sec.
func (s *Server) HealthOK(ctx context.Context) bool {
	if s.Cfg.HealthURL == "" {
		return true
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.Cfg.HealthURL, nil)
	if err != nil {
		return false
	}
	resp, err := s.health.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// Servers — реестр серверов координатора.
type Servers struct {
	mu            sync.Mutex // мутации list/byName (W6: CRUD серверов)
	list          []*Server
	byName        map[string]*Server
	healthTimeout time.Duration

	// Наблюдатели смены состояния (планировщик Э4: SERVER_DOWN при
	// миграции обязан дойти до планировщика).
	obsMu sync.Mutex
	obs   []func(name string, st model.ServerState)
}

// OnChange — наблюдатель смены состояния сервера.
func (s *Servers) OnChange(fn func(name string, st model.ServerState)) {
	s.obsMu.Lock()
	defer s.obsMu.Unlock()
	s.obs = append(s.obs, fn)
}

// NewServers — реестр из конфигурации. Ключи апстримов читаются из
// переменных окружения (key_env); отсутствующая переменная — пустой ключ
// и предупреждение (vLLM без авторизации).
func NewServers(cfg []config.Server, healthTimeout time.Duration) (*Servers, error) {
	s := &Servers{byName: map[string]*Server{}, healthTimeout: healthTimeout}
	for i := range cfg {
		c := cfg[i]
		if _, dup := s.byName[c.Name]; dup {
			return nil, fmt.Errorf("servers: дублирующееся имя %s", c.Name)
		}
		s.byName[c.Name] = s.newServer(c)
		s.list = append(s.list, s.byName[c.Name])
	}
	s.sortByPriority()
	return s, nil
}

// newServer — runtime-запись из конфига (ключ апстрима из env).
func (s *Servers) newServer(c config.Server) *Server {
	state := model.ServerUp
	keyEnv := c.Upstreams.OpenAI.KeyEnv
	k := os.Getenv(keyEnv)
	// v2 (S11): ключ обязателен (key_env задан), но переменная пуста в
	// окружении службы — сервер не годится (KEY_MISSING).
	if keyEnv != "" && k == "" {
		state = model.ServerKeyMissing
	}
	srv := &Server{
		Cfg:    c,
		state:  state,
		key:    k,
		health: &http.Client{Timeout: s.healthTimeout},
	}
	return srv
}

func (s *Servers) sortByPriority() {
	// Приоритет: сначала большие (раздел 6 ТЗ: UP-сервер с наибольшим).
	sort.SliceStable(s.list, func(i, j int) bool {
		return s.list[i].Cfg.Priority > s.list[j].Cfg.Priority
	})
}

// ByName — сервер по имени (nil, если его нет).
func (s *Servers) ByName(name string) *Server {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.byName[name]
}

// AddOrUpdate — создать или обновить сервер в работе (W6, мастер/раздел 3.4):
// новый — в реестр, существующий — обновить Cfg/ключ.
func (s *Servers) AddOrUpdate(c config.Server) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if srv, ok := s.byName[c.Name]; ok {
		srv.Cfg = c
		if k := os.Getenv(c.Upstreams.OpenAI.KeyEnv); k != "" {
			srv.key = k
		}
		s.sortByPriorityLocked()
		return nil
	}
	s.byName[c.Name] = s.newServer(c)
	s.list = append(s.list, s.byName[c.Name])
	s.sortByPriorityLocked()
	return nil
}

// Remove — удалить сервер из реестра (W6, 5.2) + уведомить DOWN.
func (s *Servers) Remove(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	srv, ok := s.byName[name]
	if !ok {
		return
	}
	delete(s.byName, name)
	for i, sv := range s.list {
		if sv.Cfg.Name == name {
			s.list = append(s.list[:i], s.list[i+1:]...)
			break
		}
	}
	srv.SetState(model.ServerDown)
	s.notifyLocked(name, model.ServerDown)
}

// sortByPriorityLocked — пере-сортировка (вызывается с mu).
func (s *Servers) sortByPriorityLocked() {
	sort.SliceStable(s.list, func(i, j int) bool {
		return s.list[i].Cfg.Priority > s.list[j].Cfg.Priority
	})
}

// notifyLocked — уведомить наблюдателей (вызывается с mu или без).
func (s *Servers) notifyLocked(name string, st model.ServerState) {
	s.obsMu.Lock()
	obs := append([]func(string, model.ServerState){}, s.obs...)
	s.obsMu.Unlock()
	for _, fn := range obs {
		fn(name, st)
	}
}

// All — все серверы в порядке приоритета (снимок).
func (s *Servers) All() []*Server {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*Server(nil), s.list...)
}

// SetState — смена состояния (Э3: внеочередная проверка; Э6: монитор).
// Наблюдатели (OnChange) вызовятся при смене состояния.
func (s *Servers) SetState(name string, st model.ServerState) {
	s.mu.Lock()
	srv, ok := s.byName[name]
	s.mu.Unlock()
	if !ok {
		return
	}
	if srv.State() == st {
		return
	}
	srv.SetState(st)
	s.notifyLocked(name, st)
}

// StateOf — состояние по имени.
func (s *Servers) StateOf(name string) model.ServerState {
	s.mu.Lock()
	srv, ok := s.byName[name]
	s.mu.Unlock()
	if ok {
		return srv.State()
	}
	return model.ServerDown
}

// Accepts — сервер принимает ли класс очереди (servers[].accept).
func (s *Server) Accepts(c model.QueueClass) bool {
	for _, name := range s.Cfg.Accept {
		if cl, ok := model.QueueClassParse(name); ok && cl == c {
			return true
		}
	}
	return false
}

// candidateOK — сервер годится для выдачи: UP (не DOWN, не DRAINING) и
// принимает класс.
func (s *Server) candidateOK(c model.QueueClass) bool {
	if s.State() != model.ServerUp {
		return false
	}
	return s.Accepts(c)
}
