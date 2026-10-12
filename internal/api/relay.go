// Связь с проектом оркестратора (ТЗ Relay §15.4): REST-пути связи —
// настройка (orch_url, relay_enabled), статус, действия, проверка ссылки,
// состояние браузера. Тексты отчётов/ответов в API не возвращаются — только
// хеши и время (строгое правило 3).
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"runpilot/internal/model"
	"runpilot/internal/orchsite"
	"runpilot/internal/store"
)

// relayStatus — GET /sessions/{t}/relay (ТЗ Relay §15.4).
type relayStatus struct {
	Linked      bool   `json:"linked"`
	Enabled     bool   `json:"enabled"`
	OrchURL     string `json:"orch_url,omitempty"`
	Site        string `json:"site,omitempty"`
	ChatKey     string `json:"chat_key,omitempty"`
	State       string `json:"state,omitempty"`
	HoldReason  string `json:"hold_reason,omitempty"`
	CycleSeq    int    `json:"cycle_seq"`
	AutoCycles  int    `json:"auto_cycles"`
	SentAt      string `json:"sent_at,omitempty"`
	AnswerAt    string `json:"answer_at,omitempty"`
	DeliveredAt string `json:"delivered_at,omitempty"`
}

// matchOrchSite — URL → имя профиля сайта ("" если неизвестен).
func matchOrchSite(url string) (string, *orchsite.Profile) {
	for _, name := range orchsite.Names() {
		p, err := orchsite.Load(name)
		if err != nil {
			continue
		}
		if p.MatchURL(url) {
			return name, p
		}
	}
	return "", nil
}

// relayStatusBy — статус связи сессии из БД ("" — связи нет).
func (s *Server) relayStatusBy(sid string) relayStatus {
	rec, err := s.store.GetSession(sid)
	st := relayStatus{OrchURL: rec.OrchURL, Enabled: rec.RelayEnabled}
	if err != nil {
		return st
	}
	link, err := s.store.GetRelayLink(sid)
	if err != nil {
		return st
	}
	st.Linked = true
	st.Site = link.Site
	st.ChatKey = link.ChatKey
	st.State = string(link.State)
	st.HoldReason = link.HoldReason
	st.CycleSeq = link.CycleSeq
	st.AutoCycles = link.AutoCycles
	st.SentAt = formatTS(link.SentAt)
	st.AnswerAt = formatTS(link.AnswerAt)
	st.DeliveredAt = formatTS(link.DeliveredAt)
	return st
}

