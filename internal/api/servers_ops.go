package api

// Серверы (разделы 5.1/5.2, 3.4 ТЗ v2): мастер (создание + пробный запрос),
// удаление в обоих режимах (CONTROL 7), управление (drain/undrain/test-request).

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"time"

	"runpilot/internal/config"
	"runpilot/internal/model"
	"runpilot/internal/store"
)

// serverReg — реестр серверов в работе (шлюз) для CRUD.
type serverReg interface {
	AddOrUpdate(c config.Server) error
	Remove(name string)
}

// SetServerReg — реестр серверов (шлюз); ставится из serve.go.
func (s *Server) SetServerReg(r serverReg) { s.serverReg = r }

var serverNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

func validServerName(n string) bool { return serverNameRe.MatchString(n) }

// handleServerCreate — POST /api/v1/servers: создать сервер (мастер, 5.1).
func (s *Server) handleServerCreate(w http.ResponseWriter, r *http.Request) {
	var c config.Server
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		httpError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	if !validServerName(c.Name) {
		httpError(w, http.StatusBadRequest, "BAD_NAME", "имя [a-z0-9-]{1,32}, начинается с буквы/цифры")
		return
	}
	if _, err := s.store.GetServer(c.Name); err == nil {
		httpError(w, http.StatusConflict, "NAME_IN_USE", "сервер уже существует")
		return
	}
	if c.Slots <= 0 {
		c.Slots = 1
	}
	if err := s.store.UpsertServer(c, s.clk.Now()); err != nil {
		httpError(w, http.StatusInternalServerError, "STORE", err.Error())
		return
	}
	s.applyServerConfig(c)
	s.log.Info("api: сервер создан", "server", c.Name, "slots", c.Slots)
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"name": c.Name})
}

// handleServerGet — GET /api/v1/servers/{name}: конфигурация сервера.
func (s *Server) handleServerGet(w http.ResponseWriter, r *http.Request) {
	c, err := s.store.GetServer(r.PathValue("name"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			httpError(w, http.StatusNotFound, "NOT_FOUND", "нет сервера")
			return
		}
		httpError(w, http.StatusInternalServerError, "STORE", err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(c)
}

// patchUpstms — апстрим в PATCH (только переданные поля меняются).
type patchUpstms struct {
	OpenAI *patchOpenAI `json:"openai"`
}

type patchOpenAI struct {
	URL    *string `json:"url"`
	Model  *string `json:"model"`
	KeyEnv *string `json:"key_env"`
}

// handleServerPatch — PATCH /api/v1/servers/{name}: частичное обновление.
func (s *Server) handleServerPatch(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	cur, err := s.store.GetServer(name)
	if err != nil {
		httpError(w, http.StatusNotFound, "NOT_FOUND", "нет сервера")
		return
	}
	var patch struct {
		Priority        *int         `json:"priority"`
		Slots           *int         `json:"slots"`
		HealthURL       *string      `json:"health_url"`
		MetricsURL      *string      `json:"metrics_url"`
		MaxOutputTokens *int         `json:"max_output_tokens"`
		RequireDirect   *bool        `json:"require_direct"`
		Accept          []string     `json:"accept"`
		Upstreams       *patchUpstms `json:"upstreams"`
	}
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		httpError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	if patch.Priority != nil {
		cur.Priority = *patch.Priority
	}
	if patch.Slots != nil && *patch.Slots > 0 {
		cur.Slots = *patch.Slots
	}
	if patch.HealthURL != nil {
		cur.HealthURL = *patch.HealthURL
	}
	if patch.MetricsURL != nil {
		cur.MetricsURL = *patch.MetricsURL
	}
	if patch.MaxOutputTokens != nil {
		cur.MaxOutputTokens = *patch.MaxOutputTokens
	}
	if patch.RequireDirect != nil {
		cur.RequireDirect = *patch.RequireDirect
	}
	if patch.Accept != nil {
		cur.Accept = patch.Accept
	}
	if u := patch.Upstreams; u != nil && u.OpenAI != nil {
		if u.OpenAI.URL != nil {
			cur.Upstreams.OpenAI.URL = *u.OpenAI.URL
		}
		if u.OpenAI.Model != nil {
			cur.Upstreams.OpenAI.Model = *u.OpenAI.Model
		}
		if u.OpenAI.KeyEnv != nil {
			cur.Upstreams.OpenAI.KeyEnv = *u.OpenAI.KeyEnv
		}
	}
	if err := s.store.UpsertServer(cur, s.clk.Now()); err != nil {
		httpError(w, http.StatusInternalServerError, "STORE", err.Error())
		return
	}
	s.applyServerConfig(cur)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cur)
}

// handleServerDelete — DELETE /api/v1/servers/{name}?mode=now|after_turns
// (CONTROL 7, 5.2). now: без активных ходов — сразу; с активными — «удаляется».
func (s *Server) handleServerDelete(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	mode := r.URL.Query().Get("mode")
	if mode == "" {
		mode = "now"
	}
	if _, err := s.store.GetServer(name); err != nil {
		httpError(w, http.StatusNotFound, "NOT_FOUND", "нет сервера")
		return
	}
	switch mode {
	case "now":
		if s.serverUsed(name) == 0 {
			s.FinalizeServerRemoval(name)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"deleted": name})
			return
		}
		s.markServerRemoving(name)
		w.WriteHeader(http.StatusAccepted)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"removing": name})
	case "after_turns":
		s.markServerRemoving(name)
		w.WriteHeader(http.StatusAccepted)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"removing": name})
	default:
		httpError(w, http.StatusBadRequest, "BAD_MODE", "mode=now|after_turns")
	}
}

