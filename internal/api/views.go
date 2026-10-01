package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"runpilot/internal/model"
	"runpilot/internal/scheduler"
	"runpilot/internal/store"
)

// schedOr503 — планировщик обязан быть (все команды TUI/CLI).
func (s *Server) schedOr503(w http.ResponseWriter) bool {
	if s.sched == nil {
		httpError(w, http.StatusServiceUnavailable, "NO_SCHEDULER", "планировщик не запущен")
		return false
	}
	return true
}

// --- сущности с полем actions (v2 раздел 15.2) ---

// SessionView — сессия + действия (поля store.SessionRecord раскрываются
// плоским JSON-ключом; добавляются actions, untested).
type SessionView struct {
	store.SessionRecord
	Actions  []model.Action `json:"actions"`
	Untested bool           `json:"untested,omitempty"`
}

// NodeView — узел + действия (плоско, как api.NodeInfo без Conn).
type NodeView struct {
	Host        string   `json:"Host"`
	HostIP      string   `json:"HostIP"`
	RUNPILOTVersion  string   `json:"RUNPILOTVersion"`
	TmuxVersion string   `json:"TmuxVersion"`
	OS          string   `json:"OS"`
	Sockets     []string `json:"Sockets"`
	// W6 (5.3, J7): число отцепленных (UNMANAGED) панелей узла.
	Unmanaged int          `json:"unmanaged"`
	Actions   []model.Action `json:"actions"`
	// W7 (14.4, N9/N10): здоровье узла (проблемы, расхождение часов, диск).
	Health *NodeHealthView `json:"health,omitempty"`
}

// GatewayInfo — эндпоинт шлюза для диалога «Новая сессия» (W9 доп-3c):
// с каким окружением runpilot запускает кодера и что требуется в конфиге qwen.
type GatewayInfo struct {
	URL        string `json:"url"`
	ModelAlias string `json:"model_alias"`
}

// StateView — срез координатора с действиями сущностей (v2 15.2).
type StateView struct {
	Mode     string            `json:"mode"`
	Sessions []SessionView     `json:"sessions"`
	Panes    []PaneInfo        `json:"panes"`
	Nodes    []NodeView        `json:"nodes"`
	Servers  []scheduler.ServerRow `json:"servers"`
	Gateway  GatewayInfo       `json:"gateway"`
	// 8.3 X1: живые UNMANAGED-панели («вне runpilot») по узлам.
	Unmanaged []UnmanagedView `json:"unmanaged,omitempty"`
}

// sessionView — запись сессии → с действиями (вычисляет сервер, untested C6).
func sessionView(rec store.SessionRecord) SessionView {
	return SessionView{SessionRecord: rec, Actions: model.SessionActions(rec.State),
		Untested: qwenUntested(rec.AgentVersion)}
}

// nodeView — узел → с действиями.
func nodeView(n NodeInfo) NodeView {
	return NodeView{
		Host: n.Host, HostIP: n.HostIP, RUNPILOTVersion: n.RUNPILOTVersion,
		TmuxVersion: n.TmuxVersion, OS: n.OS, Sockets: n.Sockets,
		Actions: model.NodeActions(),
	}
}

// --- чтение: очередь, серверы, статистика, экран ---

// handleQueue — GET /api/v1/queue (раздел 11 ТЗ; колонка WHY).
func (s *Server) handleQueue(w http.ResponseWriter, r *http.Request) {
	if !s.schedOr503(w) {
		return
	}
	rows := s.sched.QueueView()
	for i := range rows {
		if info, ok := s.hub.PaneBySID(rows[i].SID); ok {
			rows[i].Prompt = info.Pane.InputPreview
		}
	}
	writeJSON(w, rows)
}

// handleServers — GET /api/v1/servers (раздел 11 ТЗ).
func (s *Server) handleServers(w http.ResponseWriter, r *http.Request) {
	if !s.schedOr503(w) {
		return
	}
	writeJSON(w, s.sched.ServersView())
}

// doctorServerRow — сервер в /api/v1/doctor (серверная часть, Э6).
type doctorServerRow struct {
	Name           string   `json:"name"`
	State          string   `json:"state,omitempty"`
	MetricsMissing bool     `json:"metrics_missing"`
	FoundVLLM      []string `json:"found_vllm,omitempty"`
	AbsentVLLM     []string `json:"absent_vllm,omitempty"`
}

