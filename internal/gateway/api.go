package gateway

// API-клиенты шлюза (безсессионные OpenAI-запросы в общую очередь).
//
// POST /v1/chat/completions с Authorization: Bearer <ключ>. Один запрос =
// один ход псевдо-сессии (sid=api:<name>). Тело проксируется без изменений
// (transformBody). stream:true → 400. Ожидание слота — до client.max_hold_sec,
// потом 503 + Retry-After. Липкая аренда: сервер удерживается за клиентом
// sticky_idle_sec (без вытеснения кодера).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"runpilot/internal/clock"
	"runpilot/internal/model"
	"runpilot/internal/store"
)

// errAPIHoldTimeout — удержание API-запроса истекло (client.max_hold_sec).
var errAPIHoldTimeout = errors.New("gateway: api hold timeout")

// stickyLeaseRec — липкая аренда: сервер удерживается за клиентом.
type stickyLeaseRec struct {
	server string
	since  time.Time
}

// apiSticky — липкие аренды по клиентам (в памяти; не переживает рестарт).
// (Поле добавляется в Gateway; здесь — вспомогательные функции.)

// bearerKey — ключ из Authorization: Bearer <key>.
func bearerKey(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > len(bearerPrefix) && strings.EqualFold(h[:len(bearerPrefix)], bearerPrefix) {
		return strings.TrimSpace(h[len(bearerPrefix):])
	}
	return ""
}

// writeAPIError — JSON-ошибка API-клиента (формат OpenAI error).
func (g *Gateway) writeAPIError(w http.ResponseWriter, code int, msg, codeStr string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	fmt.Fprintf(w, `{"error":{"message":"%s","type":"server_error","code":"%s"}}`, msg, codeStr)
}

