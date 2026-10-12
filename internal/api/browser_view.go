// Живой просмотр вкладки браузера (ТЗ Relay §8): WS-мост screencast +
// управление. Оператор видит вкладку в вебе и управляет мышью/вводом —
// единственный путь для входа на сайт оркестратора (captcha/2FA — только
// оператор, ТЗ Relay §18 R6).
//
// Протокол:
//   сервер→клиент: двоичные кадры (JPEG screencast) + JSON
//   {type:"open"|"error"|"closed"};
//   клиент→сервер: JSON {type:"click",x,y} | {type:"text",text} |
//   {type:"key",key} | {type:"close"}.
//
// Аудит (ТЗ Relay §8.4.7): открытие, закрытие, число событий ввода.
// Содержимое ввода и кадров не пишется.
package api

import (
	"encoding/json"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"runpilot/internal/browser"
	"runpilot/internal/model"

	"github.com/gorilla/websocket"
)

// BrowserViewWS — живой просмотр вкладки (ТЗ Relay §8):
// GET /web/ws/browser/view?tab= (WS-апгрейд, cookieAuth + Origin на вебе).
// Кадры screencast → браузер (двоичные), управление → странице (JSON).
func (s *Server) BrowserViewWS(w http.ResponseWriter, r *http.Request, actor string) {
	if s.browser == nil || !s.browser.Running() {
		httpError(w, http.StatusServiceUnavailable, "BROWSER_OFF", "браузер не запущен")
		return
	}
	tabID := r.URL.Query().Get("tab")
	page := s.browser.TabPage(tabID)
	if page == nil {
		httpError(w, http.StatusNotFound, "TAB_NOT_FOUND", "нет такой вкладки")
		return
	}
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	view := browser.NewView(page, s.cfg.Browser.ViewJPEGQuality, s.cfg.Browser.ViewMaxWidth)
	var wmu sync.Mutex // один писатель на conn (кадры + JSON-сигналы)
	var inputs atomic.Int64 // число событий ввода (аудит, §8.4.7)
	openedAt := s.clk.Now()
	defer s.auditBrowserView(tabID, actor, openedAt, inputs.Load())

	if err := view.StartScreencast(func(data []byte) {
		wmu.Lock()
		_ = conn.WriteMessage(websocket.BinaryMessage, data)
		wmu.Unlock()
	}); err != nil {
		viewBrowserError(conn, "SCREENCAST_FAILED")
		return
	}
	defer view.StopScreencast()

	wmu.Lock()
	_ = conn.WriteJSON(map[string]any{"type": "open", "tab": tabID})
	wmu.Unlock()

	// Управление: клик / ввод / закрытие.
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var msg struct {
			Type string  `json:"type"`
			X    float64 `json:"x"`
			Y    float64 `json:"y"`
			Text string  `json:"text"`
			Key  string  `json:"key"`
		}
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		switch msg.Type {
		case "click":
			inputs.Add(1)
			_ = view.Click(msg.X, msg.Y)
		case "text":
			inputs.Add(1)
			_ = view.InsertText(msg.Text)
		case "key":
			inputs.Add(1)
		case "close":
			wmu.Lock()
			_ = conn.WriteJSON(map[string]any{"type": "closed"})
			wmu.Unlock()
			return
		}
	}
}

// auditBrowserView — VIEW_OPEN/CLOSE (ТЗ Relay §8.4.7): вкладка, оператор,
// длительность, число событий ввода. Содержимое не пишется.
func (s *Server) auditBrowserView(tabID, actor string, openedAt time.Time, inputs int64) {
	payload, _ := json.Marshal(map[string]any{
		"tab":         tabID,
		"operator":    actor,
		"duration_ms": s.clk.Now().Sub(openedAt).Milliseconds(),
		"inputs":      inputs,
	})
	_ = s.store.EventRecord(model.Event{
		TS: s.clk.Now(), Kind: "browser_view_close", Payload: payload,
	})
}

// viewBrowserError — отказ после апгрейда: код ошибки, затем закрытие.
func viewBrowserError(conn *websocket.Conn, code string) {
	_ = conn.WriteJSON(map[string]any{"type": "error", "code": code})
}
