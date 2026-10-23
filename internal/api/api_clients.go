package api

// API-клиенты шлюза (раздел «Учёт и интерфейс»): список + учёт за сутки,
// создание/отключение/перевыпуск ключа. Ключ показывается один раз (при
// создании и перевыпуске) и нигде не хранится — только key_hash в БД.

import (
	"encoding/json"
	"net/http"

	"runpilot/internal/model"
	"runpilot/internal/store"
)

// ApiClientView — клиент + учёт за сутки (для веба и CLI).
type ApiClientView struct {
	Name           string `json:"name"`
	Class          string `json:"class"`
	Priority       int    `json:"priority"`
	Enabled        bool   `json:"enabled"`
	AllowedServers []string `json:"allowed_servers,omitempty"`
	MaxHoldSec     int    `json:"max_hold_sec"`
	StickyIdleSec  int    `json:"sticky_idle_sec"`
	AgingSec       int    `json:"aging_sec"`
	CreatedAt      string `json:"created_at"`
	// Учёт за последние сутки.
	Requests         int `json:"requests_24h"`
	PromptTokens     int `json:"prompt_tokens_24h"`
	CompletionTokens int `json:"completion_tokens_24h"`
}

// handleApiClients — GET /api/v1/api-clients: список + учёт за сутки.
func (s *Server) handleApiClients(w http.ResponseWriter, r *http.Request) {
	clients, err := s.store.ListApiClients()
	if err != nil {
		httpError(w, http.StatusInternalServerError, "STORE", err.Error())
		return
	}
	daily, _ := s.store.ApiRequestsDaily(s.clk.Now())
	out := make([]ApiClientView, 0, len(clients))
	for _, c := range clients {
		v := ApiClientView{
			Name:           c.Name,
			Class:          c.Class.String(),
			Priority:       c.Priority,
			Enabled:        c.Enabled,
			AllowedServers: c.AllowedServers,
			MaxHoldSec:     c.MaxHoldSec,
			StickyIdleSec:  c.StickyIdleSec,
			AgingSec:       c.AgingSec,
			CreatedAt:      c.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		}
		if d, ok := daily[c.Name]; ok {
			v.Requests = d.Requests
			v.PromptTokens = d.PromptTokens
			v.CompletionTokens = d.CompletionTokens
		}
		out = append(out, v)
	}
	writeJSON(w, out)
}