// isStreamBody — stream:true в теле запроса.
func isStreamBody(raw []byte) bool {
	var req struct {
		Stream bool `json:"stream"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return false
	}
	return req.Stream
}

// handleAPIRequest — POST /v1/chat/completions (Bearer-ключ).
func (g *Gateway) handleAPIRequest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := g.clk.Now()
	key := bearerKey(r)
	client, err := g.st.GetApiClientByKey(key)
	if err != nil || !client.Enabled {
		g.writeAPIError(w, http.StatusUnauthorized, "invalid api key", "invalid_api_key")
		_ = g.st.ApiRequestRecord(store.ApiRequest{TS: now, Client: client.Name, Result: "401"})
		return
	}
	sid := store.APISessionSID(client.Name)

	// Тело целиком, лимит max_body_mb.
	limit := g.cfg.Gateway.MaxBodyBytes()
	r.Body = http.MaxBytesReader(w, r.Body, limit+1)
	raw, rerr := io.ReadAll(r.Body)
	_ = r.Body.Close()
	if rerr != nil {
		var maxErr *http.MaxBytesError
		if errors.As(rerr, &maxErr) {
			g.writeAPIError(w, http.StatusRequestEntityTooLarge, "body too large", "body_too_large")
		} else {
			g.writeAPIError(w, http.StatusBadRequest, "bad request", "bad_request")
		}
		_ = g.st.ApiRequestRecord(store.ApiRequest{TS: now, Client: client.Name, Result: "400"})
		return
	}

	// stream:true — только для клиентов с allow_stream (учёт токенов — из
	// usage-чанка, см. streamingWriter; usage-чанк с пустым choices клиенту
	// не передаётся).
	stream := isStreamBody(raw)
	if stream && !client.AllowStream {
		g.writeAPIError(w, http.StatusBadRequest, "streaming is not enabled for this api client", "stream_not_enabled")
		_ = g.st.ApiRequestRecord(store.ApiRequest{TS: now, Client: client.Name, Result: "400"})
		return
	}

	// Псевдо-сессия.
	if err := g.st.EnsureAPISession(client.Name, client.Class); err != nil {
		g.writeAPIError(w, http.StatusInternalServerError, "internal error", "internal")
		return
	}
	sess, err := g.st.GetSession(sid)
	if err != nil {
		g.writeAPIError(w, http.StatusInternalServerError, "internal error", "internal")
		return
	}

	// Ёмкость: max_inflight.
	if g.tryBumpTotalInflight() {
		g.writeAPI503(w, "runpilot_no_slot")
		_ = g.st.ApiRequestRecord(store.ApiRequest{TS: now, Client: client.Name, Result: "503"})
		return
	}
	defer g.unBumpTotalInflight()

	// Режим координатора.
	if m, _ := g.st.Mode(); m == string(model.ModeEmergency) {
		g.writeAPI503(w, "runpilot_emergency")
		_ = g.st.ApiRequestRecord(store.ApiRequest{TS: now, Client: client.Name, Result: "503"})
		return
	}

	// Контекст запроса (аварийная остановка отменяет).
	pctx, cancelCause := context.WithCancelCause(ctx)
	id := g.registerInflight(sid, cancelCause)
	defer g.unregisterInflight(id)
	r = r.Clone(pctx)
	ctx = pctx

	// Память тела.
	if !g.tryBumpBuffered(int64(len(raw))) {
		g.writeAPI503(w, "runpilot_gateway_busy")
		_ = g.st.ApiRequestRecord(store.ApiRequest{TS: now, Client: client.Name, Result: "503"})
		return
	}
	defer g.unBumpBuffered(int64(len(raw)))

	g.log.Debug("gateway: api: параметры запроса", "client", client.Name,
		"stream", stream, "bytes", len(raw))

	// Разрешение аренды (BATCH, allowed_servers, max_hold_sec).
	decision, err := g.resolveAPIPass(ctx, sess, client)
	if err != nil {
		if errors.Is(err, errAPIHoldTimeout) {
			g.writeAPI503(w, "runpilot_no_slot")
			_ = g.st.ApiRequestRecord(store.ApiRequest{TS: now, Client: client.Name,
				WaitMS: g.clk.Now().Sub(now).Milliseconds(), Result: "503"})
			return
		}
		if ctx.Err() != nil {
			_ = g.st.ApiRequestRecord(store.ApiRequest{TS: now, Client: client.Name, Result: "499"})
			return
		}
		g.log.Error("gateway: api: разрешение аренды", "client", client.Name, "err", err)
		g.writeAPIError(w, http.StatusBadGateway, "bad gateway", "bad_gateway")
		_ = g.st.ApiRequestRecord(store.ApiRequest{TS: now, Client: client.Name, Result: "502"})
		return
	}
	srvName := decision.server.Cfg.Name
	g.bumpInflight(sid, srvName)
	defer g.unBumpInflight(sid, srvName)

	g.reqHook(sid, true)
	defer g.reqHook(sid, false)

	// Проксирование.
	g.serveAPIAttempt(w, r, ctx, sess, client, "v1/chat/completions", raw, decision, now)
}

// writeAPI503 — 503 + Retry-After для API-клиента.
func (g *Gateway) writeAPI503(w http.ResponseWriter, code string) {
	w.Header().Set("Retry-After", strconv.Itoa(g.cfg.Gateway.RetryAfterSec))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusServiceUnavailable)
	fmt.Fprintf(w, `{"error":{"message":"runpilot: %s","type":"server_error","code":"%s"}}`, code, code)
}

// resolveAPIPass — разрешение аренды для API-клиента:
//   1. действующая аренда (липкая) → проксировать на её сервер;
//   2. липкий сервер (sticky_idle_sec) и слот свободен → аренда;
//   3. выдача (BATCH, allowed_servers);
//   4. удержание до client.max_hold_sec.
func (g *Gateway) resolveAPIPass(ctx context.Context, sess store.SessionRecord, client store.ApiClient) (*leaseDecision, error) {
	// Правило 1: действующая аренда.
	if lease, err := g.st.LeaseGet(sess.SID); err == nil {
		if srv := g.servers.ByName(lease.Server); srv != nil {
			return &leaseDecision{server: srv, lease: lease}, nil
		}
		_ = g.st.LeaseRelease(sess.SID, releaseReasonServerDown, g.clk.Now())
		g.wake()
	}
	// Правило 2–3: выдача (с липким сервером в приоритете).
	class := client.Class
	if lease, srv := g.grantAPI(class, client, sess.SID); srv != nil {
		g.sessionGrantedEvents(sess)
		return &leaseDecision{server: srv, lease: lease}, nil
	}
	// Правило 4: удержание.
	return g.holdAPIPass(ctx, sess, client)
}

// grantAPI — выдача аренды API-клиенту (BATCH, allowed_servers, липкий сервер).
func (g *Gateway) grantAPI(class model.QueueClass, client store.ApiClient, sid string) (*model.Lease, *Server) {
	order := g.pickAPIServers(client)
	// Липкий сервер — в приоритете (если в allowed и в порядке).
	if s, ok := g.stickyOf(client); ok {
		if i := indexServer(order, s.Cfg.Name); i > 0 {
			order = append([]*Server{order[i]}, append(order[:i:i], order[i+1:]...)...)
		}
	}
	for _, srv := range order {
		if !srv.candidateOK(class) {
			continue
		}
		leased, err := g.st.LeaseCountByServer(srv.Cfg.Name)
		if err != nil || leased >= srv.Cfg.Slots {
			continue
		}
		l := model.Lease{
			SID:       sid,
			Server:    srv.Cfg.Name,
			Slot:      leased + 1,
			State:     model.LeaseActive,
			Origin:    model.LeaseOriginImplicit,
			GrantedAt: g.clk.Now(),
		}
		got, err := g.st.LeaseCreateIfAbsent(l, srv.Cfg.Slots, true)
		if err != nil {
			if !errors.Is(err, store.ErrSlotFull) {
				g.log.Error("gateway: api: создать аренду", "sid", sid, "err", err)
			}
			continue
		}
		if got.Server != srv.Cfg.Name {
			if other := g.servers.ByName(got.Server); other != nil {
				return got, other
			}
			continue
		}
		return got, srv
	}
	return nil, nil
}

// pickAPIServers — серверы для API-клиента (allowed_servers, по приоритету).
func (g *Gateway) pickAPIServers(client store.ApiClient) []*Server {
	if len(client.AllowedServers) == 0 {
		return g.servers.All()
	}
	allowed := map[string]bool{}
	for _, name := range client.AllowedServers {
		allowed[name] = true
	}
	var out []*Server
	for _, srv := range g.servers.All() {
		if allowed[srv.Cfg.Name] {
			out = append(out, srv)
		}
	}
	return out
}

// indexServer — индекс сервера по имени (-1, если нет).
func indexServer(list []*Server, name string) int {
	for i, s := range list {
		if s.Cfg.Name == name {
			return i
		}
	}
	return -1
}

// holdAPIPass — удержание API-запроса до client.max_hold_sec.
func (g *Gateway) holdAPIPass(ctx context.Context, sess store.SessionRecord, client store.ApiClient) (*leaseDecision, error) {
	now := g.clk.Now()
	class := client.Class
	_ = g.st.QueueUpsertHeld(model.QueueEntry{
		SID:         sess.SID,
		Class:       class,
		EnqueuedAt:  now,
		NotBefore:   now,
		Constraint:  model.NoConstraint,
		Mode:        model.QueueModeSubmit,
		HeldRequest: true,
		HeldSince:   now,
	})
	g.heldCountAdd(sess.SID)
	g.log.Info("gateway: api: удержание началось", "client", client.Name, "max_hold_sec", client.MaxHoldSec)
	wake := g.subscribeWake()
	defer g.unsubscribeWake(wake)
	timer := time.NewTimer(time.Duration(client.MaxHoldSec) * time.Second)
	defer timer.Stop()

	for {
		if lease, err := g.st.LeaseGet(sess.SID); err == nil {
			if srv := g.servers.ByName(lease.Server); srv != nil {
				held := g.clk.Now().Sub(now).Milliseconds()
				g.metrics.RecordHold(float64(held) / msToSec)
				g.heldCountDec(sess.SID)
				g.sessionGrantedEvents(sess)
				return &leaseDecision{server: srv, lease: lease, heldMS: held}, nil
			}
		}
		select {
		case <-ctx.Done():
			g.clientGone(sess.SID, g.clk.Now().Sub(now).Milliseconds())
			return nil, ctx.Err()
		case <-timer.C:
			g.heldCountDec(sess.SID)
			_ = g.st.QueueDelete(sess.SID)
			g.metrics.RecordHold(float64(g.clk.Now().Sub(now).Milliseconds()) / msToSec)
			return nil, errAPIHoldTimeout
		case <-wake:
		}
	}
}

// serveAPIAttempt — проксирование API-запроса (+ учёт api_request, липкая аренда).
// stream:true (allow_stream) — SSE-поток насквозь, учёт токенов из usage-чанка.
func (g *Gateway) serveAPIAttempt(w http.ResponseWriter, r *http.Request, ctx context.Context,
	sess store.SessionRecord, client store.ApiClient, rest string, raw []byte, decision *leaseDecision, t0 time.Time) {
	stream := isStreamBody(raw)
	wantsUsage := hasIncludeUsage(raw)
	var body []byte
	var err error
	if stream {
		body, err = transformAPIStreamBody(raw,
			decision.server.Cfg.Upstreams.OpenAI.Model,
			decision.server.Cfg.MaxOutputTokens, wantsUsage)
	} else {
		body, err = transformBody(raw,
			decision.server.Cfg.Upstreams.OpenAI.Model,
			decision.server.Cfg.MaxOutputTokens)
	}
	if err != nil {
		g.writeAPIError(w, http.StatusBadRequest, "bad request", "bad_request")
		_ = g.st.ApiRequestRecord(store.ApiRequest{TS: t0, Client: client.Name, Result: "400"})
		return
	}
	var (
		st *attemptState
		u  *apiUsage
	)
	if stream {
		var prefail bool
		prefail, st, u = g.doAPIStreamAttempt(w, r, ctx, rest, body, decision, sess.SID, !wantsUsage)
		if prefail {
			g.apiPrefail(w, sess.SID, client, decision, t0)
			return
		}
	} else {
		var prefail bool
		var respBody []byte
		prefail, st, respBody = g.doAPIAttempt(w, r, ctx, rest, body, decision, sess.SID)
		if prefail {
			g.apiPrefail(w, sess.SID, client, decision, t0)
			return
		}
		u = usageFromBody(respBody)
	}
	g.finishAttempt(w, st)
	// Учёт: токены из usage (non-streaming — из тела, streaming — из usage-чанка).
	g.recordAPIRequest(client, decision, st, t0, u)
	g.apiSessionIdle(sess.SID)
	// Липкая аренда: сервер удерживается за клиентом sticky_idle_sec
	// (аренда живёт, повторные запросы не идут в очередь). Если в очереди
	// ждёт кодер — аренда снимается немедленно (сервер отдаётся кодеру).
	// Без липкой аренды (StickyIdleSec=0) — аренда снимается после запроса.
	switch {
	case g.coderInQueue():
		g.releaseStickyLease(client.Name)
	case client.StickyIdleSec > 0:
		g.setSticky(client, decision.server)
	default:
		if decision.lease != nil {
			_ = g.st.LeaseRelease(sess.SID, model.ReleaseTurnDone, g.clk.Now())
			g.wake()
		}
	}
}

// apiSessionIdle — API-псевдосессия после запроса возвращается в IDLE
// (иначе grantable для held-запроса не пропустит RUNNING — планировщик
// не выдаст слот удержанному запросу).
func (g *Gateway) apiSessionIdle(sid string) {
	if sess, err := g.st.GetSession(sid); err == nil && sess.Kind == model.KindAPI {
		_ = g.st.SetSessionState(sid, model.SessionIdle, "", g.clk.Now())
	}
}

// coderInQueue — в очереди ждёт кодер (сессия не API-псевдосессии).
func (g *Gateway) coderInQueue() bool {
	entries, err := g.st.QueueList()
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.SID, "api:") {
			return true
		}
	}
	return false
}

// bufferingWriter — ResponseWriter: буферизует тело (non-streaming ответ
// API-клиента), чтобы учесть токены (usage) до отправки клиенту.
type bufferingWriter struct {
	http.ResponseWriter
	st  *attemptState
	clk clock.Clock

	buf        bytes.Buffer
	status     int
	headerSent bool
}

func (b *bufferingWriter) WriteHeader(code int) {
	b.st.setStatus(code)
	if !b.headerSent {
		b.status = code
		b.headerSent = true
	}
}

func (b *bufferingWriter) Write(data []byte) (int, error) {
	if b.st.migrating() {
		return 0, errMigrate
	}
	if !b.headerSent {
		b.WriteHeader(http.StatusOK)
	}
	n, err := b.buf.Write(data)
	if n > 0 {
		b.st.setFirstByte(b.clk.Now())
	}
	b.st.noteWrite(n)
	return n, err
}

// Flush — non-streaming: тело буферизуется, клиенту не посылается.
func (b *bufferingWriter) Flush() {}

// flushToClient — дописать буфер клиенту (заголовки уже в Header).
func (b *bufferingWriter) flushToClient() {
	if b.status == 0 {
		b.status = http.StatusOK
	}
	b.ResponseWriter.WriteHeader(b.status)
	_, _ = b.buf.WriteTo(b.ResponseWriter)
}

// doAPIAttempt — как doAttempt, но с буферизацией ответа (учёт токенов).
// Возвращает (prefail, st, respBody).
func (g *Gateway) doAPIAttempt(w http.ResponseWriter, r *http.Request, ctx context.Context,
	rest string, body []byte, decision *leaseDecision, sid string) (bool, *attemptState, []byte) {
	st := newAttemptState()
	req := withState(r, st)
	req = withSID(req, sid)
	req.URL.Path = rest
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	bw := &bufferingWriter{ResponseWriter: w, st: st, clk: g.clk}
	g.proxyFor(decision.server).ServeHTTP(bw, req)
	if preFirstByteFail(st, ctx) {
		return true, st, nil
	}
	// Тело для учёта — до flushToClient: WriteTo расходует буфер
	// (сдвигает read-offset), и Bytes() после него пуст.
	respBody := bw.buf.Bytes()
	bw.flushToClient()
	return false, st, respBody
}

// doAPIStreamAttempt — стриминговое проксирование API-запроса: SSE-поток
// насквозь к клиенту, usage извлекается (учёт токенов). Отказ до первого
// байта — как в doAPIAttempt (prefail). hideUsageChunk — usage-чанк с
// пустым choices не передавать клиенту (клиент не просил include_usage).
func (g *Gateway) doAPIStreamAttempt(w http.ResponseWriter, r *http.Request, ctx context.Context,
	rest string, body []byte, decision *leaseDecision, sid string, hideUsageChunk bool) (bool, *attemptState, *apiUsage) {
	st := newAttemptState()
	req := withState(r, st)
	req = withSID(req, sid)
	req.URL.Path = rest
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	sw := &streamingWriter{ResponseWriter: w, st: st, clk: g.clk, hideUsageChunk: hideUsageChunk}
	g.proxyFor(decision.server).ServeHTTP(sw, req)
	if preFirstByteFail(st, ctx) {
		return true, st, nil
	}
	return false, st, sw.usage()
}

// transformAPIStreamBody — тело стримингового API-запроса: как transformBody
// + stream_options.include_usage=true (учёт токенов из usage-чанка). Если
// клиент сам задал stream_options — merge (остальные поля сохраняются).
// clientWantsUsage=true — клиент сам запросил include_usage: не трогаем
// (usage-чанк пройдёт к клиенту, см. streamingWriter).
func transformAPIStreamBody(raw []byte, model string, maxOutput int, clientWantsUsage bool) ([]byte, error) {
	doc, err := transformBodyDoc(raw, model, maxOutput)
	if err != nil {
		return nil, err
	}
	if !clientWantsUsage {
		var so map[string]json.RawMessage
		if v, ok := doc["stream_options"]; ok {
			_ = json.Unmarshal(v, &so)
		}
		if so == nil {
			so = map[string]json.RawMessage{}
		}
		so["include_usage"] = json.RawMessage("true")
		b, err := json.Marshal(so)
		if err != nil {
			return nil, fmt.Errorf("gateway: stream_options: %w", err)
		}
		doc["stream_options"] = b
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("gateway: сборка тела: %w", err)
	}
	return out, nil
}

// hasIncludeUsage — клиент сам запросил stream_options.include_usage.
func hasIncludeUsage(raw []byte) bool {
	var req struct {
		StreamOptions struct {
			IncludeUsage bool `json:"include_usage"`
		} `json:"stream_options"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return false
	}
	return req.StreamOptions.IncludeUsage
}