// FinalizeServerRemoval — завершить удаление (5.2): БД + реестр + cfg.
// Вызывается при mode=now без ходов и из планировщика (OnServerIdle).
func (s *Server) FinalizeServerRemoval(name string) {
	if err := s.store.DeleteServer(name); err != nil {
		s.log.Warn("api: удаление сервера: "+err.Error(), "server", name)
		return
	}
	if s.serverReg != nil {
		s.serverReg.Remove(name)
	}
	s.removeCfgServer(name)
	s.log.Info("api: сервер удалён", "server", name)
}

// markServerRemoving — дренаж (новые ходы не выдаются) + пометка «удаляется».
func (s *Server) markServerRemoving(name string) {
	if s.serverDrain != nil {
		_ = s.serverDrain(name, true)
	}
	if s.sched != nil {
		s.sched.MarkServerRemoving(name)
	}
}

// applyServerConfig — применить конфиг сервера в работе (шлюз + cfg).
func (s *Server) applyServerConfig(c config.Server) {
	if s.serverReg != nil {
		_ = s.serverReg.AddOrUpdate(c)
	}
	s.setCfgServer(c)
}

// serverUsed — активные ходы сервера (из вида планировщика).
func (s *Server) serverUsed(name string) int {
	if s.sched == nil {
		return 0
	}
	for _, row := range s.sched.ServersView() {
		if row.Name == name {
			return row.Used
		}
	}
	return 0
}

// handleServerOp — POST /api/v1/servers/{name}/{op}.
func (s *Server) handleServerOp(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	op := r.PathValue("op")
	if _, err := s.store.GetServer(name); err != nil {
		httpError(w, http.StatusNotFound, "NOT_FOUND", "нет сервера")
		return
	}
	switch op {
	case "drain":
		s.serverOpDrain(w, name, true)
	case "undrain":
		s.serverOpDrain(w, name, false)
	case "test-request":
		s.serverTestRequest(w, name)
	case "unquarantine":
		s.serverOpUnquarantine(w, name)
	case "cancel-turns":
		s.serverOpCancelTurns(w, name)
	case "use-model":
		s.serverOpUseModel(w, r, name)
	case "disable":
		s.serverOpDisable(w, name, true)
	case "enable":
		s.serverOpDisable(w, name, false)
	default:
		httpError(w, http.StatusNotFound, "NOT_FOUND", "нет операции: "+op)
	}
}

// serverOpUnquarantine — снять карантин (раздел 7.1 С4/С5): сервер
// возвращается в состояние здоровья, окно отказов сбрасывается.
func (s *Server) serverOpUnquarantine(w http.ResponseWriter, name string) {
	if s.mon == nil {
		httpError(w, http.StatusServiceUnavailable, "NO_MONITOR", "монитор недоступен")
		return
	}
	s.mon.Unquarantine(name)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"server": name, "state": string(model.ServerUp)})
}