// handleApiClientCreate — POST /api/v1/api-clients {name, ...} → ключ один раз.
func (s *Server) handleApiClientCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name           string   `json:"name"`
		Priority       int      `json:"priority"`
		AllowedServers []string `json:"allowed_servers"`
		MaxHoldSec     int      `json:"max_hold_sec"`
		StickyIdleSec  int      `json:"sticky_idle_sec"`
		AgingSec       int      `json:"aging_sec"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		httpError(w, http.StatusBadRequest, "BAD_REQUEST", "name обязателен")
		return
	}
	c := store.ApiClient{
		Name:           body.Name,
		Enabled:        true,
		AllowedServers: body.AllowedServers,
		Priority:       body.Priority,
		MaxHoldSec:     body.MaxHoldSec,
		StickyIdleSec:  body.StickyIdleSec,
		AgingSec:       body.AgingSec,
	}
	if c.MaxHoldSec == 0 {
		c.MaxHoldSec = apiClientDefaultMaxHoldSec
	}
	if c.StickyIdleSec == 0 {
		c.StickyIdleSec = apiClientDefaultStickyIdleSec
	}
	if c.AgingSec == 0 {
		c.AgingSec = apiClientDefaultAgingSec
	}
	key, err := s.store.CreateApiClient(c)
	if err != nil {
		httpError(w, http.StatusConflict, "EXISTS", err.Error())
		return
	}
	writeJSON(w, map[string]string{"name": c.Name, "key": key})
}

// handleApiClientSetEnabled — POST /api/v1/api-clients/{name}/enable|disable.
func (s *Server) handleApiClientSetEnabled(w http.ResponseWriter, r *http.Request, enabled bool) {
	name := r.PathValue("name")
	if err := s.store.SetApiClientEnabled(name, enabled); err != nil {
		httpError(w, http.StatusNotFound, "NOT_FOUND", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleApiClientReissue — POST /api/v1/api-clients/{name}/reissue → ключ один раз.
func (s *Server) handleApiClientReissue(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	key, err := s.store.ReissueAPIKey(name)
	if err != nil {
		httpError(w, http.StatusNotFound, "NOT_FOUND", err.Error())
		return
	}
	writeJSON(w, map[string]string{"name": name, "key": key})
}

// apiClientView — клиент + учёт за сутки → ApiClientView.
func apiClientView(c store.ApiClient, daily map[string]store.ApiRequestDaily) ApiClientView {
	v := ApiClientView{
		Name:           c.Name,
		Class:          c.Class.String(),
		Priority:       c.Priority,
		Enabled:        c.Enabled,
		AllowedServers: c.AllowedServers,
		MaxHoldSec:     c.MaxHoldSec,
		StickyIdleSec:  c.StickyIdleSec,
		AgingSec:       c.AgingSec,
		CreatedAt:      c.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
	if d, ok := daily[c.Name]; ok {
		v.Requests = d.Requests
		v.PromptTokens = d.PromptTokens
		v.CompletionTokens = d.CompletionTokens
	}
	return v
}

// handleApiClientGet — GET /api/v1/api-clients/{name}: один клиент + учёт.
func (s *Server) handleApiClientGet(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	c, err := s.store.GetApiClient(name)
	if err != nil {
		httpError(w, http.StatusNotFound, "NOT_FOUND", err.Error())
		return
	}
	daily, _ := s.store.ApiRequestsDaily(s.clk.Now())
	writeJSON(w, apiClientView(c, daily))
}

// handleApiClientUpdate — PATCH /api/v1/api-clients/{name}: обновить настройки
// (class, priority, allowed_servers, таймауты). Ключ и enabled не меняются.
func (s *Server) handleApiClientUpdate(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var body struct {
		Class          string   `json:"class"`
		Priority       int      `json:"priority"`
		AllowedServers []string `json:"allowed_servers"`
		MaxHoldSec     int      `json:"max_hold_sec"`
		StickyIdleSec  int      `json:"sticky_idle_sec"`
		AgingSec       int      `json:"aging_sec"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	c, err := s.store.GetApiClient(name)
	if err != nil {
		httpError(w, http.StatusNotFound, "NOT_FOUND", err.Error())
		return
	}
	if body.Class != "" {
		cls, ok := model.QueueClassParse(body.Class)
		if !ok {
			httpError(w, http.StatusBadRequest, "BAD_CLASS", "неизвестный класс: "+body.Class)
			return
		}
		c.Class = cls
	}
	c.Priority = body.Priority
	c.AllowedServers = body.AllowedServers
	c.MaxHoldSec = body.MaxHoldSec
	c.StickyIdleSec = body.StickyIdleSec
	c.AgingSec = body.AgingSec
	updated, err := s.store.SetApiClient(c)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "STORE", err.Error())
		return
	}
	daily, _ := s.store.ApiRequestsDaily(s.clk.Now())
	writeJSON(w, apiClientView(updated, daily))
}

// handleApiClientDelete — DELETE /api/v1/api-clients/{name}: удалить клиента
// и его псевдо-сессию. Если псевдо-сессия держит аренду — снять её.
func (s *Server) handleApiClientDelete(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	sid := store.APISessionSID(name)
	// Активная псевдо-сессия — снять аренду (иначе сиротский слот).
	if rec, err := s.store.GetSession(sid); err == nil &&
		(rec.State == model.SessionRunning || rec.State == model.SessionDispatching) {
		s.releaseOnGone(sid, "client_deleted")
	}
	if err := s.store.DeleteApiClient(name); err != nil {
		httpError(w, http.StatusNotFound, "NOT_FOUND", err.Error())
		return
	}
	s.log.Info("api: api-клиент удалён", "name", name)
	w.WriteHeader(http.StatusNoContent)
}
