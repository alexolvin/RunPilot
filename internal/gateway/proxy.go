package gateway

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
	"time"

	"runpilot/internal/clock"
	"runpilot/internal/model"
	"runpilot/internal/store"
)

// attemptState — состояние одной попытки проксирования (переносится
// через контекст запроса в ModifyResponse/ErrorHandler).
type attemptState struct {
	mu          sync.Mutex
	wrote       int
	status      int
	migrate     bool
	copyErr     error
	firstByteAt time.Time
}

func newAttemptState() *attemptState { return &attemptState{} }

func (s *attemptState) markMigrate() {
	s.mu.Lock()
	s.migrate = true
	s.mu.Unlock()
}

func (s *attemptState) migrating() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.migrate
}

func (s *attemptState) wroteToClient() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.wrote > 0
}

func (s *attemptState) noteWrite(n int) {
	s.mu.Lock()
	s.wrote += n
	s.mu.Unlock()
}

func (s *attemptState) noteCopyErr(err error) {
	s.mu.Lock()
	s.copyErr = err
	s.mu.Unlock()
}

func (s *attemptState) copyError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.copyErr
}

func (s *attemptState) setStatus(code int) {
	s.mu.Lock()
	s.status = code
	s.mu.Unlock()
}

func (s *attemptState) statusCode() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

func (s *attemptState) firstByte() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.firstByteAt
}

func (s *attemptState) setFirstByte(at time.Time) {
	s.mu.Lock()
	if s.firstByteAt.IsZero() {
		s.firstByteAt = at
	}
	s.mu.Unlock()
}

type stateCtxKey struct{}

// withState — запрос со состоянием попытки в контексте (ReverseProxy
// использует r.Context() как основу исходящего запроса).
func withState(r *http.Request, st *attemptState) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), stateCtxKey{}, st))
}

func stateFromCtx(ctx context.Context) *attemptState {
	st, _ := ctx.Value(stateCtxKey{}).(*attemptState)
	return st
}

// captureWriter — ResponseWriter: при миграции (502/503/504 до первого
// байта) ответ не доходит до клиента; первый байт фиксируется по часам
// шлюза (t_first_byte строки request).
type captureWriter struct {
	http.ResponseWriter
	st  *attemptState
	clk clock.Clock

	headerSent bool
}

func (c *captureWriter) WriteHeader(code int) {
	c.st.setStatus(code)
	c.headerSent = true
	if c.st.migrating() {
		return
	}
	c.ResponseWriter.WriteHeader(code)
	c.st.setFirstByte(c.clk.Now())
}

func (c *captureWriter) Write(b []byte) (int, error) {
	if c.st.migrating() {
		return 0, errMigrate
	}
	if !c.headerSent {
		c.WriteHeader(http.StatusOK)
	}
	n, err := c.ResponseWriter.Write(b)
	if n > 0 {
		c.st.setFirstByte(c.clk.Now())
	}
	c.st.noteWrite(n)
	return n, err
}

func (c *captureWriter) Flush() {
	if c.st.migrating() || !c.headerSent {
		return
	}
	if f, ok := c.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// transportFor — транспорт сервера: ResponseHeaderTimeout =
// server.first_byte_timeout_sec (раздел 6 ТЗ). Вызывается с захваченным
// g.prMu (из proxyFor) — мютекс не реентерабельный.
func (g *Gateway) transportFor(srv *Server) *http.Transport {
	if t, ok := g.transports[srv.Cfg.Name]; ok {
		return t
	}
	t := &http.Transport{
		ResponseHeaderTimeout: time.Duration(srv.Cfg.FirstByteTimeoutSec) * time.Second,
	}
	g.transports[srv.Cfg.Name] = t
	return t
}

// proxyFor — ReverseProxy сервера (лениво). FlushInterval: -1 —
// стриминг без буферизации (раздел 6 ТЗ).
func (g *Gateway) proxyFor(srv *Server) *httputil.ReverseProxy {
	g.prMu.Lock()
	defer g.prMu.Unlock()
	if p, ok := g.proxies[srv.Cfg.Name]; ok {
		return p
	}
	p := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			g.direct(pr.Out, srv)
		},
		ModifyResponse: g.modifyResponseFor(srv),
		ErrorHandler:   g.proxyErrorHandler,
		FlushInterval:  -1,
		Transport:      g.transportFor(srv),
	}
	g.proxies[srv.Cfg.Name] = p
	return p
}

