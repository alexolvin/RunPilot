package api

// Новая сессия из веба (раздел 13.2, CONTROL 5/6): координатор сам создаёт
// tmux-сессию на узле (spawn) и ставит @runpilot_sid. Каталог валидируется узлом
// (project_roots + симлинк → DIR_FORBIDDEN/DIR_NOT_FOUND).

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"text/template"
	"time"

	"runpilot/internal/model"
	"runpilot/internal/proto"
	"runpilot/internal/sid"
	"runpilot/internal/store"
)

// SpawnRequest — POST /api/v1/sessions/spawn.
type SpawnRequest struct {
	Name        string   `json:"name"`
	Dir         string   `json:"dir"`
	Node        string   `json:"node"`
	Prio        string   `json:"prio"`
	Pin         string   `json:"pin"`
	Prefer      string   `json:"prefer"`
	AutoEnqueue bool     `json:"auto_enqueue"`
	Args        []string `json:"args"`
}

// handleSpawn — новая сессия: имя → узел → spawn → @runpilot_sid.
func (s *Server) handleSpawn(w http.ResponseWriter, r *http.Request) {
	var req SpawnRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	if req.Dir == "" {
		httpError(w, http.StatusBadRequest, "BAD_REQUEST", "dir обязателен")
		return
	}
	if req.Name == "" {
		req.Name = filepath.Base(req.Dir)
	}
	// Имя занято живой сессией → NAME_IN_USE.
	if live, err := s.store.FindLiveByName(req.Name); err == nil {
		httpError(w, http.StatusConflict, "NAME_IN_USE",
			fmt.Sprintf("имя занято сессией %s (%s)", live.SID, live.State))
		return
	}

	host, err := s.pickNode(req.Node)
	if err != nil {
		httpError(w, http.StatusServiceUnavailable, "NO_NODE", err.Error())
		return
	}

	newSID, err := sid.New()
	if err != nil {
		httpError(w, http.StatusInternalServerError, "SID", err.Error())
		return
	}
	env, err := s.spawnEnv(newSID)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "ENV", err.Error())
		return
	}

	m := proto.New(proto.KindSpawn)
	m.Name = req.Name
	m.Dir = req.Dir
	m.Env = env
	m.Args = req.Args
	m.SID = newSID
	// item 3: размер контекста (min по серверам) → contextWindowSize кодера.
	m.ContextWindow = s.MinContextWindow()
	// Таймаут «панель готова» — из конфига (web.spawn_ready_sec, default 5).
	timeout := time.Duration(s.cfg.Web.SpawnReadySec) * time.Second
	// Spawn ДО записи: при неудаче (DIR_FORBIDDEN и пр.) записи не остаётся.
	rep, err := s.dispatchToNodeTimeout(r.Context(), host, m, timeout)
	if err != nil {
		httpError(w, http.StatusServiceUnavailable, "NODE", err.Error())
		return
	}
	if rep.Result != proto.ResSpawned {
		status := http.StatusBadRequest
		if rep.Result == "NAME_IN_USE" {
			status = http.StatusConflict
		}
		httpError(w, status, rep.Result, rep.Detail)
		return
	}
	now := s.clk.Now()
	rec := store.SessionRecord{
		SID: newSID, Name: req.Name, Host: host, HostIP: s.hostIP(host),
		Profile: "qwen", State: model.SessionIdle, StateChangedAt: now,
		Class: model.ClassNormal, ConstraintKind: model.ConstraintNone,
		AutoEnqueue: req.AutoEnqueue, CreatedAt: now,
	}
	if req.Pin != "" {
		rec.ConstraintKind = model.ConstraintPin
		rec.ConstraintServer = req.Pin
	} else if req.Prefer != "" {
		rec.ConstraintKind = model.ConstraintPrefer
		rec.ConstraintServer = req.Prefer
	}
	if err := s.store.CreateSession(rec); err != nil {
		httpError(w, http.StatusInternalServerError, "STORE", err.Error())
		return
	}
	// Панель с @runpilot_sid зарегистрирована сразу (CONTROL 5: ≤5s).
	pane := proto.Pane{PaneID: rep.Detail, SID: newSID, State: "IDLE"}
	s.hub.SetPane(host, pane)
	if s.sched != nil {
		s.sched.PaneUpdate(rep.Detail, newSID, model.PaneIdle, 0, now)
	}
	// Событие создания (SSE): открытое вебо узнаёт о сессии без перезагрузки
	// (до этого список обновлялся только при RESYNC/релоаде).
	s.recordSessionCreated(newSID, now)
	s.log.Info("api: spawn: сессия создана", "sid", newSID, "name", req.Name,
		"node", host, "pane", rep.Detail)
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{
		"sid": newSID, "name": req.Name, "node": host, "pane": rep.Detail,
	})
}

// handleNameCheck — GET /api/v1/sessions/name-check?name= (автодополнение).
func (s *Server) handleNameCheck(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	used := false
	detail := ""
	if name != "" {
		if live, err := s.store.FindLiveByName(name); err == nil {
			used, detail = true, live.SID
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"name": name, "used": used, "sid": detail})
}

// recordSessionCreated — событие создания сессии (SSE-лента): переход
// "" → IDLE. Клиент по незнакомому sid берёт полный срез /api/v1/state
// (в событии — только состояние, без полей сессии).
func (s *Server) recordSessionCreated(sidv string, now time.Time) {
	payload, _ := json.Marshal(map[string]any{"from": "", "to": string(model.SessionIdle)})
	_ = s.store.EventRecord(model.Event{TS: now, Kind: model.KindSessionState, SID: sidv, Payload: payload})
}

// pickNode — узел для spawn: по имени или первый онлайн.
func (s *Server) pickNode(name string) (string, error) {
	nodes := s.hub.Nodes()
	if name != "" {
		for _, n := range nodes {
			if n.Host == name {
				return name, nil
			}
		}
		return "", fmt.Errorf("узел %s не подключён", name)
	}
	if len(nodes) == 0 {
		return "", fmt.Errorf("нет подключённых узлов")
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Host < nodes[j].Host })
	return nodes[0].Host, nil
}

// spawnEnv — окружение агента из шаблона профиля (раздел 7 ТЗ).
func (s *Server) spawnEnv(sidv string) (map[string]string, error) {
	if s.prof == nil {
		return map[string]string{}, nil
	}
	data := map[string]any{
		"Gateway":    s.cfg.Coordinator.GatewayURL,
		"SID":        sidv,
		"ModelAlias": s.cfg.Profiles.Qwen.ModelAlias,
	}
	out := map[string]string{}
	for k, v := range s.prof.Env {
		var buf bytes.Buffer
		if err := template.Must(template.New("env").Parse(v)).Execute(&buf, data); err != nil {
			return nil, fmt.Errorf("шаблон env %s: %w", k, err)
		}
		out[k] = buf.String()
	}
	return out, nil
}

// hostIP — IP узла из hub (для записи сессии).
func (s *Server) hostIP(host string) string {
	n, ok := s.hub.Node(host)
	if !ok {
		return ""
	}
	return n.HostIP
}
