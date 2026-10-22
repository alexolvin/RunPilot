// Package gateway — шлюз runpilot (раздел 6 ТЗ).
//
// Прозрачный обратный прокси на gateway_port: по sid из пути находит
// аренду, подменяет модель и ключ, стримит ответ без буферизации
// (httputil.ReverseProxy, FlushInterval: -1). Учёт токенов не ведётся.
//
// Строгие правила:
//   - пороги (hold_max_sec, max_body_mb, retry_after_sec, max_inflight,
//     max_output_tokens, first_byte_timeout_sec) — только из конфигурации;
//   - время — только internal/clock;
//   - HTTP-коды — константы net/http (числовые литералы в пакете запрещены
//     scripts/check-literals, кроме 0 и 1).
package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"runpilot/internal/clock"
	"runpilot/internal/config"
	"runpilot/internal/model"
	"runpilot/internal/store"
)

// errHoldTimeout — удержание истекло (gateway.hold_max_sec).
var errHoldTimeout = errors.New("gateway: hold timeout")

// errMigrate — служебная: копия тела остановлена из-за 502/503/504
// до первого байта (раздел 6 ТЗ: миграция).
var errMigrate = errors.New("gateway: migrate before first byte")

// releaseReasonServerDown — код освобождения аренды при отказе апстрима
// до первого байта (раздел 6 ТЗ).
const releaseReasonServerDown = "SERVER_DOWN"

// Gateway — шлюз координатора.
type Gateway struct {
	cfg     *config.Config
	st      *store.Store
	clk     clock.Clock
	log     *slog.Logger
	servers *Servers

	// Прокси и транспорты по серверам (лениво).
	prMu       sync.Mutex
	proxies    map[string]*httputil.ReverseProxy
	transports map[string]*http.Transport

	// Ждущие слота (удержание): просыпаются при освобождении аренды.
	wm      sync.Mutex
	waiters map[chan struct{}]struct{}

	// Счётчики inflight сессии и сервера (раздел 6 ТЗ). pendingGone — аренда
	// сессии снимается после GONE, когда inflight(sid)=0 (запрос завершён),
	// не раньше (иначе слот выдался бы, пока запрос убитой сессии летит в апстрим).
	im          sync.Mutex
	total       int
	bySID       map[string]int
	byServer    map[string]int
	pendingGone map[string]string // sid → reason (снять аренду, когда inflight(sid)=0)

	// W7 (6.2): контексты идущих генерирующих запросов — аварийная
	// остановка отменяет все (CancelAll): vLLM прекращает генерацию.
	ic          sync.Mutex
	inflightCtx map[int]context.CancelCauseFunc
	inflightSID map[int]string // id → sid (отмена запросов конкретной сессии при GONE)
	icSeq       int64

	// last_request_end действующих аренд (раздел 6 ТЗ; в памяти,
	// в схему lease не входит).
	lm      sync.Mutex
	lastEnd map[int64]time.Time

	// Хук учёта запросов для планировщика (раздел 8 ТЗ: завершение
	// хода по inflight и last_request_end). Вызывается вне блокировок.
	hm      sync.Mutex
	hookReq func(sid string, started bool)

	// v2 (S4/S5): наблюдатель отказов (монитор ведёт окно отказов и
	// карантин). reportFault вызывается из modifyResponse (поток прокси).
	fr            sync.Mutex
	faultReporter FaultReporter

	// Метрики шлюза (раздел 14 ТЗ).
	metrics *Metrics

	// Счётчик удерживаемых запросов по sid (п.2: два параллельных запроса
	// одной сессии). При client_gone запись очереди удаляется, только когда
	// обрывается последний удерживаемый запрос этой sid.
	hm2       sync.Mutex
	heldCount map[string]int

	// Сумма тел в памяти шлюза (проксируемые + удержанные), байт (W15 этап 1).
	// Превышение gateway.max_buffered_mb → 503 runpilot_gateway_busy.
	bufMu    sync.Mutex
	buffered int64

	// Липкие аренды API-клиентов (сервер удерживается за клиентом
	// sticky_idle_sec; в памяти, не переживает рестарт). stickyTimers —
	// таймер снятия аренды по истечении sticky_idle_sec (клиент в простое).
	stickyMu     sync.Mutex
	sticky       map[string]stickyLeaseRec
	stickyTimers map[string]*time.Timer
}