// direct — целевой URL (upstreams.openai.url + остаток пути) и
// заголовки: Authorization/x-api-key удаляются, ставится Bearer-ключ
// из переменной окружения key_env (раздел 6 ТЗ).
func (g *Gateway) direct(out *http.Request, srv *Server) {
	u, err := url.Parse(srv.Cfg.Upstreams.OpenAI.URL)
	if err != nil {
		u = &url.URL{Scheme: "http"}
	}
	out.URL.Scheme = u.Scheme
	out.URL.Host = u.Host
	out.URL.Path = joinPath(u.Path, out.URL.Path)
	out.Header.Del("Authorization")
	out.Header.Del("x-api-key")
	if key := srv.Key(); key != "" {
		out.Header.Set("Authorization", "Bearer "+key)
	}
}

// joinPath — "/base" + "v1/x" → "/base/v1/x".
func joinPath(base, rest string) string {
	if base == "" {
		return "/" + rest
	}
	return base + "/" + rest
}

// modifyResponseFor — до записи клиенту (за сервер srv): классификация
// отказа (S4/S5) + миграция 502/503/504 до первого байта (раздел 6 ТЗ).
// 4xx передаются без изменений (S6 обрабатывается отдельно). Состояние
// попытки — в контексте исходящего запроса.
func (g *Gateway) modifyResponseFor(srv *Server) func(*http.Response) error {
	return func(res *http.Response) error {
		st := stateFromCtx(res.Request.Context())
		if st != nil {
			st.setStatus(res.StatusCode)
		}
		var prefix []byte
		// v2 (S4/S5): классификация отказа по коду и телу (префикс).
		// Ошибка 5xx — небольшой JSON; читается префикс, остаток сливается.
		if res.StatusCode >= http.StatusInternalServerError {
			prefix, _ = io.ReadAll(io.LimitReader(res.Body, faultBodyLimit))
			_, _ = io.Copy(io.Discard, res.Body)
			_ = res.Body.Close()
			if fc := ClassifyFault(res.StatusCode, prefix); fc == model.FaultOOM || fc == model.FaultEngineDead {
				g.reportFault(srv.Cfg.Name, fc)
			}
		}
		switch {
		case res.StatusCode == http.StatusBadGateway ||
			res.StatusCode == http.StatusServiceUnavailable ||
			res.StatusCode == http.StatusGatewayTimeout:
			// отказ до первого байта: отметка миграции, пустое тело,
			// ответ не доходит до клиента (раздел 6 ТЗ).
			if st != nil {
				st.markMigrate()
			}
			res.Body = io.NopCloser(bytes.NewReader(nil))
		case res.StatusCode >= http.StatusInternalServerError:
			// прочие 5xx (500/501) — передать клиенту (тело из префикса).
			res.Body = io.NopCloser(bytes.NewReader(prefix))
		}
		return nil
	}
}

// proxyErrorHandler — ошибка ReverseProxy: до первого байта (соединение,
// таймаут заголовков) или после (разрыв потока). Решение — по факту
// записанного клиенту (в serveAttempt).
func (g *Gateway) proxyErrorHandler(w http.ResponseWriter, req *http.Request, err error) {
	st := stateFromCtx(req.Context())
	if st == nil {
		return
	}
	st.noteCopyErr(err)
	g.log.Warn("gateway: ошибка проксирования", "err", err)
}

// preFirstByteFail — отказ до первого байта (раздел 6 ТЗ): 502/503/504,
// ошибка соединения или ResponseHeaderTimeout; клиент жив.
func preFirstByteFail(st *attemptState, ctx context.Context) bool {
	if st.migrating() {
		return true
	}
	return st.copyError() != nil && !st.wroteToClient() && ctx.Err() == nil
}

