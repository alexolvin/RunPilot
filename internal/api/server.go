package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sort"
	"sync"
	"time"

	"runpilot/internal/clock"
	"runpilot/internal/config"
	"runpilot/internal/model"
	"runpilot/internal/monitor"
	"runpilot/internal/proto"
	"runpilot/internal/scheduler"
	"runpilot/internal/sid"
	"runpilot/internal/store"
	"runpilot/internal/texts"
	"runpilot/profiles"

	"github.com/gorilla/websocket"
)

// Server — HTTP/WS-сервер координатора (Э2: канал узла, runpilot run, state;
// Э4: планировщик, why, pause).
type Server struct {
	cfg   *config.Config
	store *store.Store
	hub   *Hub
	clk   clock.Clock
	log   *slog.Logger
	token string
	sched *scheduler.Scheduler
	mon   *monitor.Monitor
	prof  *profiles.Qwen
	// notify — уведомление оператора (Э7: Telegram); по умолчанию в лог.
	notify func(text string)
	// version — версия координатора (GET /api/v1/meta, v2 раздел 15.1).
	version string

	// serverDrain — drain/undrain сервера (runpilot server drain); ставится из
	// serve.go (шлюз меняет состояние, планировщик — через OnChange).
	serverDrain func(name string, on bool) error
	// serverReg — реестр серверов в работе (W6 CRUD): AddOrUpdate/Remove.
	serverReg serverReg

	// Активные SSE-подключения (cancel их контекста = разрыв для клиента;
	// используется стендом для проверки RESYNC, v2 CONTROL 2).
	sseMu      sync.Mutex
	sseSeq     int64
	sseCancels map[int64]context.CancelFunc

	// pendingNodeRemoval — узлы, ожидающие удаления after_turns (W6).
	removalMu      sync.Mutex
	pendingRemoval map[string]bool

	// idemCache — Idempotency-Key (O2): ответ первого запроса на повтор
	// (двойное нажатие / повтор сети) — тот же ответ, без повторного эффекта.
	idemMu     sync.Mutex
	idemCache  map[string]idemRec
	idemMaxAge time.Duration

	// warnedVer — версии Qwen Code, по которым уже прислан [WARN] untested (C6).
	warnedVerMu sync.Mutex
	warnedVer   map[string]bool

	// undoQueue — отмена массовой операции (O5): снимок до «Очистить очередь»
	// (вернуть записи) / «Вернуть все» (снова в HOLD) + дедлайн web.undo_sec.
	undoMu      sync.Mutex
	undoEntries []model.QueueEntry
	undoHolds   []string
	undoExpiry  time.Time
	undoKind    string

	// Терминал в браузере (13.4 ТЗ): открытые PTY-мосты (chan → сессия),
	// лимит terminals_per_node_max и таймер бездействия terminal_idle_min.
	termMu      sync.Mutex
	terminals   map[int]*terminalSession
	termSeq     int
	termMaxPerNode int
	termIdle    time.Duration
}

// idemRec — кэшированный ответ первого запроса с данным Idempotency-Key.
type idemRec struct {
	status      int
	contentType string
	body        []byte
	at          time.Time
}

// bodyCapture — перехват статуса и тела ответа для кэша идемпотентности.
type bodyCapture struct {
	w           http.ResponseWriter
	status      int
	contentType string
	body        *bytes.Buffer
}

func (b *bodyCapture) Header() http.Header { return b.w.Header() }

func (b *bodyCapture) WriteHeader(c int) {
	if b.status == 0 {
		b.status = c
	}
	b.w.WriteHeader(c)
}

func (b *bodyCapture) Write(p []byte) (int, error) {
	if b.status == 0 {
		b.status = http.StatusOK
	}
	if b.contentType == "" {
		b.contentType = b.w.Header().Get("Content-Type")
	}
	b.body.Write(p)
	return b.w.Write(p)
}

func (s *Server) idemGet(key string) (idemRec, bool) {
	s.idemMu.Lock()
	defer s.idemMu.Unlock()
	rec, ok := s.idemCache[key]
	if ok && s.clk.Now().Sub(rec.at) > s.idemMaxAge {
		delete(s.idemCache, key)
		ok = false
	}
	return rec, ok
}

func (s *Server) idemPut(key string, rec idemRec) {
	s.idemMu.Lock()
	defer s.idemMu.Unlock()
	// Ограниченный кэш: выкидываем устаревшие, при переполнении — всё.
	if len(s.idemCache) >= idemCacheMax {
		now := s.clk.Now()
		for k, r := range s.idemCache {
			if now.Sub(r.at) > s.idemMaxAge {
				delete(s.idemCache, k)
			}
		}
	}
	if len(s.idemCache) >= idemCacheMax {
		s.idemCache = map[string]idemRec{}
	}
	s.idemCache[key] = rec
}

// DropSSE — разорвать все активные SSE-подключения (стенд: проверка RESYNC).
func (s *Server) DropSSE() {
	s.sseMu.Lock()
	for _, c := range s.sseCancels {
		c()
	}
	s.sseCancels = map[int64]context.CancelFunc{}
	s.sseMu.Unlock()
}

