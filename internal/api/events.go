package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"runpilot/internal/model"
)

// registerSSE/unregisterSSE — учёт активных SSE-подключений (DropSSE, стенд).
// registerSSE атомен с проверкой лимита (v2 K9): accepted=false — лимит
// web.sse_clients_max исчерпан (клиент отклонён, запись не добавлена).
func (s *Server) registerSSE(c context.CancelFunc) (id int64, accepted bool) {
	s.sseMu.Lock()
	defer s.sseMu.Unlock()
	if max := s.cfg.Web.SSEClientsMax; max > 0 && len(s.sseCancels) >= max {
		return 0, false
	}
	s.sseSeq++
	id = s.sseSeq
	s.sseCancels[id] = c
	return id, true
}

func (s *Server) unregisterSSE(id int64) {
	s.sseMu.Lock()
	delete(s.sseCancels, id)
	s.sseMu.Unlock()
}

// sseEvent — тело события SSE (приложение Б ТЗ). Клиент обязан показать
// неизвестный kind как сырой JSON и не падать (R4: события — единственный
// источник изменений, без опроса REST).
type sseEvent struct {
	Kind    string          `json:"kind"`
	SID     string          `json:"sid,omitempty"`
	Server  string          `json:"server,omitempty"`
	TS      string          `json:"ts"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// encodeSSE — JSON-строка события (используется в потоке SSE и в JSON-ответе).
func encodeSSE(e model.Event) []byte {
	data := sseEvent{
		Kind: e.Kind, SID: e.SID, Server: e.Server,
		TS: e.TS.UTC().Format(time.RFC3339Nano),
	}
	if len(e.Payload) > 0 {
		data.Payload = e.Payload
	}
	b, _ := json.Marshal(data)
	return b
}

// handleEvents — SSE-поток /api/v1/events (v2 раздел 15.4).
//
// Досылка и RESYNC: каждое событие несёт монотонный id; клиент переподключается
// с заголовком Last-Event-ID. Координатор держит последние web.sse_replay
// событий (лента в БД). Если Last-Event-ID >= maxID - sse_replay — досылает
// пропущенные (id > Last-Event-ID). Если клиент отстал дальше буфера — шлёт
// RESYNC (id = maxID): клиент заново берёт GET /api/v1/state и продолжает с
// maxID. Свежее соединение (без Last-Event-ID) — последние sseInitialEvents.
//
// События шлются БЕЗ поля «event:» (все — «message»-события): kind читается из
// JSON-тела. Это позволяет клиенту единообразно обрабатывать и неизвестные kind.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		s.handleEventsJSON(w, r, limitStr)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		httpError(w, http.StatusInternalServerError, "SSE", "нет flusher")
		return
	}
	// v2 (K9): лимит одновременных SSE-клиентов (web.sse_clients_max).
	// Регистрация атомарна с проверкой лимита (до W.WriteHeader, без гонок).
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	sseID, accepted := s.registerSSE(cancel)
	if !accepted {
		httpError(w, http.StatusServiceUnavailable, "SSE_LIMIT",
			"превышен лимит одновременных SSE-клиентов")
		return
	}
	defer s.unregisterSSE(sseID)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	replay := s.cfg.Web.SSEReplay
	if replay <= 0 {
		replay = sseInitialEvents
	}
	maxID, err := s.store.EventMaxID()
	if err != nil {
		maxID = 0
	}

	after := int64(0)
	resync := false
	if lastIDStr := r.Header.Get("Last-Event-ID"); lastIDStr != "" {
		// Переподключение: досылка из Last-Event-ID (стандарт SSE).
		if lastID, perr := strconv.ParseInt(lastIDStr, decimalBase, int64Bits); perr == nil && lastID > 0 {
			if lastID >= maxID-int64(replay) {
				after = lastID // в пределах буфера — досылаем пропущенные
			} else {
				resync = true // отстал дальше буфера — RESYNC
				after = maxID
			}
		}
	} else {
		// Свежее соединение: начальная лента (последние sseInitialEvents).
		after = maxID - sseInitialEvents
		if after < 0 {
			after = 0
		}
	}

	send := func(e model.Event) {
		fmt.Fprintf(w, "id: %d\ndata: %s\n\n", e.ID, encodeSSE(e))
		flusher.Flush()
	}
	sendResync := func(id int64) {
		fmt.Fprintf(w, "id: %d\ndata: %s\n\n", id, encodeSSE(model.Event{ID: id, Kind: model.KindResync, TS: s.clk.Now()}))
		flusher.Flush()
	}

	if resync {
		// RESYNC: клиент заново берёт GET /api/v1/state; дальше — только
		// события с id > maxID (after уже = maxID).
		sendResync(maxID)
	}

	poll := time.NewTicker(ssePollMS * time.Millisecond)
	defer poll.Stop()
	keepAlive := time.NewTicker(sseKeepAliveSec * time.Second)
	defer keepAlive.Stop()

	// Живые экраны (15.4): подписка ?screens=<t>,<t> — раз в
	// web.screen_interval_ms снимок панели (ANSI) → событие SCREEN.
	// screenCh nil — подписки нет (этот case не срабатывает).
	screenSIDs := s.parseScreens(r.URL.Query().Get("screens"))
	var screenCh <-chan time.Time
	if tick := s.screenTicker(); tick != nil {
		defer tick.Stop()
		screenCh = tick.C
	}
	screenNxt := maxID
	sendScreen := func(f screenFrame, sid string) {
		screenNxt++
		payload, _ := json.Marshal(f)
		ev := model.Event{ID: screenNxt, Kind: model.KindScreen, SID: sid, TS: s.clk.Now(), Payload: payload}
		fmt.Fprintf(w, "id: %d\ndata: %s\n\n", screenNxt, encodeSSE(ev))
		flusher.Flush()
	}

	for {
		if events, err := s.store.EventList(after, sseBatchSize); err == nil {
			for _, e := range events {
				send(e)
				after = e.ID
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-keepAlive.C:
			fmt.Fprint(w, ": keep-alive\n\n")
			flusher.Flush()
		case <-poll.C:
		case <-screenCh:
			for _, sid := range screenSIDs {
				if f, ok := s.captureScreen(ctx, sid); ok {
					sendScreen(f, sid)
				}
			}
		}
	}
}

// handleEventsJSON — разовый JSON-ответ: последние N событий (runpilot events).
func (s *Server) handleEventsJSON(w http.ResponseWriter, r *http.Request, limitStr string) {
	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit <= 0 {
		httpError(w, http.StatusBadRequest, "BAD_LIMIT", "limit должен быть > 0")
		return
	}
	maxID, err := s.store.EventMaxID()
	if err != nil {
		httpError(w, http.StatusInternalServerError, "STORE", err.Error())
		return
	}
	after := maxID - int64(limit)
	if after < 0 {
		after = 0
	}
	events, err := s.store.EventList(after, limit)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "STORE", err.Error())
		return
	}
	out := make([]sseEvent, 0, len(events))
	for _, e := range events {
		se := sseEvent{
			Kind: e.Kind, SID: e.SID, Server: e.Server,
			TS: e.TS.UTC().Format(time.RFC3339Nano),
		}
		if len(e.Payload) > 0 {
			se.Payload = e.Payload
		}
		out = append(out, se)
	}
	writeJSON(w, out)
}