// streamingWriter — ResponseWriter стримингового API-запроса: SSE-поток
// передаётся клиенту побайто, usage/model извлекаются (учёт токенов).
// Usage-чанк (choices: [] + usage) клиенту, не запросившему include_usage,
// НЕ передаётся (клиент читает choices[0]).
type streamingWriter struct {
	http.ResponseWriter
	st  *attemptState
	clk clock.Clock

	// hideUsageChunk — не передавать usage-чанк клиенту.
	hideUsageChunk bool

	headerSent bool
	pending    []byte

	model      string
	prompt     int
	completion int
	haveUsage  bool
}

func (s *streamingWriter) WriteHeader(code int) {
	s.st.setStatus(code)
	if !s.headerSent {
		s.headerSent = true
		if !s.st.migrating() {
			s.ResponseWriter.WriteHeader(code)
		}
	}
}

func (s *streamingWriter) Write(data []byte) (int, error) {
	if s.st.migrating() {
		return 0, errMigrate
	}
	if !s.headerSent {
		s.WriteHeader(http.StatusOK)
	}
	s.st.setFirstByte(s.clk.Now())
	s.pending = append(s.pending, data...)
	// Полный SSE-событие = блок до "\n\n"; некомплектное — в pending.
	for {
		idx := bytes.Index(s.pending, []byte(sseEventSep))
		if idx < 0 {
			break
		}
		event := s.pending[:idx]
		s.pending = s.pending[idx+len(sseEventSep):]
		if s.forwardEvent(event) {
			s.Flush()
		}
	}
	s.st.noteWrite(len(data))
	return len(data), nil
}