// NewServer собирает сервер; токен — из env (coordinator.token_env).
func NewServer(cfg *config.Config, st *store.Store, hub *Hub, clk clock.Clock, log *slog.Logger) *Server {
	return &Server{
		cfg: cfg, store: st, hub: hub, clk: clk, log: log,
		token:          os.Getenv(cfg.Coordinator.TokenEnv),
		notify:         func(text string) { log.Info("runpilot: " + text) },
		sseCancels:     map[int64]context.CancelFunc{},
		pendingRemoval: map[string]bool{},
		idemCache:      map[string]idemRec{},
		idemMaxAge:     idemCacheTTL,
		warnedVer:      map[string]bool{},
		// Терминал (13.4 ТЗ): лимиты из web.* (0 — без ограничения).
		terminals:      map[int]*terminalSession{},
		termMaxPerNode: cfg.Web.TerminalsPerNodeMax,
		termIdle:       time.Duration(cfg.Web.TerminalIdleMin) * time.Minute,
	}
}

// SetNotify — хук уведомления оператора (Э7: Telegram; по умолчанию в лог).
func (s *Server) SetNotify(fn func(text string)) {
	if fn != nil {
		s.notify = fn
	}
}

// MarkNodeRemoval — пометить узел на удаление after_turns (W6 CONTROL 7).
func (s *Server) MarkNodeRemoval(host string) {
	s.removalMu.Lock()
	s.pendingRemoval[host] = true
	s.removalMu.Unlock()
}

// PendingRemoval — есть ли пометка удаления узла.
func (s *Server) PendingRemoval(host string) bool {
	s.removalMu.Lock()
	defer s.removalMu.Unlock()
	return s.pendingRemoval[host]
}

// SetVersion — версия координатора (для meta); ставится из serve.go.
func (s *Server) SetVersion(v string) { s.version = v }

// SetScheduler — планировщик (Э4): хуки снимков/узлов + why/pause.
func (s *Server) SetScheduler(sch *scheduler.Scheduler) { s.sched = sch }

// SetMonitor — монитор (Э6): серверная часть doctor (/api/v1/doctor).
func (s *Server) SetMonitor(mon *monitor.Monitor) { s.mon = mon }

// SetServerDrain — drain/undrain сервера (Э5); шлюз меняет состояние,
// планировщик узнаёт через Servers.OnChange.
func (s *Server) SetServerDrain(fn func(name string, on bool) error) { s.serverDrain = fn }

// SetProfile — профиль qwen (W6 spawn: окружение/команда агента).
func (s *Server) SetProfile(p *profiles.Qwen) { s.prof = p }