// serveAttempt — попытка проксирования и, при отказе до первого байта,
// один повтор через миграцию (раздел 6 ТЗ). raw — исходное тело: подмена
// model/max_* происходит под сервер каждой попытки.
func (g *Gateway) serveAttempt(w http.ResponseWriter, r *http.Request, ctx context.Context,
	sess store.SessionRecord, rest string, raw []byte, decision *leaseDecision, t0 time.Time) {
	body, err := transformBody(raw,
		decision.server.Cfg.Upstreams.OpenAI.Model,
		decision.server.Cfg.MaxOutputTokens)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	prefail, st := g.doAttempt(w, r, ctx, rest, body, decision)
	if !prefail {
		g.recordRequest(sess, decision, rest, st, t0, ctx)
		return
	}
	g.resetClientHeaders(w)
	next, err := g.migratePass(ctx, sess, decision)
	if err != nil {
		if errors.Is(err, errHoldTimeout) {
			g.writeNoSlot(w, g.queuePositionOrOne(sess.SID))
		}
		return
	}
	g.bumpInflight(sess.SID, next.server.Cfg.Name)
	defer g.unBumpInflight(sess.SID, next.server.Cfg.Name)
	// Тело под новый сервер: model и max_output_tokens выбранного
	// сервера (раздел 6 ТЗ). raw уже валиден — ошибка невозможна.
	body2, err := transformBody(raw,
		next.server.Cfg.Upstreams.OpenAI.Model,
		next.server.Cfg.MaxOutputTokens)
	if err != nil {
		http.Error(w, "bad gateway", http.StatusBadGateway)
		return
	}
	prefail2, st2 := g.doAttempt(w, r, ctx, rest, body2, next)
	if prefail2 {
		// Повторный отказ до первого байта: повторный повтор ТЗ не
		// описывает — состояние второго сервера по проверке /health,
		// аренда освобождается, клиенту 502 (документированное
		// расширение в CONTROL Э3).
		if !next.server.HealthOK(ctx) {
			g.servers.SetState(next.server.Cfg.Name, model.ServerDown)
		}
		if next.lease != nil {
			_ = g.st.LeaseRelease(sess.SID, releaseReasonServerDown, g.clk.Now())
			g.wake()
		}
		http.Error(w, "bad gateway", http.StatusBadGateway)
	}
	// Строка request — за итоговый сервер (ход прошёл здесь).
	g.recordRequest(sess, next, rest, st2, t0, ctx)
}

// doAttempt — одна попытка; (true, st) = отказ до первого байта.
func (g *Gateway) doAttempt(w http.ResponseWriter, r *http.Request, ctx context.Context,
	rest string, body []byte, decision *leaseDecision) (bool, *attemptState) {
	st := newAttemptState()
	req := withState(r, st)
	req.URL.Path = rest
	req.Body = io.NopCloser(bytes.NewReader(body))
	// Тело подменялось (model/key) — длина могла измениться;
	// Content-Length обязан совпадать с фактической длиной тела.
	req.ContentLength = int64(len(body))
	// Ответ пишется через captureWriter: при миграции байты не доходят
	// до клиента, первый байт фиксируется по часам шлюза.
	cw := &captureWriter{ResponseWriter: w, st: st, clk: g.clk}
	g.proxyFor(decision.server).ServeHTTP(cw, req)
	if preFirstByteFail(st, ctx) {
		return true, st
	}
	g.finishAttempt(w, st)
	return false, st
}

// finishAttempt — пост-обработка попытки: разрыв после начала потока
// закрывает соединение с клиентом (раздел 6 ТЗ); код ошибки строки
// request определяется из состояния (resultError).
func (g *Gateway) finishAttempt(w http.ResponseWriter, st *attemptState) {
	if st.copyError() == nil || !st.wroteToClient() {
		return
	}
	if hj, ok := w.(http.Hijacker); ok {
		if conn, _, err := hj.Hijack(); err == nil {
			_ = conn.Close()
		}
	}
}

// resultError — код ошибки строки request (пусто = успех).
func (g *Gateway) resultError(st *attemptState, ctx context.Context) string {
	if st.copyError() == nil {
		return ""
	}
	if st.wroteToClient() {
		return "UPSTREAM_LOST"
	}
	if ctx.Err() != nil {
		return "CLIENT_GONE"
	}
	return "UPSTREAM_FAILED"
}

// recordRequest — строка request без токенов (раздел 6 ТЗ): строка
// request, requests аренды, lease.last_request_end, last_server сессии.
func (g *Gateway) recordRequest(sess store.SessionRecord, decision *leaseDecision,
	rest string, st *attemptState, t0 time.Time, ctx context.Context) {
	tEnd := g.clk.Now()
	req := model.Request{
		SID:    sess.SID,
		TurnID: 0, // ходы ведёт планировщик (Э4); в Э3 запрос без хода
		Server: decision.server.Cfg.Name,
		Path:   rest,
		Status: st.statusCode(),
		TStart: t0,
		TEnd:   tEnd,
		HeldMS: decision.heldMS,
		Error:  g.resultError(st, ctx),
	}
	if first := st.firstByte(); !first.IsZero() {
		req.TFirstByte = first
	}
	if decision.lease != nil {
		g.setLastRequestEnd(decision.lease.ID, tEnd)
	}
	_ = g.st.RequestRecord(req)
}

// resetClientHeaders — после поглощённого ответа 502/503/504 заголовки
// клиента очищаются перед повторной попыткой.
func (g *Gateway) resetClientHeaders(w http.ResponseWriter) {
	h := w.Header()
	for k := range h {
		h.Del(k)
	}
}