// FaultReporter — приём отказа класса OOM/ENGINE_DEAD (раздел 7.1 С4/С5):
// наблюдатель ведёт окно отказов и при пороге переводит сервер в карантин.
type FaultReporter interface {
	ReportFault(name string, fc model.FaultClass)
}

// SetFaultReporter — наблюдатель отказов (монитор); nil — без учёта.
func (g *Gateway) SetFaultReporter(r FaultReporter) {
	g.fr.Lock()
	g.faultReporter = r
	g.fr.Unlock()
}

// reportFault — уведомить наблюдателя отказом (вне блокировок прокси).
func (g *Gateway) reportFault(name string, fc model.FaultClass) {
	g.fr.Lock()
	r := g.faultReporter
	g.fr.Unlock()
	if r != nil {
		r.ReportFault(name, fc)
	}
}

// SetRequestHook — хук начала/конца проксируемого генерирующего запроса
// (для планировщика Э4; started: true — запрос начался, false — закончен).
func (g *Gateway) SetRequestHook(fn func(sid string, started bool)) {
	g.hm.Lock()
	defer g.hm.Unlock()
	g.hookReq = fn
}

func (g *Gateway) reqHook(sid string, started bool) {
	g.hm.Lock()
	fn := g.hookReq
	g.hm.Unlock()
	if fn != nil {
		fn(sid, started)
	}
}

// NewGateway — сборка шлюза. Таймаут внеочередной проверки /health —
// monitor.health_timeout_sec из конфигурации.
func NewGateway(cfg *config.Config, st *store.Store, clk clock.Clock, log *slog.Logger) (*Gateway, error) {
	servers, err := NewServers(cfg.Servers,
		time.Duration(cfg.Monitor.HealthTimeoutSec)*time.Second)
	if err != nil {
		return nil, err
	}
	g := &Gateway{
		cfg:         cfg,
		st:          st,
		clk:         clk,
		log:         log,
		servers:     servers,
		proxies:     map[string]*httputil.ReverseProxy{},
		transports:  map[string]*http.Transport{},
		waiters:     map[chan struct{}]struct{}{},
		bySID:       map[string]int{},
		byServer:    map[string]int{},
		pendingGone: map[string]string{},
		inflightCtx: map[int]context.CancelCauseFunc{},
		inflightSID: map[int]string{},
		lastEnd:     map[int64]time.Time{},
		metrics:      NewMetrics(),
		heldCount:    map[string]int{},
		sticky:       map[string]stickyLeaseRec{},
		stickyTimers: map[string]*time.Timer{},
	}
	return g, nil
}

// Servers — реестр серверов (для планировщика и монитора, Э4/Э6).
func (g *Gateway) Servers() *Servers { return g.servers }

// MetricsHandler — обработчик /metrics (раздел 14 ТЗ); монтируется на API-порт.
func (g *Gateway) MetricsHandler() http.Handler { return g.metrics.Handler() }

// Handler — HTTP-обработчик шлюза (gateway_port).
func (g *Gateway) Handler() http.Handler {
	return http.HandlerFunc(g.serveHTTP)
}