// Routes — все операторские/данные маршруты /api/v1/* БЕЗ аутентификации и
// БЕЗ канала узла (v2 раздел 15.1: веб-слушатель монтирует те же
// обработчики, что и API-слушатель, с cookie-аутентификацией).
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/run", s.handleRun)
	mux.HandleFunc("PATCH /api/v1/sessions/{sid}/pane", s.handlePane)
	// v2 (W6, 13.2): новая сессия из веба + проверка имени.
	mux.HandleFunc("POST /api/v1/sessions/spawn", s.handleSpawn)
	mux.HandleFunc("GET /api/v1/sessions/name-check", s.handleNameCheck)
	mux.HandleFunc("GET /api/v1/state", s.handleState)
	mux.HandleFunc("GET /api/v1/sessions", s.handleSessions)
	mux.HandleFunc("GET /api/v1/sessions/{sid}/why", s.handleWhy)
	mux.HandleFunc("GET /api/v1/sessions/{t}/peek", s.handlePeek)
	mux.HandleFunc("GET /api/v1/sessions/{t}/screen", s.handleScreen)
	mux.HandleFunc("POST /api/v1/sessions/{t}/enqueue", s.handleEnqueue)
	mux.HandleFunc("POST /api/v1/sessions/{t}/dequeue", s.handleDequeue)
	mux.HandleFunc("POST /api/v1/sessions/{t}/requeue", s.handleRequeue)
	mux.HandleFunc("POST /api/v1/queue/undo", s.handleQueueUndo)
	mux.HandleFunc("POST /api/v1/sessions/{t}/prio", s.handlePrio)
	mux.HandleFunc("POST /api/v1/sessions/{t}/pin", func(w http.ResponseWriter, r *http.Request) {
		s.handleConstraint(w, r, model.ConstraintPin)
	})
	mux.HandleFunc("POST /api/v1/sessions/{t}/prefer", func(w http.ResponseWriter, r *http.Request) {
		s.handleConstraint(w, r, model.ConstraintPrefer)
	})
	mux.HandleFunc("POST /api/v1/sessions/{t}/unpin", func(w http.ResponseWriter, r *http.Request) {
		s.handleConstraint(w, r, model.ConstraintNone)
	})
	mux.HandleFunc("POST /api/v1/sessions/{t}/hold", s.handleHold)
	mux.HandleFunc("POST /api/v1/sessions/{t}/unhold", s.handleUnhold)
	mux.HandleFunc("POST /api/v1/sessions/{t}/auto-enqueue", s.handleAutoEnqueue)
	mux.HandleFunc("POST /api/v1/sessions/{t}/cancel", s.handleCancel)
	mux.HandleFunc("POST /api/v1/pause", s.handlePause)
	mux.HandleFunc("POST /api/v1/resume", s.handleResume)
	// v2 (W7, 6.2): аварийная остановка + снятие.
	mux.HandleFunc("POST /api/v1/emergency", s.handleEmergency)
	mux.HandleFunc("POST /api/v1/emergency/release", s.handleEmergencyRelease)
	mux.HandleFunc("POST /api/v1/sessions/hold-emergency/requeue", s.handleRequeueEmergency)
	mux.HandleFunc("POST /api/v1/nodes/{h}/drain", s.handleNodeDrain)
	mux.HandleFunc("POST /api/v1/servers/{s}/drain", s.handleServerDrain)
	// v2 (W6, 5.1/5.2): серверы — мастер, удаление, управление.
	mux.HandleFunc("POST /api/v1/servers", s.handleServerCreate)
	mux.HandleFunc("POST /api/v1/servers/probe", s.handleServerProbe)
	mux.HandleFunc("GET /api/v1/servers/{name}", s.handleServerGet)
	mux.HandleFunc("PATCH /api/v1/servers/{name}", s.handleServerPatch)
	mux.HandleFunc("DELETE /api/v1/servers/{name}", s.handleServerDelete)
	mux.HandleFunc("POST /api/v1/servers/{name}/{op}", s.handleServerOp)
	// v2 (W7, 8.2): внешние кодеры — список, «Игнорировать», «Завершить».
	mux.HandleFunc("GET /api/v1/externals", s.handleExternals)
	mux.HandleFunc("POST /api/v1/externals/ignore", s.handleExternalIgnore)
	mux.HandleFunc("DELETE /api/v1/externals/ignore/{id}", s.handleExternalIgnoreDelete)
	mux.HandleFunc("POST /api/v1/externals/kill", s.handleExternalKill)
	// v2 (W6, раздел 14/5.3): узлы — подключение, список, удаление, обновление, dirs.
	mux.HandleFunc("POST /api/v1/nodes/enroll", s.handleNodeEnroll)
	mux.HandleFunc("GET /api/v1/nodes", s.handleNodes)
	mux.HandleFunc("DELETE /api/v1/nodes/{host}", s.handleNodeDelete)
	mux.HandleFunc("POST /api/v1/nodes/{host}/update", s.handleNodeUpdate)
	mux.HandleFunc("POST /api/v1/nodes/{host}/qwen-settings", s.handleNodeQwenSettings)
	mux.HandleFunc("GET /api/v1/nodes/{host}/dirs", s.handleNodeDirs)
	mux.HandleFunc("GET /api/v1/queue", s.handleQueue)
	mux.HandleFunc("GET /api/v1/servers", s.handleServers)
	mux.HandleFunc("GET /api/v1/stats", s.handleStats)
	mux.HandleFunc("GET /api/v1/events", s.handleEvents)
	mux.HandleFunc("GET /api/v1/journal", s.handleJournal)
	mux.HandleFunc("GET /api/v1/monitoring", s.handleMonitoring)
	mux.HandleFunc("GET /api/v1/notifications", s.handleNotifications)
	mux.HandleFunc("POST /api/v1/notifications/{id}/read", s.handleNotificationRead)
	mux.HandleFunc("POST /api/v1/notifications/read-all", s.handleNotificationsReadAll)
	mux.HandleFunc("GET /api/v1/doctor", s.handleDoctor)
	mux.HandleFunc("GET /api/v1/meta", s.handleMeta)
	mux.HandleFunc("POST /api/v1/internal/dispatch", s.handleDispatch)
	mux.HandleFunc("GET /api/v1/internal/dispatch-latency", s.handleDispatchLatency)
	// v2 (раздел 4): рабочие настройки — схема, изменения, ревизии, откат,
	// экспорт и импорт.
	mux.HandleFunc("GET /api/v1/settings", s.handleGetSettings)
	mux.HandleFunc("PATCH /api/v1/settings", s.handlePatchSettings)
	mux.HandleFunc("GET /api/v1/settings/schema", s.handleSettingsSchema)
	mux.HandleFunc("GET /api/v1/settings/revisions", s.handleSettingsRevisions)
	mux.HandleFunc("POST /api/v1/settings/revert", s.handleSettingsRevert)
	mux.HandleFunc("GET /api/v1/settings/export", s.handleSettingsExport)
	mux.HandleFunc("POST /api/v1/settings/import", s.handleSettingsImport)
	// v2 (раздел 12): командная строка — общий разбор веб/Telegram.
	mux.HandleFunc("POST /api/v1/command", s.handleCommand)
	mux.HandleFunc("GET /api/v1/command/complete", s.handleCommandComplete)
	mux.HandleFunc("POST /api/v1/sessions/{t}/paste", s.handlePaste)
	mux.HandleFunc("POST /api/v1/sessions/{t}/approve", s.handleApprove)
	mux.HandleFunc("POST /api/v1/sessions/{t}/compress", s.handleCompress)
	// v2 (8.3 X1, 5.4): приём открытых tmux-панелей («вне runpilot») и удаление
	// GONE-сессий.
	mux.HandleFunc("POST /api/v1/unmanaged/{host}/{pane_id}/adopt", s.handleAdopt)
	mux.HandleFunc("DELETE /api/v1/sessions/{sid}", s.handleSessionDelete)
	return mux
}