// handleDoctor — GET /api/v1/doctor (раздел 13 ТЗ): серверная часть
// проверок. По каждому серверу: состояние, metrics_missing (раздел 5
// ТЗ) и найденные/отсутствующие vllm: метрики (раздел 10 ТЗ).
func (s *Server) handleDoctor(w http.ResponseWriter, r *http.Request) {
	states := map[string]string{}
	if s.sched != nil {
		for _, v := range s.sched.ServersView() {
			states[v.Name] = v.State
		}
	}
	rows := make([]doctorServerRow, 0, len(s.cfg.Servers))
	for _, sc := range s.cfg.Servers {
		row := doctorServerRow{Name: sc.Name, State: states[sc.Name]}
		if s.mon != nil {
			if last, ok := s.mon.LastMetrics(sc.Name); ok {
				row.FoundVLLM = last.Found
				row.AbsentVLLM = last.Missing()
			}
			row.MetricsMissing = s.mon.Sample(sc.Name).Missing
		}
		rows = append(rows, row)
	}
	writeJSON(w, map[string]any{"servers": rows})
}

// handleStats — GET /api/v1/stats?since=24h (раздел 11 ТЗ; без токенов).
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	if !s.schedOr503(w) {
		return
	}
	sinceDur := statsDefaultHours * time.Hour
	if v := r.URL.Query().Get("since"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			sinceDur = d
		}
	}
	res, err := s.sched.Stats(s.clk.Now().Add(-sinceDur))
	if err != nil {
		httpError(w, http.StatusInternalServerError, "STORE", err.Error())
		return
	}
	writeJSON(w, res)
}