// serveHTTP — разбор пути /s/<sid>/..., доступ, маршрутизация.
func (g *Gateway) serveHTTP(w http.ResponseWriter, r *http.Request) {
	// API-клиенты (безсессионные OpenAI-запросы): POST /v1/chat/completions
	// с Bearer-ключом (не /s/<sid>/).
	if r.Method == http.MethodPost && r.URL.Path == "/v1/chat/completions" {
		g.handleAPIRequest(w, r)
		return
	}
	rest, sid := parseSPath(r.URL.Path)
	if rest == "" || sid == "" {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	sess, err := g.st.GetSession(sid)
	if err != nil || sess.State == model.SessionGone {
		// Неизвестный (или GONE) sid — 404 (раздел 6 ТЗ).
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if remoteIP(r) != sess.HostIP {
		// Чужой IP — 403 (раздел 6 ТЗ).
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if r.Method == http.MethodPost && isGenerativePath(rest) {
		g.handleGenerative(w, r, sess, rest)
		return
	}
	g.handlePassthrough(w, r, sess, rest)
}

// parseSPath — "/s/<sid>/<rest>" → (rest, sid).
func parseSPath(path string) (rest, sid string) {
	if !strings.HasPrefix(path, "/s/") {
		return "", ""
	}
	p := strings.TrimPrefix(path, "/s/")
	i := strings.IndexByte(p, '/')
	if i < 0 {
		return "", p
	}
	return p[i+1:], p[:i]
}

// remoteIP — IP источника (раздел 6 ТЗ: обязан равняться host_ip сессии).
func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// handleGenerative — генерирующий запрос (POST v1/chat/completions,
// v1/completions, v1/responses): участвует в арендах.
func (g *Gateway) handleGenerative(w http.ResponseWriter, r *http.Request, sess store.SessionRecord, rest string) {
	ctx := r.Context()
	now := g.clk.Now()

	// W7 (6.2/6.4): режим координатора. EMERGENCY — любой генерирующий
	// запрос 503 runpilot_emergency; SAFE_MODE — новая неявная аренда 503
	// runpilot_safe_mode, идущие (с активной арендой) обслуживаются.
	if m, _ := g.st.Mode(); m == string(model.ModeEmergency) {
		g.writeMode503(w, "runpilot_emergency", "runpilot: аварийная остановка, генерация отменена")
		g.metrics.RecordRequest("", "503")
		return
	}
	if m, _ := g.st.Mode(); m == string(model.ModeSafeMode) {
		if _, err := g.st.LeaseGet(sess.SID); err != nil {
			g.writeMode503(w, "runpilot_safe_mode", "runpilot: безопасный режим, новых аренд нет")
			g.metrics.RecordRequest("", "503")
			return
		}
	}

	// Ёмкость: превышение max_inflight → 503 runpilot_no_slot без удержания
	// (раздел 6 ТЗ).
	if g.tryBumpTotalInflight() {
		g.writeNoSlot(w, g.queuePositionOrOne(sess.SID))
		g.metrics.RecordRequest("", "503")
		return
	}
	defer g.unBumpTotalInflight()

	// W7 (6.2): контекст запроса регистрируется — аварийная остановка
	// отменяет все идущие (CancelAll), а GONE сессии — CancelSID.
	// WithCancelCause: внутренняя отмена передаёт причину (CancelSID/CancelAll),
	// клиентский обрыв — parent (r.Context()) завершается первым.
	pctx, cancelCause := context.WithCancelCause(ctx)
	id := g.registerInflight(sess.SID, cancelCause)
	defer g.unregisterInflight(id)
	r = r.Clone(pctx)
	ctx = pctx

	// Тело целиком, лимит max_body_mb (раздел 6 ТЗ → 413).
	limit := g.cfg.Gateway.MaxBodyBytes()
	r.Body = http.MaxBytesReader(w, r.Body, limit+1)
	raw, err := io.ReadAll(r.Body)
	_ = r.Body.Close()
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			size := r.ContentLength
			if size <= limit {
				size = limit + 1
			}
			g.writeBodyTooLarge(w, size)
			g.metrics.RecordRequest("", strconv.Itoa(http.StatusRequestEntityTooLarge))
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		g.metrics.RecordRequest("", strconv.Itoa(http.StatusBadRequest))
		return
	}

	// W15 этап 1: сумма тел в памяти (gateway.max_buffered_mb) → 503
	// runpilot_gateway_busy. Удержание не создаётся; память тела
	// освобождаем при выходе из обработчика (defer).
	if !g.tryBumpBuffered(int64(len(raw))) {
		g.writeGatewayBusy(w)
		g.metrics.RecordRequest("", strconv.Itoa(http.StatusServiceUnavailable))
		return
	}
	defer g.unBumpBuffered(int64(len(raw)))

	// П.3: debug-лог параметров запроса (без содержимого, правило 7).
	g.logRequestParams(sess.SID, raw)

	// Разрешение аренды (раздел 6 ТЗ, правила 1–4).
	decision, err := g.resolvePass(ctx, sess)
	if err != nil {
		if errors.Is(err, errHoldTimeout) {
			g.writeNoSlot(w, g.queuePositionOrOne(sess.SID))
			g.metrics.RecordRequest("", "503")
			return
		}
		if ctx.Err() != nil {
			g.metrics.RecordRequest("", "0")
			return
		}
		g.log.Error("gateway: разрешение аренды", "sid", sess.SID, "err", err)
		http.Error(w, "bad gateway", http.StatusBadGateway)
		g.metrics.RecordRequest("", "502")
		return
	}
	srvName := decision.server.Cfg.Name
	g.bumpInflight(sess.SID, srvName)
	defer g.unBumpInflight(sess.SID, srvName)

	// Учёт запроса для планировщика: запрос начался (аренда разрешена)
	// и закончится при выходе из обработчика.
	g.reqHook(sess.SID, true)
	defer g.reqHook(sess.SID, false)

	// Попытка проксирования; один повтор через миграцию (раздел 6 ТЗ).
	// Тело подменяется под сервер каждой попытки — в serveAttempt.
	g.serveAttempt(w, r, ctx, sess, rest, raw, decision, now)
}

// handlePassthrough — прочие пути после /s/<sid>/: сервер аренды,
// без аренды — UP-сервер с наибольшим priority; удержания нет.
func (g *Gateway) handlePassthrough(w http.ResponseWriter, r *http.Request, sess store.SessionRecord, rest string) {
	var srv *Server
	if lease, err := g.st.LeaseGet(sess.SID); err == nil {
		srv = g.servers.ByName(lease.Server)
	}
	if srv == nil {
		for _, c := range g.servers.All() {
			if c.State() == model.ServerUp {
				srv = c
				break
			}
		}
	}
	if srv == nil {
		g.writeNoSlot(w, 1)
		return
	}
	req := r.Clone(r.Context())
	req = withSID(req, sess.SID)
	req.URL.Path = rest
	// Прямой writer: удержание и миграция для прочих путей не действуют.
	g.proxyFor(srv).ServeHTTP(w, req)
}

// queuePositionOrOne — позиция в очереди для тела runpilot_no_slot.
func (g *Gateway) queuePositionOrOne(sid string) int {
	pos, err := g.st.QueuePosition(sid)
	if err != nil || pos < 1 {
		return 1
	}
	return pos
}

// writeNoSlot — ответ об отсутствии слота (раздел 6 ТЗ, тело побайтно).
func (g *Gateway) writeNoSlot(w http.ResponseWriter, pos int) {
	w.Header().Set("Retry-After", strconv.Itoa(g.cfg.Gateway.RetryAfterSec))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusServiceUnavailable)
	fmt.Fprintf(w, `{"error":{"message":"runpilot: no free slot, queue position %d","type":"server_error","code":"runpilot_no_slot"}}`, pos)
}

// writeMode503 — отказ по режиму координатора (W7 6.2/6.4): 503 +
// Retry-After + код runpilot_emergency / runpilot_safe_mode (тело формата раздела 6).
func (g *Gateway) writeMode503(w http.ResponseWriter, code, message string) {
	w.Header().Set("Retry-After", strconv.Itoa(g.cfg.Gateway.RetryAfterSec))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusServiceUnavailable)
	fmt.Fprintf(w, `{"error":{"message":"%s","type":"server_error","code":"%s"}}`, message, code)
}

