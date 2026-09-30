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

	// Счётчики inflight сессии и сервера (раздел 6 ТЗ).
	im       sync.Mutex
	total    int
	bySID    map[string]int
	byServer map[string]int

	// W7 (6.2): контексты идущих генерирующих запросов — аварийная
	// остановка отменяет все (CancelAll): vLLM прекращает генерацию.
	ic          sync.Mutex
	inflightCtx map[int]context.CancelFunc
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
		inflightCtx: map[int]context.CancelFunc{},
		lastEnd:     map[int64]time.Time{},
	}
	return g, nil
}

// Servers — реестр серверов (для планировщика и монитора, Э4/Э6).
func (g *Gateway) Servers() *Servers { return g.servers }

// Handler — HTTP-обработчик шлюза (gateway_port).
func (g *Gateway) Handler() http.Handler {
	return http.HandlerFunc(g.serveHTTP)
}

// serveHTTP — разбор пути /s/<sid>/..., доступ, маршрутизация.
func (g *Gateway) serveHTTP(w http.ResponseWriter, r *http.Request) {
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
		return
	}
	if m, _ := g.st.Mode(); m == string(model.ModeSafeMode) {
		if _, err := g.st.LeaseGet(sess.SID); err != nil {
			g.writeMode503(w, "runpilot_safe_mode", "runpilot: безопасный режим, новых аренд нет")
			return
		}
	}

	// Ёмкость: превышение max_inflight → 503 runpilot_no_slot без удержания
	// (раздел 6 ТЗ).
	if g.tryBumpTotalInflight() {
		g.writeNoSlot(w, g.queuePositionOrOne(sess.SID))
		return
	}
	defer g.unBumpTotalInflight()

	// W7 (6.2): контекст запроса регистрируется — аварийная остановка
	// отменяет все идущие (CancelAll): апстримы прекращают генерацию.
	pctx, cancel := context.WithCancel(ctx)
	id := g.registerInflight(cancel)
	defer g.unregisterInflight(id)
	r = r.Clone(pctx)
	ctx = pctx

	// Тело целиком, лимит max_body_mb (раздел 6 ТЗ → 413).
	limit := g.cfg.Gateway.MaxBodyBytes()
	r.Body = http.MaxBytesReader(w, r.Body, limit+1)
	raw, err := io.ReadAll(r.Body)
	_ = r.Body.Close()
	if err != nil {
		status := http.StatusBadRequest
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			status = http.StatusRequestEntityTooLarge
		}
		http.Error(w, err.Error(), status)
		return
	}

	// Разрешение аренды (раздел 6 ТЗ, правила 1–4).
	decision, err := g.resolvePass(ctx, sess)
	if err != nil {
		if errors.Is(err, errHoldTimeout) {
			g.writeNoSlot(w, g.queuePositionOrOne(sess.SID))
			return
		}
		if ctx.Err() != nil {
			return
		}
		g.log.Error("gateway: разрешение аренды", "sid", sess.SID, "err", err)
		http.Error(w, "bad gateway", http.StatusBadGateway)
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

// registerInflight — зарегистрировать контекст идущего запроса (W7 6.2).
func (g *Gateway) registerInflight(cancel context.CancelFunc) int {
	id := atomic.AddInt64(&g.icSeq, 1)
	g.ic.Lock()
	g.inflightCtx[int(id)] = cancel
	g.ic.Unlock()
	return int(id)
}

// unregisterInflight — снять контекст идущего запроса.
func (g *Gateway) unregisterInflight(id int) {
	g.ic.Lock()
	delete(g.inflightCtx, id)
	g.ic.Unlock()
}

// CancelAll — отменить все идущие генерирующие запросы (W7 6.2): апстримы
// прекращают генерацию (время отмены — в пределах таймаутов транспорта).
func (g *Gateway) CancelAll() {
	g.ic.Lock()
	cancels := make([]context.CancelFunc, 0, len(g.inflightCtx))
	for _, c := range g.inflightCtx {
		cancels = append(cancels, c)
	}
	g.inflightCtx = map[int]context.CancelFunc{}
	g.ic.Unlock()
	for _, c := range cancels {
		c()
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

func (g *Gateway) bumpInflight(sid, server string) {
	g.im.Lock()
	g.bySID[sid]++
	g.byServer[server]++
	g.im.Unlock()
}

func (g *Gateway) unBumpInflight(sid, server string) {
	g.im.Lock()
	g.bySID[sid]--
	if g.bySID[sid] <= 0 {
		delete(g.bySID, sid)
	}
	g.byServer[server]--
	if g.byServer[server] <= 0 {
		delete(g.byServer, server)
	}
	g.im.Unlock()
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