// Flush — прокинуть клиенту (ReverseProxy с FlushInterval: -1 вызывает
// после каждого write апстрима).
func (s *streamingWriter) Flush() {
	if !s.headerSent || s.st.migrating() {
		return
	}
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// forwardEvent — одно SSE-событие (строка "data: …" без завершающего \n\n).
// true — событие передано клиенту.
func (s *streamingWriter) forwardEvent(raw []byte) bool {
	payload := raw
	if i := bytes.IndexByte(raw, ':'); i >= 0 {
		payload = bytes.TrimSpace(raw[i+1:])
	}
	if bytes.Equal(payload, []byte("[DONE]")) {
		return s.writeEvent(raw)
	}
	// Извлечь usage/model для учёта; событие не пересобирается.
	var probe struct {
		Model   string              `json:"model"`
		Choices []json.RawMessage   `json:"choices"`
		Usage   *struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(payload, &probe); err != nil {
		return s.writeEvent(raw)
	}
	if probe.Model != "" && s.model == "" {
		s.model = probe.Model
	}
	if probe.Usage != nil {
		s.prompt = probe.Usage.PromptTokens
		s.completion = probe.Usage.CompletionTokens
		s.haveUsage = true
		// Usage-чанк с пустым choices — клиенту, не запросившему
		// include_usage, не посылать (клиент читает choices[0]).
		if s.hideUsageChunk && len(probe.Choices) == 0 {
			return false
		}
	}
	return s.writeEvent(raw)
}

// writeEvent — событие + завершающий \n\n.
func (s *streamingWriter) writeEvent(raw []byte) bool {
	buf := append(append([]byte{}, raw...), []byte(sseEventSep)...)
	_, err := s.ResponseWriter.Write(buf)
	return err == nil
}

// usage — извлечённый usage (учёт токенов); nil, если usage-чанка не было.
func (s *streamingWriter) usage() *apiUsage {
	if s.model == "" && !s.haveUsage {
		return nil
	}
	return &apiUsage{model: s.model, prompt: s.prompt, completion: s.completion}
}

// apiUsage — учёт токенов API-запроса (non-streaming — из тела ответа,
// streaming — из usage-чанка).
type apiUsage struct {
	model      string
	prompt     int
	completion int
}

// usageFromBody — usage из non-streaming ответа.
func usageFromBody(respBody []byte) *apiUsage {
	var resp struct {
		Model string `json:"model"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil
	}
	return &apiUsage{model: resp.Model, prompt: resp.Usage.PromptTokens, completion: resp.Usage.CompletionTokens}
}

// apiPrefail — отказ до первого байта (502/503/504, ошибка соединения):
// 502 + Retry-After, аренда снимается.
func (g *Gateway) apiPrefail(w http.ResponseWriter, sid string, client store.ApiClient, decision *leaseDecision, t0 time.Time) {
	g.resetClientHeaders(w)
	if decision.lease != nil {
		_ = g.st.LeaseRelease(sid, releaseReasonServerDown, g.clk.Now())
		g.wake()
	}
	g.apiSessionIdle(sid)
	g.writeAPI502WithRetry(w, "runpilot_server_down")
	_ = g.st.ApiRequestRecord(store.ApiRequest{
		TS: t0, Client: client.Name, Server: decision.server.Cfg.Name,
		LatencyMS: g.clk.Now().Sub(t0).Milliseconds(), Result: "502",
	})
}

// writeAPI502WithRetry — 502 + Retry-After (сервер упал в запросе).
func (g *Gateway) writeAPI502WithRetry(w http.ResponseWriter, code string) {
	w.Header().Set("Retry-After", strconv.Itoa(g.cfg.Gateway.RetryAfterSec))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadGateway)
	fmt.Fprintf(w, `{"error":{"message":"runpilot: %s","type":"server_error","code":"%s"}}`, code, code)
}

// recordAPIRequest — строка api_request (токены из usage: non-streaming —
// из тела ответа, streaming — из usage-чанка).
func (g *Gateway) recordAPIRequest(client store.ApiClient, decision *leaseDecision, st *attemptState, t0 time.Time, u *apiUsage) {
	tEnd := g.clk.Now()
	result := "OK"
	if st.copyError() != nil {
		result = "502"
	}
	var modelName string
	var prompt, completion int
	if u != nil {
		modelName = u.model
		prompt = u.prompt
		completion = u.completion
	}
	_ = g.st.ApiRequestRecord(store.ApiRequest{
		TS:               t0,
		Client:           client.Name,
		Server:           decision.server.Cfg.Name,
		Model:            modelName,
		PromptTokens:     prompt,
		CompletionTokens: completion,
		WaitMS:           decision.heldMS,
		LatencyMS:        tEnd.Sub(t0).Milliseconds(),
		Result:           result,
	})
	g.metrics.RecordRequest(decision.server.Cfg.Name, strconv.Itoa(st.statusCode()))
}

// --- Липкая аренда (в памяти) ---

// setSticky — сервер удерживается за клиентом (аренда живёт
// sticky_idle_sec; таймер снимает её, если клиент в простое).
func (g *Gateway) setSticky(client store.ApiClient, srv *Server) {
	g.stickyMu.Lock()
	defer g.stickyMu.Unlock()
	g.sticky[client.Name] = stickyLeaseRec{server: srv.Cfg.Name, since: g.clk.Now()}
	if t, ok := g.stickyTimers[client.Name]; ok {
		t.Stop()
	}
	g.stickyTimers[client.Name] = time.AfterFunc(
		time.Duration(client.StickyIdleSec)*time.Second,
		func() { g.releaseStickyLease(client.Name) })
}

// releaseStickyLease — снять липкую аренду клиента по истечении
// sticky_idle_sec (клиент в простое → слот освобождается).
func (g *Gateway) releaseStickyLease(name string) {
	g.stickyMu.Lock()
	delete(g.sticky, name)
	if t, ok := g.stickyTimers[name]; ok {
		t.Stop()
		delete(g.stickyTimers, name)
	}
	g.stickyMu.Unlock()
	sid := store.APISessionSID(name)
	if _, err := g.st.LeaseGet(sid); err == nil {
		_ = g.st.LeaseRelease(sid, model.ReleaseTurnDone, g.clk.Now())
		g.wake()
	}
}

// stickyOf — действующая липкая аренда клиента (sticky_idle_sec).
func (g *Gateway) stickyOf(client store.ApiClient) (*Server, bool) {
	g.stickyMu.Lock()
	rec, ok := g.sticky[client.Name]
	g.stickyMu.Unlock()
	if !ok {
		return nil, false
	}
	if g.clk.Now().Sub(rec.since) > time.Duration(client.StickyIdleSec)*time.Second {
		g.stickyMu.Lock()
		delete(g.sticky, client.Name)
		g.stickyMu.Unlock()
		return nil, false
	}
	srv := g.servers.ByName(rec.server)
	if srv == nil {
		return nil, false
	}
	return srv, true
}