// writeBodyTooLarge — 413 (W15 этап 1): тело runpilot_body_too_large, побайтно.
// sizeBytes — фактический размер тела (Content-Length).
func (g *Gateway) writeBodyTooLarge(w http.ResponseWriter, sizeBytes int64) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusRequestEntityTooLarge)
	_, _ = io.WriteString(w, g.cfg.Gateway.BodyTooLargeMessage(sizeBytes))
}

// writeGatewayBusy — 503 (W15 этап 1): переполнение gateway.max_buffered_mb.
// Retry-After = gateway.retry_after_sec; код runpilot_gateway_busy.
func (g *Gateway) writeGatewayBusy(w http.ResponseWriter) {
	w.Header().Set("Retry-After", strconv.Itoa(g.cfg.Gateway.RetryAfterSec))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = fmt.Fprintf(w, `{"error":{"message":"runpilot: gateway buffered limit exceeded","type":"server_error","code":"runpilot_gateway_busy"}}`)
}

// logRequestParams — П.3: debug-лог параметров запроса (без содержимого,
// правило 7): stream, max_tokens, число tools и число messages.
func (g *Gateway) logRequestParams(sid string, body []byte) {
	var req struct {
		Stream    bool `json:"stream"`
		MaxTokens int  `json:"max_tokens"`
		Tools     []any `json:"tools"`
		Messages  []any `json:"messages"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		g.log.Debug("gateway: разбор параметров запроса", "sid", sid, "err", err)
		return
	}
	g.log.Debug("gateway: параметры запроса",
		"sid", sid,
		"stream", req.Stream,
		"max_tokens", req.MaxTokens,
		"tools", len(req.Tools),
		"messages", len(req.Messages),
	)
}

// registerInflight — зарегистрировать контекст идущего запроса (W7 6.2).
func (g *Gateway) registerInflight(sid string, cancel context.CancelCauseFunc) int {
	id := atomic.AddInt64(&g.icSeq, 1)
	g.ic.Lock()
	g.inflightCtx[int(id)] = cancel
	g.inflightSID[int(id)] = sid
	g.ic.Unlock()
	return int(id)
}

// unregisterInflight — снять контекст идущего запроса.
func (g *Gateway) unregisterInflight(id int) {
	g.ic.Lock()
	delete(g.inflightCtx, id)
	delete(g.inflightSID, id)
	g.ic.Unlock()
}

// internalCancel — причина внутренней отмены запроса (CancelSID/CancelAll).
// Дистинкт от клиентского обрыва (r.Context() завершается первым):
// resultError проверяет context.Cause — *internalCancel → причина, иначе CLIENT_GONE.
type internalCancel struct{ reason string }

func (c *internalCancel) Error() string { return "internal_cancel:" + c.reason }

// CancelAll — отменить все идущие генерирующие запросы (W7 6.2): апстримы
// прекращают генерацию (время отмены — в пределах таймаутов транспорта).
func (g *Gateway) CancelAll() {
	g.ic.Lock()
	cancels := make([]context.CancelCauseFunc, 0, len(g.inflightCtx))
	for _, c := range g.inflightCtx {
		cancels = append(cancels, c)
	}
	g.inflightCtx = map[int]context.CancelCauseFunc{}
	g.inflightSID = map[int]string{}
	g.ic.Unlock()
	for _, c := range cancels {
		c(&internalCancel{reason: "CancelAll"})
	}
}

// CancelSID — отменить все идущие запросы сессии sid (сцена GONE: кодер убит
// / сессия завершена): апстримы прекращают генерацию, обработчики выходят
// (unBumpInflight снимает аренду, когда inflight(sid) дойдёт до 0).
func (g *Gateway) CancelSID(sid string) {
	g.ic.Lock()
	var cancels []context.CancelCauseFunc
	for id, c := range g.inflightCtx {
		if g.inflightSID[id] == sid {
			cancels = append(cancels, c)
		}
	}
	g.ic.Unlock()
	for _, c := range cancels {
		c(&internalCancel{reason: "CancelSID"})
	}
}

// --- inflight-счётчики (раздел 6 ТЗ) ---

// tryBumpTotalInflight — true, если лимит max_inflight уже достигнут.
func (g *Gateway) tryBumpTotalInflight() bool {
	g.im.Lock()
	defer g.im.Unlock()
	if g.total >= g.cfg.Gateway.MaxInflight {
		return true
	}
	g.total++
	return false
}

func (g *Gateway) unBumpTotalInflight() {
	g.im.Lock()
	g.total--
	g.im.Unlock()
}

// tryBumpBuffered — учесть тело в памяти; false, если сумма тел превысила
// gateway.max_buffered_mb (W15 этап 1). n = 0 — без учёта (лимит не задан).
func (g *Gateway) tryBumpBuffered(n int64) bool {
	g.bufMu.Lock()
	defer g.bufMu.Unlock()
	if g.cfg.Gateway.MaxBufferedMB > 0 && g.buffered+n > g.cfg.Gateway.MaxBufferedBytes() {
		return false
	}
	g.buffered += n
	return true
}

func (g *Gateway) unBumpBuffered(n int64) {
	g.bufMu.Lock()
	g.buffered -= n
	g.bufMu.Unlock()
}

// BufferedBytes — текущая сумма тел в памяти (диагностика/тесты).
func (g *Gateway) BufferedBytes() int64 {
	g.bufMu.Lock()
	defer g.bufMu.Unlock()
	return g.buffered
}

func (g *Gateway) bumpInflight(sid, server string) {
	g.im.Lock()
	g.bySID[sid]++
	g.byServer[server]++
	g.im.Unlock()
}

func (g *Gateway) unBumpInflight(sid, server string) {
	g.im.Lock()
	g.bySID[sid]--
	reason := ""
	releaseNow := false
	if g.bySID[sid] <= 0 {
		delete(g.bySID, sid)
		if r, ok := g.pendingGone[sid]; ok {
			// Сессия GONE и последний запрос завершён (t_end записан) —
			// теперь безопас снять аренду.
			delete(g.pendingGone, sid)
			reason, releaseNow = r, true
		}
	}
	g.byServer[server]--
	if g.byServer[server] <= 0 {
		delete(g.byServer, server)
	}
	g.im.Unlock()
	if releaseNow {
		g.releaseAndWake(sid, reason)
	}
}

// --- ждущие слота ---

// subscribeWake — канал пробуждения (буфер 1: сигнал не теряется —
// проверка слота предшествует select).
func (g *Gateway) subscribeWake() chan struct{} {
	ch := make(chan struct{}, 1)
	g.wm.Lock()
	g.waiters[ch] = struct{}{}
	g.wm.Unlock()
	return ch
}

func (g *Gateway) unsubscribeWake(ch chan struct{}) {
	g.wm.Lock()
	delete(g.waiters, ch)
	g.wm.Unlock()
}

// wake — разбудить всех ждущих (освобождение аренды).
func (g *Gateway) wake() {
	g.wm.Lock()
	defer g.wm.Unlock()
	for ch := range g.waiters {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// Release — освободить аренду сессии (тесты, оператор, планировщик Э4).
func (g *Gateway) Release(sid, reason string) {
	if err := g.st.LeaseRelease(sid, reason, g.clk.Now()); err == nil {
		g.wake()
	}
}

// releaseAndWake — снять аренду + разбудить ждущих (вне блокировок im).
func (g *Gateway) releaseAndWake(sid, reason string) {
	if err := g.st.LeaseRelease(sid, reason, g.clk.Now()); err == nil {
		g.wake()
	}
}

// ReleaseOnGone — сессия GONE (любая причина): отменить идущие запросы sid и
// снять аренду, но ТОЛЬКО после того, как inflight(sid)=0 (запрос завершён,
// t_end записан) — иначе слот выдался бы, пока запрос убитой сессии ещё летит
// в апстрим. Если запросов уже нет — снять сразу.
func (g *Gateway) ReleaseOnGone(sid, reason string) {
	g.CancelSID(sid)
	g.im.Lock()
	if g.bySID[sid] <= 0 {
		g.im.Unlock()
		g.releaseAndWake(sid, reason)
		return
	}
	g.pendingGone[sid] = reason
	g.im.Unlock()
}

// Wake — разбудить ждущих слота (удержание): вызывается планировщиком,
// когда он выдал аренду или снял её.
func (g *Gateway) Wake() { g.wake() }

// setLastRequestEnd — lease.last_request_end (раздел 6 ТЗ).
func (g *Gateway) setLastRequestEnd(leaseID int64, at time.Time) {
	g.lm.Lock()
	g.lastEnd[leaseID] = at
	g.lm.Unlock()
}

// LastRequestEnd — время последнего запроса аренды (для планировщика Э4).
func (g *Gateway) LastRequestEnd(leaseID int64) (time.Time, bool) {
	g.lm.Lock()
	defer g.lm.Unlock()
	t, ok := g.lastEnd[leaseID]
	return t, ok
}

// InflightByServer — счётчик inflight сервера (диагностика, Э4/Э5).
func (g *Gateway) InflightByServer(server string) int {
	g.im.Lock()
	defer g.im.Unlock()
	return g.byServer[server]
}