// EnrollHandler — /enroll/<token>/… (14.2): токен в пути — аутентификация.
// Отдельный mux: API-слушатель монтирует его в Handler(), а веб-слушатель —
// публично, без cookie (curl -fsSL … | bash с чистой машины не имеет
// cookie веба; Routes() эти маршруты не содержит).
func (s *Server) EnrollHandler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/enroll/{token}/{rest...}", http.HandlerFunc(s.handleEnroll))
	return mux
}

// Handler — API-слушатель (v2 раздел 2.1): Bearer-токен оператора +
// канал узла (WS). Канал узла НЕ монтируется на веб-слушатель.
// W6: дистрибутив (только токен узла) и /enroll/<token>/ (токен в пути)
// идут ДО общего /api/v1/ со своей аутентификацией.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /api/v1/node", s.nodeAuth(http.HandlerFunc(s.handleNodeWS)))
	mux.Handle("GET /api/v1/dist/", http.HandlerFunc(s.handleDist))
	mux.Handle("/enroll/", s.EnrollHandler())
	// runpilot exec (раздел 8.1): токен оператора ИЛИ узла с host_ip — своя
	// аутентификация. Точные method+path-паттерны (без subtree-редиректа).
	mux.Handle("POST /api/v1/jobs", s.jobsAuth(http.HandlerFunc(s.handleJobCreate)))
	mux.Handle("GET /api/v1/jobs/{sid}/wait", s.jobsAuth(http.HandlerFunc(s.handleJobWait)))
	mux.Handle("POST /api/v1/jobs/{sid}/heartbeat", s.jobsAuth(http.HandlerFunc(s.handleJobHeartbeat)))
	mux.Handle("POST /api/v1/jobs/{sid}/finish", s.jobsAuth(http.HandlerFunc(s.handleJobFinish)))
	mux.Handle("/api/v1/", s.auth(s.idempotent(s.Routes())))
	return mux
}

// auth — Bearer $RUNPILOT_TOKEN (раздел 14 ТЗ); пустой токен — локальный режим.
func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.token != "" {
			if r.Header.Get("Authorization") != "Bearer "+s.token {
				httpError(w, http.StatusUnauthorized, "UNAUTHORIZED", "нет Bearer-токена")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// idempotent — Idempotency-Key (O2): повтор того же запроса (двойное нажатие
// или повтор сети) возвращает ответ первого запроса, эффект применяется один
// раз. Без заголовка — прозрачный проход. Кэшируются только 2xx-ответы.
func (s *Server) idempotent(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Idempotency-Key")
		if key == "" {
			next.ServeHTTP(w, r)
			return
		}
		if rec, ok := s.idemGet(key); ok {
			w.Header().Set("Content-Type", rec.contentType)
			w.WriteHeader(rec.status)
			_, _ = w.Write(rec.body)
			return
		}
		buf := &bodyCapture{w: w, body: &bytes.Buffer{}}
		next.ServeHTTP(buf, r)
		if buf.status >= status2xxLo && buf.status < status2xxHi {
			s.idemPut(key, idemRec{status: buf.status, contentType: buf.contentType,
				body: append([]byte(nil), buf.body.Bytes()...), at: s.clk.Now()})
		}
	})
}

// --- runpilot run (коллизии раздела 8 ТЗ) ---

// RunRequest — регистрация сессии.
type RunRequest struct {
	Name         string `json:"name"`
	Host         string `json:"host"`
	HostIP       string `json:"host_ip"`
	Profile      string `json:"profile"`
	AgentVersion string `json:"agent_version"`
	Prio         string `json:"prio"`
	Pin          string `json:"pin"`
	Prefer       string `json:"prefer"`
	AutoEnqueue  bool   `json:"auto_enqueue"`
}

// RunResponse — результат регистрации.
type RunResponse struct {
	SID string `json:"sid"`
}