// handlePeek — GET /api/v1/sessions/{t}/peek?n=40 (раздел 11 ТЗ).
func (s *Server) handlePeek(w http.ResponseWriter, r *http.Request) {
	if !s.schedOr503(w) {
		return
	}
	n := peekDefault
	if v := r.URL.Query().Get("n"); v != "" {
		if x, err := strconv.Atoi(v); err == nil && x > 0 {
			n = x
		}
	}
	text, err := s.sched.Peek(r.PathValue("t"), n)
	if err != nil {
		httpError(w, http.StatusNotFound, "PANE", err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(text))
}

// --- команды очереди (раздел 11 ТЗ) ---

// handleEnqueue — POST /api/v1/sessions/{t}/enqueue.
func (s *Server) handleEnqueue(w http.ResponseWriter, r *http.Request) {
	if !s.schedOr503(w) {
		return
	}
	var body struct {
		Class  string `json:"class"`
		Pin    string `json:"pin"`
		Prefer string `json:"prefer"`
		Front  bool   `json:"front"`
		After  string `json:"after"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	class := model.ClassNormal
	if body.Class != "" {
		c, ok := model.QueueClassParse(body.Class)
		if !ok {
			httpError(w, http.StatusBadRequest, "BAD_PRIO", "неизвестный класс: "+body.Class)
			return
		}
		class = c
	}
	cons := model.NoConstraint
	if body.Pin != "" {
		cons = model.Constraint{Kind: model.ConstraintPin, Server: body.Pin}
	} else if body.Prefer != "" {
		cons = model.Constraint{Kind: model.ConstraintPrefer, Server: body.Prefer}
	}
	e := model.QueueEntry{Class: class, Constraint: cons, AfterSID: body.After}
	if err := s.sched.Enqueue(r.PathValue("t"), e, body.Front); err != nil {
		httpError(w, http.StatusBadRequest, "ENQUEUE", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDequeue — POST /api/v1/sessions/{t}/dequeue.
func (s *Server) handleDequeue(w http.ResponseWriter, r *http.Request) {
	if !s.schedOr503(w) {
		return
	}
	if err := s.sched.Dequeue(r.PathValue("t")); err != nil {
		httpError(w, http.StatusBadRequest, "DEQUEUE", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleRequeue — POST /api/v1/sessions/{t}/requeue {back}.
func (s *Server) handleRequeue(w http.ResponseWriter, r *http.Request) {
	if !s.schedOr503(w) {
		return
	}
	var body struct {
		Back bool `json:"back"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if err := s.sched.Requeue(r.PathValue("t"), body.Back); err != nil {
		httpError(w, http.StatusBadRequest, "REQUEUE", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handlePrio — POST /api/v1/sessions/{t}/prio {class}.
func (s *Server) handlePrio(w http.ResponseWriter, r *http.Request) {
	if !s.schedOr503(w) {
		return
	}
	var body struct {
		Class string `json:"class"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	c, ok := model.QueueClassParse(body.Class)
	if !ok {
		httpError(w, http.StatusBadRequest, "BAD_PRIO", "неизвестный класс: "+body.Class)
		return
	}
	if err := s.sched.Prio(r.PathValue("t"), c); err != nil {
		httpError(w, http.StatusBadRequest, "PRIO", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleConstraint — POST /api/v1/sessions/{t}/(pin|prefer|unpin) {server}.
func (s *Server) handleConstraint(w http.ResponseWriter, r *http.Request, kind model.ConstraintKind) {
	if !s.schedOr503(w) {
		return
	}
	t := r.PathValue("t")
	var body struct {
		Server string `json:"server"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	var err error
	switch kind {
	case model.ConstraintPin:
		err = s.sched.Pin(t, body.Server)
	case model.ConstraintPrefer:
		err = s.sched.Prefer(t, body.Server)
	default:
		err = s.sched.Unpin(t)
	}
	if err != nil {
		httpError(w, http.StatusBadRequest, "CONSTRAINT", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleHold — POST /api/v1/sessions/{t}/hold.
func (s *Server) handleHold(w http.ResponseWriter, r *http.Request) {
	if !s.schedOr503(w) {
		return
	}
	if err := s.sched.Hold(r.PathValue("t")); err != nil {
		httpError(w, http.StatusBadRequest, "HOLD", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleUnhold — POST /api/v1/sessions/{t}/unhold.
func (s *Server) handleUnhold(w http.ResponseWriter, r *http.Request) {
	if !s.schedOr503(w) {
		return
	}
	if err := s.sched.Unhold(r.PathValue("t")); err != nil {
		httpError(w, http.StatusBadRequest, "UNHOLD", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleAutoEnqueue — POST /api/v1/sessions/{t}/auto-enqueue {on}.
func (s *Server) handleAutoEnqueue(w http.ResponseWriter, r *http.Request) {
	if !s.schedOr503(w) {
		return
	}
	var body struct {
		On bool `json:"on"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if err := s.sched.SetAutoEnqueue(r.PathValue("t"), body.On); err != nil {
		httpError(w, http.StatusBadRequest, "AUTO_ENQUEUE", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleCancel — POST /api/v1/sessions/{t}/cancel (прервать ход).
func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	if !s.schedOr503(w) {
		return
	}
	if err := s.sched.Cancel(r.PathValue("t")); err != nil {
		httpError(w, http.StatusBadGateway, "CANCEL", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleResume — POST /api/v1/resume (снять паузу).
func (s *Server) handleResume(w http.ResponseWriter, r *http.Request) {
	if !s.schedOr503(w) {
		return
	}
	s.sched.Pause(false)
	w.WriteHeader(http.StatusNoContent)
}

// handleNodeDrain — POST /api/v1/nodes/{h}/drain {on}.
func (s *Server) handleNodeDrain(w http.ResponseWriter, r *http.Request) {
	if !s.schedOr503(w) {
		return
	}
	var body struct {
		On bool `json:"on"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	s.sched.NodeDrain(r.PathValue("h"), body.On)
	w.WriteHeader(http.StatusNoContent)
}

// handleServerDrain — POST /api/v1/servers/{s}/drain {on}.
func (s *Server) handleServerDrain(w http.ResponseWriter, r *http.Request) {
	if !s.schedOr503(w) {
		return
	}
	if s.serverDrain == nil {
		httpError(w, http.StatusServiceUnavailable, "NO_DRAIN", "drain не настроен")
		return
	}
	var body struct {
		On bool `json:"on"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if err := s.serverDrain(r.PathValue("s"), body.On); err != nil {
		httpError(w, http.StatusBadRequest, "DRAIN", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleServerPause — POST /api/v1/servers/{s}/pause {on}. on=true → PAUSED
// (новые ходы не выдаются, идущие доделываются), on=false → UP.
func (s *Server) handleServerPause(w http.ResponseWriter, r *http.Request) {
	if !s.schedOr503(w) {
		return
	}
	if s.serverPause == nil {
		httpError(w, http.StatusServiceUnavailable, "NO_PAUSE", "пауза не настроена")
		return
	}
	var body struct {
		On bool `json:"on"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if err := s.serverPause(r.PathValue("s"), body.On); err != nil {
		httpError(w, http.StatusBadRequest, "PAUSE", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