// serverOpCancelTurns — снять ходы с сервера (раздел 7.1 С4): аренды
// RUNNING-сессий (inflight = 0) снимаются (SERVER_DOWN), сессии → очередь.
func (s *Server) serverOpCancelTurns(w http.ResponseWriter, name string) {
	if s.sched == nil {
		httpError(w, http.StatusServiceUnavailable, "NO_SCHED", "планировщик недоступен")
		return
	}
	n := s.sched.CancelServerTurns(name)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"server": name, "released": n})
}

// serverOpUseModel — установить модель сервера (раздел 7.1 С7/С8 «Использовать
// B»): обновляется upstreams.openai.model, сервер возвращается в выдачу.
func (s *Server) serverOpUseModel(w http.ResponseWriter, r *http.Request, name string) {
	var body struct {
		Model string `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Model == "" {
		httpError(w, http.StatusBadRequest, "BAD_MODEL", "нужно {\"model\": \"...\"}")
		return
	}
	c, err := s.store.GetServer(name)
	if err != nil {
		httpError(w, http.StatusNotFound, "NOT_FOUND", "нет сервера")
		return
	}
	c.Upstreams.OpenAI.Model = body.Model
	if err := s.store.UpsertServer(c, s.clk.Now()); err != nil {
		httpError(w, http.StatusInternalServerError, "STORE", err.Error())
		return
	}
	s.applyServerConfig(c)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"server": name, "model": body.Model})
}

// serverOpDisable — disable/enable сервера (раздел 7.1): disable — из выдачи
// (DRAINING), enable — в выдачу (UP).
func (s *Server) serverOpDisable(w http.ResponseWriter, name string, off bool) {
	if s.serverDrain != nil {
		if err := s.serverDrain(name, off); err != nil {
			httpError(w, http.StatusInternalServerError, "DRAIN", err.Error())
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	st := "в выдаче"
	if off {
		st = "отключён"
	}
	json.NewEncoder(w).Encode(map[string]string{"server": name, "state": st})
}

func (s *Server) serverOpDrain(w http.ResponseWriter, name string, on bool) {
	if s.serverDrain != nil {
		if err := s.serverDrain(name, on); err != nil {
			httpError(w, http.StatusInternalServerError, "DRAIN", err.Error())
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	st := "drain снят"
	if on {
		st = "drain"
	}
	json.NewEncoder(w).Encode(map[string]string{"server": name, "state": st})
}

// serverTestRequest — пробный запрос (5.1, без сохранения): /health + апстрим.
func (s *Server) serverTestRequest(w http.ResponseWriter, name string) {
	c, _ := s.store.GetServer(name)
	res := map[string]any{"server": name}
	if c.HealthURL != "" {
		res["health_ok"] = httpGetOK(c.HealthURL, s.probeTimeout())
	}
	if u := c.Upstreams.OpenAI.URL; u != "" {
		res["upstream_ok"] = httpGetOK(u+"/models", s.probeTimeout())
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

// probeTimeout — таймаут пробного запроса сервера (5.1).
func (s *Server) probeTimeout() time.Duration {
	return time.Duration(s.cfg.Web.ProbeTimeoutSec) * time.Second
}

func httpGetOK(u string, d time.Duration) bool {
	if _, err := url.Parse(u); err != nil {
		return false
	}
	c := &http.Client{Timeout: d}
	resp, err := c.Get(u)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// handleServerProbe — POST /api/v1/servers/probe (5.1): проверка без сохранения.
func (s *Server) handleServerProbe(w http.ResponseWriter, r *http.Request) {
	var c config.Server
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		httpError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	res := map[string]any{}
	if c.HealthURL != "" {
		res["health_ok"] = httpGetOK(c.HealthURL, s.probeTimeout())
	} else {
		// Пустой URL — сервер не объявлен, а не «отвечает».
		res["health_ok"] = false
	}
	if u := c.Upstreams.OpenAI.URL; u != "" {
		res["upstream_ok"] = httpGetOK(u+"/models", s.probeTimeout())
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

// --- cfg.Servers (in-memory) ---

func (s *Server) setCfgServer(c config.Server) {
	for i := range s.cfg.Servers {
		if s.cfg.Servers[i].Name == c.Name {
			s.cfg.Servers[i] = c
			return
		}
	}
	s.cfg.Servers = append(s.cfg.Servers, c)
}

func (s *Server) removeCfgServer(name string) {
	out := s.cfg.Servers[:0]
	for _, sv := range s.cfg.Servers {
		if sv.Name != name {
			out = append(out, sv)
		}
	}
	s.cfg.Servers = out
}