func (s *Server) handleRun(w http.ResponseWriter, r *http.Request) {
	var req RunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	if req.Profile != "qwen" {
		httpError(w, http.StatusBadRequest, "BAD_AGENT", "профиль только qwen")
		return
	}
	if req.Name == "" || req.Host == "" {
		httpError(w, http.StatusBadRequest, "BAD_REQUEST", "name и host обязательны")
		return
	}

	// Имя занято живой сессией runpilot → NAME_IN_USE.
	if live, err := s.store.FindLiveByName(req.Name); err == nil {
		httpError(w, http.StatusConflict, "NAME_IN_USE",
			fmt.Sprintf("имя занято сессией %s (%s)", live.SID, live.State))
		return
	}
	// Имя совпадает с GONE младше срока хранения → NAME_IN_USE с sid
	// старой записи.
	if gone, err := s.store.FindSessionByName(req.Name); err == nil && gone.State == model.SessionGone {
		age := s.clk.Now().Sub(gone.StateChangedAt)
		if age < time.Duration(s.cfg.Retention.GoneSessionsDays)*hoursPerDay*time.Hour {
			httpError(w, http.StatusConflict, "NAME_IN_USE",
				fmt.Sprintf("имя занято GONE-записью %s (снимется через retention)", gone.SID))
			return
		}
	}

	prio := req.Prio
	if prio == "" {
		prio = model.ClassNormal.String() // класс по умолчанию — NORMAL
	}
	class, ok := model.QueueClassParse(prio)
	if !ok {
		httpError(w, http.StatusBadRequest, "BAD_PRIO", "неизвестный класс очереди: "+prio)
		return
	}
	c := model.NoConstraint
	if req.Pin != "" {
		c = model.Constraint{Kind: model.ConstraintPin, Server: req.Pin}
	} else if req.Prefer != "" {
		c = model.Constraint{Kind: model.ConstraintPrefer, Server: req.Prefer}
	}
	newSID, err := sid.New()
	if err != nil {
		httpError(w, http.StatusInternalServerError, "SID", err.Error())
		return
	}
	now := s.clk.Now()
	rec := store.SessionRecord{
		SID: newSID, Name: req.Name, Host: req.Host, HostIP: req.HostIP,
		Profile: req.Profile, AgentVersion: req.AgentVersion,
		State: model.SessionIdle, StateChangedAt: now,
		Class: class, ConstraintKind: c.Kind, ConstraintServer: c.Server,
		AutoEnqueue: req.AutoEnqueue, CreatedAt: now,
	}
	if err := s.store.CreateSession(rec); err != nil {
		s.log.Error("api: runpilot run: создание сессии: " + err.Error())
		httpError(w, http.StatusInternalServerError, "STORE", err.Error())
		return
	}
	s.checkAgentVersion(newSID, req.AgentVersion) // C6: версия без фикстур → WARN
	s.recordSessionCreated(newSID, now) // SSE: вебо узнаёт о сессии без релоада
	s.log.Info("api: runpilot run: сессия создана", "sid", newSID, "name", req.Name)
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(RunResponse{SID: newSID})
}