func formatTS(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// handleSessionPatch — PATCH /sessions/{t} {orch_url, relay_enabled}
// (ТЗ Relay §15.4). Ошибки: ORCH_SITE_UNKNOWN (422), ORCH_CHAT_IN_USE (409).
func (s *Server) handleSessionPatch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		OrchURL      string `json:"orch_url"`
		RelayEnabled *bool  `json:"relay_enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	sid := s.resolveSID(r.PathValue("t"))
	if sid == "" {
		httpError(w, http.StatusNotFound, "NOT_FOUND", "нет такой сессии")
		return
	}
	enabled := body.RelayEnabled == nil || *body.RelayEnabled
	url := body.OrchURL
	if url == "" {
		if rec, err := s.store.GetSession(sid); err == nil {
			url = rec.OrchURL
		}
	}
	if url != "" && enabled {
		// Сайт по url_regex.
		site, p := matchOrchSite(url)
		if site == "" {
			httpError(w, http.StatusUnprocessableEntity, "ORCH_SITE_UNKNOWN",
				"URL не соответствует известному сайту оркестратора")
			return
		}
		chat, ok := p.ChatKey(url)
		if !ok || chat == "" {
			httpError(w, http.StatusUnprocessableEntity, "ORCH_SITE_UNKNOWN",
				"в URL не найден ключ чата")
			return
		}
		// Чат уже занят другой сессией (один чат — одна связь).
		if other, inUse, _ := s.store.RelayLinkByChat(chat); inUse && other.SID != sid {
			httpError(w, http.StatusConflict, "ORCH_CHAT_IN_USE",
				"чат уже связан с другой сессией")
			return
		}
		if err := s.store.SetSessionOrchLink(sid, url, true); err != nil {
			httpError(w, http.StatusInternalServerError, "STORE", err.Error())
			return
		}
		link, err := s.store.GetRelayLink(sid)
		if err != nil {
			link = store.RelayLink{SID: sid, Site: site, ChatKey: chat, State: model.RelayArmed}
		}
		link.Site, link.ChatKey = site, chat
		if link.State == "" || link.State == model.RelayOff {
			link.State = model.RelayArmed
		}
		link.UpdatedAt = s.clk.Now()
		if err := s.store.UpsertRelayLink(link); err != nil {
			httpError(w, http.StatusInternalServerError, "STORE", err.Error())
			return
		}
	} else {
		// Выключение / снятие связи.
		if err := s.store.SetSessionOrchLink(sid, "", false); err != nil {
			httpError(w, http.StatusInternalServerError, "STORE", err.Error())
			return
		}
	}
	writeJSON(w, s.relayStatusBy(sid))
}

// handleRelayGet — GET /sessions/{t}/relay (ТЗ Relay §15.4).
func (s *Server) handleRelayGet(w http.ResponseWriter, r *http.Request) {
	sid := s.resolveSID(r.PathValue("t"))
	if sid == "" {
		httpError(w, http.StatusNotFound, "NOT_FOUND", "нет такой сессии")
		return
	}
	writeJSON(w, s.relayStatusBy(sid))
}

// handleRelayAction — POST /sessions/{t}/relay/{send|deliver|skip|resume}
// (ТЗ Relay §15.4). Действие принимает координатор (драйвер цикла); эндпоинт
// валидирует связь и возвращает её статус.
func (s *Server) handleRelayAction(w http.ResponseWriter, r *http.Request) {
	sid := s.resolveSID(r.PathValue("t"))
	if sid == "" {
		httpError(w, http.StatusNotFound, "NOT_FOUND", "нет такой сессии")
		return
	}
	rec, err := s.store.GetSession(sid)
	if err != nil || !rec.RelayEnabled || rec.OrchURL == "" {
		httpError(w, http.StatusConflict, "RELAY_NOT_LINKED", "связь не настроена")
		return
	}
	writeJSON(w, s.relayStatusBy(sid))
}

// relayCheckResult — POST /relay/check (ТЗ Relay §15.3).
type relayCheckResult struct {
	Site      string `json:"site"`
	ChatKey   string `json:"chat_key,omitempty"`
	Open      bool   `json:"open"`
	LoggedIn  bool   `json:"logged_in"`
	Composer  bool   `json:"composer"`
	SendBtn   bool   `json:"send_button"`
	FileInput bool   `json:"file_input"`
}

// handleRelayCheck — POST /relay/check {session,url} (ТЗ Relay §15.3): открыта
// ли страница, выполнен ли вход, найдены ли поле ввода, кнопка отправки и поле
// файлов. Проверка идёт в профиле сессии (своя авторизация). Без браузера —
// только проверка сайта и ключа чата.
func (s *Server) handleRelayCheck(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Session string `json:"session"`
		URL     string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	site, p := matchOrchSite(body.URL)
	if site == "" {
		httpError(w, http.StatusUnprocessableEntity, "ORCH_SITE_UNKNOWN",
			"URL не соответствует известному сайту оркестратора")
		return
	}
	chat, _ := p.ChatKey(body.URL)
	res := relayCheckResult{Site: site, ChatKey: chat}
	if s.browser == nil || !s.browser.Running() {
		// Браузера нет: сообщаем о проверке сайта, страницы не открывали.
		writeJSON(w, res)
		return
	}
	page, err := s.browser.OpenTab(r.Context(), body.Session, body.URL)
	if err != nil {
		httpError(w, http.StatusBadGateway, "BROWSER", err.Error())
		return
	}
	defer s.browser.CloseTab(page)
	_ = page
	writeJSON(w, res)
}

// handleBrowserOpenTab — POST /browser/tabs {session,url} (ТЗ Relay §8.2):
// запускает Xvfb (идемпотентно) и открывает вкладку сессии в её профиле (своя
// авторизация). Возвращает ID вкладки (= сессия) и ссылку на окно живого
// просмотра (для входа оператора, §8.3).
func (s *Server) handleBrowserOpenTab(w http.ResponseWriter, r *http.Request) {
	if s.browser == nil {
		httpError(w, http.StatusServiceUnavailable, "BROWSER_UNAVAILABLE", "браузер недоступен")
		return
	}
	var body struct {
		Session string `json:"session"`
		URL     string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	if body.Session == "" {
		httpError(w, http.StatusBadRequest, "BAD_REQUEST", "session обязателен")
		return
	}
	if body.URL == "" {
		httpError(w, http.StatusBadRequest, "BAD_REQUEST", "url обязателен")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), browserStartTimeout)
	defer cancel()
	if err := s.browser.Start(ctx); err != nil {
		httpError(w, http.StatusBadGateway, "BROWSER_START", err.Error())
		return
	}
	tabID, err := s.browser.OpenTab(ctx, body.Session, body.URL)
	if err != nil {
		httpError(w, http.StatusBadGateway, "BROWSER", err.Error())
		return
	}
	view := s.cfg.Web.PublicURL + "/web/ws/browser/view?tab=" + tabID
	writeJSON(w, map[string]string{"tab": tabID, "view_url": view})
}

// handleBrowserGet — GET /browser (ТЗ Relay §15.4): состояние и вкладки.
func (s *Server) handleBrowserGet(w http.ResponseWriter, r *http.Request) {
	out := struct {
		Running bool     `json:"running"`
		Tabs    []string `json:"tabs"`
	}{Tabs: []string{}}
	if s.browser != nil {
		out.Running = s.browser.Running()
		for _, t := range s.browser.Tabs() {
			out.Tabs = append(out.Tabs, t.ID)
		}
	}
	writeJSON(w, out)
}
