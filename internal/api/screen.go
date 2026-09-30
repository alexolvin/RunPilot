// Живой экран (раздел 15.4 ТЗ, v2). Координатор по подписке ?screens=<sid>
// раз в web.screen_interval_ms просит узел снять панель (KindScreen, ANSI) и
// шлёт событие SCREEN в SSE-поток; GET /sessions/{t}/screen — разовый снимок
// для первичной отрисовки карточки. Кадры не пишутся в БД (живой поток, R5:
// содержимое экрана не хранится).
package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"runpilot/internal/proto"
)

// screenFrame — кадр экрана (ответ /screen и payload события SCREEN).
type screenFrame struct {
	TextANSI   string `json:"text_ansi"`
	Cols       int    `json:"cols"`
	Rows       int    `json:"rows"`
	ReceivedAt string `json:"received_at"`
}

// resolveSID — целевое обозначение (sid или имя) → sid ("" если не найдено).
func (s *Server) resolveSID(t string) string {
	if t == "" {
		return ""
	}
	if r, err := s.store.GetSession(t); err == nil {
		return r.SID
	}
	if r, err := s.store.FindLiveByName(t); err == nil {
		return r.SID
	}
	return ""
}

// captureScreen — снимок панели (ANSI) по sid; false — если панели/узла нет.
func (s *Server) captureScreen(ctx context.Context, sid string) (screenFrame, bool) {
	info, ok := s.hub.PaneBySID(sid)
	if !ok {
		return screenFrame{}, false
	}
	m := proto.New(proto.KindScreen)
	m.PaneID, m.SID = info.Pane.PaneID, sid
	cctx, cancel := context.WithTimeout(ctx, time.Duration(s.cfg.Web.PasteVerifySec)*time.Second)
	defer cancel()
	reply, err := s.hub.Dispatch(cctx, m.PaneID, m)
	if err != nil || reply.Result != proto.ResSent {
		return screenFrame{}, false
	}
	return screenFrame{
		TextANSI:   reply.Detail,
		Cols:       reply.Cols,
		Rows:       reply.Rows,
		ReceivedAt: s.clk.Now().UTC().Format(time.RFC3339Nano),
	}, true
}

// handleScreen — GET /api/v1/sessions/{t}/screen (разовый снимок, 11.3).
// 404 — только если сессии нет; если сессия есть, но панель снять нельзя
// (узла нет/офлайн) — пустой кадр (экран сейчас пуст), не ошибка.
func (s *Server) handleScreen(w http.ResponseWriter, r *http.Request) {
	sid := s.resolveSID(r.PathValue("t"))
	if sid == "" {
		httpError(w, http.StatusNotFound, "NOT_FOUND", "нет такой сессии")
		return
	}
	f, ok := s.captureScreen(r.Context(), sid)
	if !ok {
		f = screenFrame{ReceivedAt: s.clk.Now().UTC().Format(time.RFC3339Nano)}
	}
	writeJSON(w, f)
}

// screenTicker — тикер живого экрана (nil, если интервал не задан).
func (s *Server) screenTicker() *time.Ticker {
	ms := s.cfg.Web.ScreenIntervalMS
	if ms <= 0 {
		return nil
	}
	return time.NewTicker(time.Duration(ms) * time.Millisecond)
}

// parseScreens — строка ?screens=<t>,<t> → sids (разрешение, дедупликация,
// предел web.screens_per_client_max).
func (s *Server) parseScreens(q string) []string {
	if q == "" {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	max := s.cfg.Web.ScreensPerClientMax
	for _, p := range strings.Split(q, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		sid := s.resolveSID(p)
		if sid == "" || seen[sid] {
			continue
		}
		seen[sid] = true
		out = append(out, sid)
		if max > 0 && len(out) >= max {
			break
		}
	}
	return out
}