// handlePane — клиент сообщает панель после tmux new-session.
func (s *Server) handlePane(w http.ResponseWriter, r *http.Request) {
	si := r.PathValue("sid")
	var body struct {
		TmuxSession  string `json:"tmux_session"`
		Window       int    `json:"window"`
		PaneID       string `json:"pane_id"`
		AgentVersion string `json:"agent_version"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	if body.PaneID == "" {
		httpError(w, http.StatusBadRequest, "BAD_REQUEST", "pane_id обязателен")
		return
	}
	if err := s.store.UpdateSessionPane(si, body.TmuxSession, body.PaneID, body.AgentVersion, body.Window); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			httpError(w, http.StatusNotFound, "NOT_FOUND", "нет сессии")
			return
		}
		httpError(w, http.StatusInternalServerError, "STORE", err.Error())
		return
	}
	if body.AgentVersion != "" {
		s.checkAgentVersion(si, body.AgentVersion) // C6: версия без фикстур → WARN
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleState — срез координатора (state, mode, nodes) для веба и CLI.
func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	sessions, err := s.store.ListSessions(false)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "STORE", err.Error())
		return
	}
	st := s.hub.State(sessions)
	mode := "NORMAL"
	if s.sched != nil {
		mode = s.sched.Mode()
	}
	// v2 15.2: сущности (сессия, узел) несут доступные действия.
	view := StateView{Mode: mode, Panes: st.Panes}
	for _, rec := range st.Sessions {
		view.Sessions = append(view.Sessions, sessionView(rec))
	}
	for _, n := range st.Nodes {
		nv := nodeView(n)
		nv.Unmanaged = s.hub.UnmanagedCount(n.Host)
		if hv, ok := s.hub.NodeHealth(n.Host); ok {
			nv.Health = &hv
		}
		view.Nodes = append(view.Nodes, nv)
	}
	if s.sched != nil {
		view.Servers = s.sched.ServersView()
	}
	// W9 доп-3c: эндпоинт шлюза для диалога «Новая сессия».
	view.Gateway = GatewayInfo{
		URL:        s.cfg.Coordinator.GatewayURL,
		ModelAlias: s.cfg.Profiles.Qwen.ModelAlias,
	}
	// 8.3 X1: «вне runpilot» — живые UNMANAGED-панели узлов.
	view.Unmanaged = st.Unmanaged
	json.NewEncoder(w).Encode(view)
}

// handleMeta — GET /api/v1/meta (v2 раздел 15.1): версия, режим,
// перечисления с подписями (из internal/texts), единицы.
func (s *Server) handleMeta(w http.ResponseWriter, r *http.Request) {
	mode := "NORMAL"
	if s.sched != nil {
		mode = s.sched.Mode()
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(texts.Meta(s.version, mode))
}

// handleSessions — список сессий (runpilot ls, Э5).
func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	all := r.URL.Query().Get("all") == "true"
	sessions, err := s.store.ListSessions(all)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "STORE", err.Error())
		return
	}
	// v2 15.2: сессии несут доступные действия.
	views := make([]SessionView, 0, len(sessions))
	for _, rec := range sessions {
		views = append(views, sessionView(rec))
	}
	json.NewEncoder(w).Encode(views)
}

// handleDispatch — Э2-замер dispatch p95: координатор → узел → reply.
// С Э4 dispatch вызывает планировщик; метод hub.Dispatch общий.
func (s *Server) handleDispatch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SID        string `json:"sid"`
		PaneID     string `json:"pane_id"`
		Mode       string `json:"mode"`
		ExpectHash uint64 `json:"expect_hash"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	paneID := body.PaneID
	if paneID == "" {
		info, ok := s.hub.PaneBySID(body.SID)
		if !ok {
			httpError(w, http.StatusNotFound, "PANE_NOT_FOUND", "нет снимка панели сессии")
			return
		}
		paneID = info.Pane.PaneID
	}
	m := proto.New(proto.KindDispatch)
	m.SID, m.PaneID, m.Mode, m.ExpectHash = body.SID, paneID, body.Mode, body.ExpectHash

	ctx, cancel := context.WithTimeout(r.Context(),
		time.Duration(s.cfg.Dispatch.NodeReplyTimeoutSec)*time.Second)
	defer cancel()
	t0 := s.clk.Now()
	reply, err := s.hub.Dispatch(ctx, paneID, m)
	if err != nil {
		httpError(w, http.StatusBadGateway, "DISPATCH", err.Error())
		return
	}
	json.NewEncoder(w).Encode(map[string]any{
		"result": reply.Result, "detail": reply.Detail,
		"rtt_ms": s.clk.Now().Sub(t0).Milliseconds(),
	})
}

// handleWhy — GET /api/v1/sessions/{sid}/why (раздел 5 ТЗ): причина,
// набор кандидатов и невыполненные предикаты.
func (s *Server) handleWhy(w http.ResponseWriter, r *http.Request) {
	if s.sched == nil {
		httpError(w, http.StatusServiceUnavailable, "NO_SCHEDULER", "планировщик не запущен")
		return
	}
	why, err := s.sched.Why(r.PathValue("sid"))
	if err != nil {
		httpError(w, http.StatusNotFound, "NOT_FOUND", "нет сессии")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(why)
}

// handlePause — POST /api/v1/pause {on: bool} (раздел 5 ТЗ).
func (s *Server) handlePause(w http.ResponseWriter, r *http.Request) {
	if s.sched == nil {
		httpError(w, http.StatusServiceUnavailable, "NO_SCHEDULER", "планировщик не запущен")
		return
	}
	var body struct {
		On bool `json:"on"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	s.sched.Pause(body.On)
	w.WriteHeader(http.StatusNoContent)
}

// handleDispatchLatency — GET /api/v1/internal/dispatch-latency:
// задержки диспетчеризации, мс (приёмка p95 ≤ budget_p95_ms).
func (s *Server) handleDispatchLatency(w http.ResponseWriter, r *http.Request) {
	if s.sched == nil {
		httpError(w, http.StatusServiceUnavailable, "NO_SCHEDULER", "планировщик не запущен")
		return
	}
	samples := s.sched.DispatchLatency()
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	p95 := int64(0)
	if n := len(samples); n > 0 {
		idx := n * p95Percent / percentWhole
		if idx >= n {
			idx = n - 1
		}
		p95 = samples[idx]
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"n": len(samples), "p95_ms": p95, "samples_ms": samples,
	})
}

// --- канал узла (WS, раздел 13 ТЗ) ---

var wsUpgrader = websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

func (s *Server) handleNodeWS(w http.ResponseWriter, r *http.Request) {
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	// Первое сообщение — hello; неизвестная мажорная версия → закрытие
	// соединения (раздел 13 ТЗ).
	var hello proto.Msg
	if err := conn.ReadJSON(&hello); err != nil {
		return
	}
	if hello.Type != proto.KindHello || !proto.Acceptable(hello.Proto) {
		_ = conn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseProtocolError, "неизвестная версия протокола"),
			time.Now().UTC().Add(time.Second))
		return
	}
	// host_ip обязан совпадать с удалённым адресом соединения (раздел 14 ТЗ).
	remoteIP, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || remoteIP != hello.HostIP {
		s.log.Warn("api: node: host_ip не совпадает", "host", hello.Host,
			"hello", hello.HostIP, "remote", remoteIP)
		_ = conn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseProtocolError, "host_ip не совпадает с удалённым адресом"),
			time.Now().UTC().Add(time.Second))
		return
	}

	host := hello.Host
	if host == "" {
		host = remoteIP
	}
	// N7: конфликт имени хоста — hello утверждает существующий host, а токен
	// привязан к другому. Соединение закрывается, имя хоста не меняется.
	if th := nodeTokenHost(r.Context()); th != "" && th != host {
		if _, exists := s.hub.Registered(host); exists {
			s.log.Warn("api: node: конфликт имени хоста", "host", host,
				"token_host", th, "addr", r.RemoteAddr)
			_ = conn.WriteControl(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "HOST_CONFLICT: имя хоста занято узлом с другим токеном"),
				time.Now().UTC().Add(time.Second))
			return
		}
	}
	lc := &lockedNodeConn{Conn: conn}
	s.hub.Register(&NodeInfo{
		Host: host, HostIP: hello.HostIP,
		RUNPILOTVersion: hello.RUNPILOTVersion, TmuxVersion: hello.TmuxVersion,
		OS: hello.OS, Sockets: hello.Sockets, Conn: lc,
		PtySend: func(c int, d []byte) error { return lc.writeBinary(proto.EncodeFrame(c, d)) },
	})
	defer func() {
		s.hub.Unregister(host)
		if s.sched != nil {
			s.sched.NodeLost(host)
		}
		// 13.4: узел отключился — закрыть его терминалы (PTY ушёл с ним).
		s.closePtyByHost(host)
	}()
	s.log.Info("api: node: подключился", "host", host, "runpilot", hello.RUNPILOTVersion)
	if s.sched != nil {
		s.sched.NodeSeen(host)
	}
	// W6 (CONTROL 2/3): версия узла != версия координатора и узел умеет
	// self-update → отправить update (бинарник проверяется SHA-256 + selftest).
	if s.version != "" && hello.RUNPILOTVersion != s.version && hasFeature(hello.Features, "update") {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), s.updateTimeout())
			defer cancel()
			if err := s.triggerNodeUpdate(ctx, host); err != nil {
				s.log.Warn("api: node: self-update: "+err.Error(), "host", host)
			}
		}()
	}
	// W9 доп-3c: hello-ответ — конфиг узла: эндпоинт qwen code (model_alias +
	// gateway_url). Узел приводит ~/.qwen/settings.json в порядок (idempotent),
	// чтобы окружение сессии OPENAI_BASE_URL шлюза не перебивалось.
	cfgMsg := proto.New(proto.KindConfig)
	cfgMsg.ModelAlias = s.cfg.Profiles.Qwen.ModelAlias
	cfgMsg.GatewayURL = s.cfg.Coordinator.GatewayURL
	if err := lc.WriteJSON(cfgMsg); err != nil {
		s.log.Warn("api: node: config (hello-ответ): "+err.Error(), "host", host)
		return
	}

	for {
		msgType, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		// Двоичный кадр = вывод PTY терминала с номером канала (13.4 ТЗ):
		// в браузер (мост терминала).
		if msgType == websocket.BinaryMessage {
			chan_, ptyData := proto.DecodeFrame(data)
			s.applyPtyData(chan_, ptyData)
			continue
		}
		var m proto.Msg
		if err := json.Unmarshal(data, &m); err != nil {
			return
		}
		switch m.Type {
		case proto.KindPanes:
			s.applyPanes(host, m.Panes)
		case proto.KindPane:
			if m.Pane != nil {
				s.hub.SetPane(host, *m.Pane)
				if s.sched != nil {
					s.sched.PaneUpdate(m.Pane.PaneID, m.Pane.SID,
						model.PaneState(m.Pane.State), m.Pane.Hash, s.clk.Now())
				}
			}
		case proto.KindPaneGone:
			s.onPaneGone(m.PaneID, m.SID)
		case proto.KindUnmanaged:
			s.hub.SetUnmanaged(host, m.Unmanaged)
		case proto.KindReply:
			s.hub.DeliverReply(m)
		case proto.KindGPU:
			// Э6: телеметрия GPU узла (server → карты).
			s.hub.SetGPU(m.Server, host, m.Cards)
		case proto.KindExternal:
			// 8.2: внешние кодеры вне tmux/runpilot.
			s.applyExternals(host, m.Externals)
		case proto.KindPtyExit:
			// 13.4: tmux attach завершился (сессия убита) — закрыть браузер.
			s.closePty(m.Chan)
		case proto.KindAgentExit, proto.KindNodeHealth, proto.KindUpdateStatus:
			s.onNodeInbound(host, m)
		default:
			s.log.Warn("api: node: неизвестное сообщение", "type", m.Type, "host", host)
		}
	}
}

// onNodeInbound — проактивные сообщения узла proto 2: agent_exit (агент в
// панели завершился → планировщику тик по sid), node_health (диск/qwen),
// update_status (результат self-update).
func (s *Server) onNodeInbound(host string, m proto.Msg) {
	switch m.Type {
	case proto.KindAgentExit:
		// 7.3 C1: кодер в панели завершился/убит → аренда AGENT_EXITED,
		// ход LOST, сессия HOLD(AGENT_EXITED).
		s.log.Info("api: node: агент завершился", "host", host, "sid", m.SID, "code", m.ExitCode)
		if s.sched != nil && m.SID != "" {
			s.sched.AgentExit(m.SID, m.ExitCode)
		}
	case proto.KindNodeHealth:
		s.applyNodeHealth(host, m)
	case proto.KindUpdateStatus:
		s.log.Info("api: node: self-update", "host", host, "result", m.Result,
			"version", m.Version)
	}
}

// applyNodeHealth — node_health узла (N9/N10, 14.4): проблемы (нет tmux/qwen,
// мало места на диске) и расхождение часов — в hub (страница узла) + [WARN].
func (s *Server) applyNodeHealth(host string, m proto.Msg) {
	var problems []string
	if m.TmuxVersion == "" {
		problems = append(problems, "no_tmux")
	}
	if m.QwenPath == "" {
		problems = append(problems, "no_qwen")
	}
	if m.DiskFreeMB > 0 && m.DiskFreeMB < s.cfg.Monitor.DiskFreeMinMB {
		problems = append(problems, "disk_low")
	}
	var skewMS int64
	if m.NodeTimeMS > 0 {
		skewMS = m.NodeTimeMS - s.clk.Now().UnixMilli()
	}
	s.hub.SetNodeHealth(host, NodeHealthView{
		Problems:    problems,
		TimeSkewMS:  skewMS,
		DiskFreeMB:  m.DiskFreeMB,
		QwenVersion: m.QwenVersion,
	})
	if len(problems) > 0 {
		s.log.Warn("api: node: здоровье: проблемы", "host", host, "problems", problems)
	}
}

// applyPanes — полный список панелей узла: снимки в hub + diff на
// исчезнувшие (PaneGone, Э4) + хук планировщика по каждому снимку.
func (s *Server) applyPanes(host string, panes []proto.Pane) {
	newIDs := map[string]bool{}
	for _, p := range panes {
		newIDs[p.PaneID] = true
	}
	goneSIDs := s.hub.DropPanesOfHost(host, newIDs)
	// C10: снят @runpilot_sid → панель есть, опции нет → сессия GONE, панель UNMANAGED.
	goneSIDs = append(goneSIDs, s.hub.MarkUnmanaged(host, panes)...)
	s.hub.SetPanes(host, panes)
	// C6: узел сообщает версию кодера в снимке (version_cmd при запуске).
	for _, p := range panes {
		s.syncAgentVersion(p)
	}
	if s.sched != nil {
		for _, sid := range goneSIDs {
			s.sched.PaneGone("", sid)
		}
		for _, p := range panes {
			s.sched.PaneUpdate(p.PaneID, p.SID,
				model.PaneState(p.State), p.Hash, s.clk.Now())
		}
	}
	s.reconcilePanesWithStore(host, panes)
}

// reconcilePanesWithStore — полная сверка с БД: сессия (PANE, не GONE)
// узла, чья панель отсутствует в полном списке узла → GONE. Закрывает
// сценарий «потерянная сессия»: координатор перезапустился, полный список
// пришёл уже без панели (hub её не знал — diff по памяти не сработал).
func (s *Server) reconcilePanesWithStore(host string, panes []proto.Pane) {
	inList := map[string]bool{}
	for _, p := range panes {
		if p.SID != "" {
			inList[p.SID] = true
		}
	}
	recs, err := s.store.ListSessions(true)
	if err != nil {
		return
	}
	gone := 0
	for _, r := range recs {
		if r.Host != host || r.Kind != model.KindPane ||
			r.State == model.SessionGone || inList[r.SID] {
			continue
		}
		if s.sched != nil {
			s.sched.PaneGone("", r.SID)
		}
		gone++
	}
	if gone > 0 {
		s.log.Warn("api: node: сверка с БД: панели не найдены → GONE",
			"host", host, "count", gone)
	}
}

// ApplyNodeMsg — сообщение «узел → координатор» тем же путём, что и
// WS-транспорт (v2 раздел 2.5: встроенный узел). Хост уже зарегистрирован.
func (s *Server) ApplyNodeMsg(host string, m proto.Msg) {
	switch m.Type {
	case proto.KindPanes:
		s.applyPanes(host, m.Panes)
	case proto.KindPane:
		if m.Pane != nil {
			s.hub.SetPane(host, *m.Pane)
			if s.sched != nil {
				s.sched.PaneUpdate(m.Pane.PaneID, m.Pane.SID,
					model.PaneState(m.Pane.State), m.Pane.Hash, s.clk.Now())
			}
		}
	case proto.KindPaneGone:
		s.onPaneGone(m.PaneID, m.SID)
	case proto.KindUnmanaged:
		s.hub.SetUnmanaged(host, m.Unmanaged)
	case proto.KindReply:
		s.hub.DeliverReply(m)
	case proto.KindGPU:
		s.hub.SetGPU(m.Server, host, m.Cards)
	case proto.KindExternal:
		s.applyExternals(host, m.Externals)
	case proto.KindPtyExit:
		// 13.4: tmux attach завершился (сессия убита) — закрыть браузер.
		s.closePty(m.Chan)
	case proto.KindAgentExit, proto.KindNodeHealth, proto.KindUpdateStatus:
		s.onNodeInbound(host, m)
	default:
		s.log.Warn("api: node: неизвестное сообщение", "type", m.Type, "host", host)
	}
}

// onPaneGone — панель исчезла (delta узла): снять снимок, сессию — в GONE.
func (s *Server) onPaneGone(paneID, sid string) {
	if id := s.hub.DropPane(paneID); id != "" {
		sid = id
	}
	if s.sched != nil && sid != "" {
		s.sched.PaneGone(paneID, sid)
	}
}

// ApplyPtyData — вывод PTY с узла (13.4) тем же путём, что и WS-транспорт:
// встроенный узел шлёт кадры напрямую (нет бинарного WS). В браузер-мост.
func (s *Server) ApplyPtyData(chan_ int, data []byte) {
	s.applyPtyData(chan_, data)
}

// NodeSeen — узел зарегистрирован (встроенный узел, v2 2.5): уведомить
// планировщик (раздел 13 ТЗ).
func (s *Server) NodeSeen(host string) {
	if s.sched != nil {
		s.sched.NodeSeen(host)
	}
}

// NodeLost — узел отключился: уведомить планировщик (раздел 13 ТЗ).
func (s *Server) NodeLost(host string) {
	if s.sched != nil {
		s.sched.NodeLost(host)
	}
}

// httpError — тело ошибки: код + текущее состояние/подробности
// (раздел 13 ТЗ: 409 недопустимый переход — тело содержит код).
func httpError(w http.ResponseWriter, status int, code, detail string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"code": code, "detail": detail})
}
