package monitor

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"runpilot/internal/clock"
	"runpilot/internal/config"
	"runpilot/internal/model"
)

// tickPoll — период опроса дедлайнов в Run (не порог ТЗ).
const tickPoll = tickPollMS * time.Millisecond

// Fetcher — GET url → (body, status, err). В тестах — без сети.
type Fetcher func(ctx context.Context, url string) ([]byte, int, error)

// HTTPFetcher — продакшн-Fetcher; таймаут даёт вызывающий через ctx.
func HTTPFetcher() Fetcher {
	c := &http.Client{}
	return func(ctx context.Context, url string) ([]byte, int, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, 0, err
		}
		resp, err := c.Do(req)
		if err != nil {
			return nil, 0, err
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		return b, resp.StatusCode, err
	}
}

// ExternalSetter — scheduler.SetExternal.
type ExternalSetter interface{ SetExternal(name string, ext int) }

// ServerStateSink — gateway.Servers: переходы состояния по health-серии.
type ServerStateSink interface {
	SetState(name string, st model.ServerState)
}

// InflightCounter — gateway: открытые запросы шлюза к серверу.
type InflightCounter interface{ InflightByServer(server string) int }

// rateSample — точка счётчиков для rate-окна.
type rateSample struct {
	ts     time.Time
	gen    float64
	prompt float64
}

// rateSeries — окно monitor.rate_window_sec для tok/s (раздел 10 ТЗ):
// индикация скорости генерации, не учёт потребления.
type rateSeries struct {
	window  time.Duration
	samples []rateSample
}

// Push — новая точка счётчиков; старые за окном выкидываются.
func (r *rateSeries) Push(ts time.Time, gen, prompt float64) {
	r.samples = append(r.samples, rateSample{ts: ts, gen: gen, prompt: prompt})
	cut := ts.Add(-r.window)
	for len(r.samples) > 1 && r.samples[1].ts.Before(cut) {
		r.samples = r.samples[1:]
	}
}

// Rate — tok/s за окно: (последняя − первая)/Δt; nil, если точек < 2.
func (r *rateSeries) Rate() (gen, prompt *float64) {
	if len(r.samples) < minRateSamples {
		return nil, nil
	}
	first, last := r.samples[0], r.samples[len(r.samples)-1]
	sec := last.ts.Sub(first.ts).Seconds()
	if sec <= 0 {
		return nil, nil
	}
	g := (last.gen - first.gen) / sec
	p := (last.prompt - first.prompt) / sec
	return &g, &p
}

// reset — после рестарта сервера счётчики обнуляются (health DOWN).
func (r *rateSeries) reset() { r.samples = nil }

// HistoryPoint — точка спарклайна (шаг = интервал метрик).
type HistoryPoint struct {
	TS      time.Time
	Running int
	Waiting int
	KV      *float64
	GenTokS *float64
	Ext     int
	Missing bool
}

// History — окно monitor.history_window_min (раздел 10 ТЗ), в памяти.
type History struct {
	window time.Duration
	points []HistoryPoint
}

// NewHistory — пустое окно.
func NewHistory(window time.Duration) History {
	return History{window: window}
}

// Push — точка; старше окна выкидывается.
func (h *History) Push(p HistoryPoint) {
	h.points = append(h.points, p)
	cut := p.TS.Add(-h.window)
	i := 0
	for i < len(h.points)-1 && h.points[i].TS.Before(cut) {
		i++
	}
	if i > 0 {
		h.points = h.points[i:]
	}
}

// Points — копия точек.
func (h *History) Points() []HistoryPoint {
	out := make([]HistoryPoint, len(h.points))
	copy(out, h.points)
	return out
}

// Sample — свежий срез сервера (для ServerView; nil = «—»).
type Sample struct {
	Running    *int
	Waiting    *int
	KV         *float64
	GenTokS    *float64
	PromptTokS *float64
	Ext        *int // nil = metrics_missing
	Missing    bool // metrics_missing (раздел 5 ТЗ)
}

// serverState — runtime-состояние одного сервера (в памяти).
type serverState struct {
	mu         sync.Mutex
	health     model.ServerHealth
	nextHealth time.Time
	nextMetric time.Time
	nextModel  time.Time
	latest     *VLLMMetrics
	rates      rateSeries
	hist       History
	missing    bool
	ext        int
	// v2 (S4/S5): окно отказов (OOM/ENGINE_DEAD) + карантин.
	faults       []time.Time // отказы в faults.window_sec (включая текущий)
	lastFC       model.FaultClass
	quarantUntil time.Time // zero = не в карантине
	// v2 (S3): нестабильность — переходы UP↔DOWN в faults.flap_window_sec.
	flaps []time.Time
}

// ModelObserver — v2 (S7/S8): наблюдение моделей (GET /v1/models) раз в
// monitor.models_refresh_sec и при переходе в UP. Монитор передаёт список
// доступных моделей; сравнение с настроенной — за наблюдателем (шлюз).
type ModelObserver interface {
	OnModels(name string, models []string)
}

// EventSink — v2 (S4/S5): запись события SERVER_FAULT (журнал) при переходе
// сервера в карантин. Реализация — обёртка над store.EventRecord.
type EventSink interface {
	RecordServerFault(name string, fc model.FaultClass, count int)
}

// Monitor — мониторинг серверов (разделы 5/10 ТЗ).
type Monitor struct {
	cfg   *config.Config
	clk   clock.Clock
	fetch Fetcher
	ext   ExternalSetter
	sink  ServerStateSink
	infl  InflightCounter
	log   *slog.Logger

	cfgBy map[string]config.Server
	names []string
	srvs  map[string]*serverState

	// modelSink — v2 (S7/S8): наблюдатель моделей (OnModels). nil — без
	// наблюдения моделей.
	modelSink ModelObserver
	// eventSink — v2 (S4/S5): запись SERVER_FAULT при переходе в карантин.
	// nil — без записи события.
	eventSink EventSink

	// K4: место на диске БД — проверка раз в monitor.disk_check_sec.
	diskMu       sync.Mutex
	diskPath     string
	freeSpace    func(string) (int64, error) // nil — проверка выключена
	diskNext     time.Time
	lastDiskFree int64
}

// SetModelObserver — v2 (S7/S8): наблюдатель моделей (вызывается на каждом
// успешном GET /v1/models).
func (m *Monitor) SetModelObserver(o ModelObserver) { m.modelSink = o }

// SetEventSink — v2 (S4/S5): запись SERVER_FAULT (журнал) при карантине.
func (m *Monitor) SetEventSink(s EventSink) { m.eventSink = s }

// New — монитор из конфигурации. Часы — только clk (строгое правило ТЗ).
func New(cfg *config.Config, clk clock.Clock, fetch Fetcher,
	ext ExternalSetter, sink ServerStateSink, infl InflightCounter, log *slog.Logger) *Monitor {
	m := &Monitor{
		cfg:   cfg,
		clk:   clk,
		fetch: fetch,
		ext:   ext,
		sink:  sink,
		infl:  infl,
		log:   log,
		cfgBy: map[string]config.Server{},
		srvs:  map[string]*serverState{},
	}
	now := clk.Now()
	for _, s := range cfg.Servers {
		m.cfgBy[s.Name] = s
		m.names = append(m.names, s.Name)
		m.srvs[s.Name] = &serverState{
			health:     model.NewServerHealth(),
			nextHealth: now,
			nextMetric: now,
			rates:      rateSeries{window: m.rateWindow()},
			hist:       NewHistory(m.historyWindow()),
		}
	}
	sort.Strings(m.names)
	return m
}

func (m *Monitor) healthInterval() time.Duration {
	return time.Duration(m.cfg.Monitor.HealthIntervalSec) * time.Second
}

func (m *Monitor) metricsInterval() time.Duration {
	return time.Duration(m.cfg.Monitor.MetricsIntervalSec) * time.Second
}

func (m *Monitor) modelsInterval() time.Duration {
	return time.Duration(m.cfg.Monitor.ModelsRefreshSec) * time.Second
}

func (m *Monitor) rateWindow() time.Duration {
	return time.Duration(m.cfg.Monitor.RateWindowSec) * time.Second
}

func (m *Monitor) historyWindow() time.Duration {
	return time.Duration(m.cfg.Monitor.HistoryWindowMin) * time.Minute
}

// Run — фоновый цикл (продакшн).
func (m *Monitor) Run(ctx context.Context) {
	t := time.NewTicker(tickPoll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.Tick()
		}
	}
}

// Tick — один проход по серверам (Run и тесты на виртуальных часах).
// Дедлайны планируются ДО сетевых вызовов.
func (m *Monitor) Tick() {
	// K4: место на диске БД (каждые monitor.disk_check_sec).
	m.tickDisk()
	// v2 (S4/S5): истечение карантина (QUARANTINED → состояние здоровья).
	for _, name := range m.names {
		m.tickQuarantine(name)
	}
	type job struct {
		name     string
		doHealth bool
		doMetric bool
		doModel  bool
	}
	var jobs []job
	for _, name := range m.names {
		ss := m.srvs[name]
		ss.mu.Lock()
		now := m.clk.Now()
		j := job{name: name}
		if !now.Before(ss.nextHealth) {
			j.doHealth = true
			ss.nextHealth = now.Add(m.healthInterval())
		}
		if !now.Before(ss.nextMetric) {
			j.doMetric = true
			ss.nextMetric = now.Add(m.metricsInterval())
		}
		if m.modelSink != nil && !now.Before(ss.nextModel) {
			j.doModel = true
			ss.nextModel = now.Add(m.modelsInterval())
		}
		ss.mu.Unlock()
		if j.doHealth || j.doMetric || j.doModel {
			jobs = append(jobs, j)
		}
	}
	for _, j := range jobs {
		if j.doHealth {
			m.checkHealth(j.name)
		}
		if j.doMetric {
			m.fetchMetrics(j.name)
		}
		if j.doModel {
			m.checkModels(j.name)
		}
	}
}

// checkHealth — GET health_url (раздел 10 ТЗ): 200 = успех; серии
// down_after/up_after → переходы состояния (наблюдатели шлюза).
func (m *Monitor) checkHealth(name string) {
	sc := m.cfgBy[name]
	ss := m.srvs[name]
	ctx, cancel := context.WithTimeout(context.Background(),
		time.Duration(m.cfg.Monitor.HealthTimeoutSec)*time.Second)
	defer cancel()
	_, status, err := m.fetch(ctx, sc.HealthURL)
	healthy := err == nil && status == http.StatusOK

	ss.mu.Lock()
	newHealth, changed := ss.health.Observe(healthy, m.cfg.Monitor.HealthDownAfter, m.cfg.Monitor.HealthUpAfter)
	ss.health = newHealth
	ss.mu.Unlock()
	if changed {
		m.sink.SetState(name, newHealth.State)
		if newHealth.State == model.ServerDown {
			ss.mu.Lock()
			ss.rates.reset() // счётчики vLLM обнуляются после рестарта
			ss.mu.Unlock()
		}
		m.observeFlap(name) // S3: нестабильность → карантин
	}
}

// observeFlap — v2 (S3): переход UP↔DOWN учтён; ≥ faults.flap_count за
// faults.flap_window_sec → карантин на faults.quarantine_sec.
func (m *Monitor) observeFlap(name string) {
	ss, ok := m.srvs[name]
	if !ok {
		return
	}
	now := m.clk.Now()
	window := time.Duration(m.cfg.Faults.FlapWindowSec) * time.Second
	ss.mu.Lock()
	if !ss.quarantUntil.IsZero() {
		ss.mu.Unlock()
		return // уже в карантине (отказов/нестабильности)
	}
	var kept []time.Time
	for _, ts := range ss.flaps {
		if now.Sub(ts) <= window {
			kept = append(kept, ts)
		}
	}
	kept = append(kept, now)
	ss.flaps = kept
	count := len(kept)
	enter := m.cfg.Faults.FlapCount > 0 && count >= m.cfg.Faults.FlapCount
	if enter {
		ss.quarantUntil = now.Add(m.faultQuarantine())
	}
	ss.mu.Unlock()
	if enter {
		m.sink.SetState(name, model.ServerQuarantined)
		m.log.Warn("monitor: сервер нестабилен — карантин", "server", name, "flaps", count)
	}
}

// checkModels — v2 (S7/S8): GET <upstream>/v1/models раз в
// monitor.models_refresh_sec. Список доступных моделей → ModelObserver
// (сравнение с настроенной, SERVER_MODEL_CHANGED / MODEL_PROBLEM — в
// наблюдателе). Ошибка/не-200/не-JSON — молча: модели не меняют состояние
// здоровья, а проблемы модели наблюдатель увидит по опросу.
func (m *Monitor) checkModels(name string) {
	sc := m.cfgBy[name]
	base := strings.TrimSuffix(sc.Upstreams.OpenAI.URL, "/")
	if base == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(),
		time.Duration(m.cfg.Monitor.HealthTimeoutSec)*time.Second)
	defer cancel()
	body, status, err := m.fetch(ctx, base+"/v1/models")
	if err != nil || status != http.StatusOK {
		return
	}
	var parsed struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &parsed) != nil {
		return
	}
	models := make([]string, 0, len(parsed.Data))
	for _, d := range parsed.Data {
		if d.ID != "" {
			models = append(models, d.ID)
		}
	}
	m.modelSink.OnModels(name, models)
}

// fetchMetrics — GET metrics_url (раздел 10 ТЗ): парсинг, ext =
// max(0, running + waiting − inflight_gw(s)), SetExternal. Метрики
// недоступны → ext = 0 и metrics_missing = true (не молчаливое 0:
// флаг виден doctor/TUI).
func (m *Monitor) fetchMetrics(name string) {
	sc := m.cfgBy[name]
	ss := m.srvs[name]
	now := m.clk.Now()
	ctx, cancel := context.WithTimeout(context.Background(),
		time.Duration(m.cfg.Monitor.MetricsTimeoutSec)*time.Second)
	defer cancel()
	body, status, err := m.fetch(ctx, sc.MetricsURL)

	ext := 0
	if err != nil || status != http.StatusOK {
		m.log.Warn("monitor: /metrics недоступен", "server", name,
			"status", status, "err", err)
		ss.mu.Lock()
		ss.missing = true
		ss.latest = nil
		ss.ext = 0
		ss.hist.Push(HistoryPoint{TS: now, Missing: true})
		ss.mu.Unlock()
		m.ext.SetExternal(name, 0)
		return
	}
	parsed, perr := ParseVLLMMetrics(body)
	if perr != nil {
		m.log.Warn("monitor: /metrics не разобран", "server", name, "err", perr)
		ss.mu.Lock()
		ss.missing = true
		ss.latest = nil
		ss.ext = 0
		ss.hist.Push(HistoryPoint{TS: now, Missing: true})
		ss.mu.Unlock()
		m.ext.SetExternal(name, 0)
		return
	}
	missing := parsed.Running == nil || parsed.Waiting == nil
	if !missing {
		ext = *parsed.Running + *parsed.Waiting - m.infl.InflightByServer(name)
		if ext < 0 {
			ext = 0
		}
	}

	ss.mu.Lock()
	ss.missing = missing
	ss.latest = parsed
	ss.ext = ext
	if parsed.GenTotal != nil && parsed.PromptTotal != nil {
		ss.rates.Push(now, *parsed.GenTotal, *parsed.PromptTotal)
	}
	gen, _ := ss.rates.Rate()
	ss.hist.Push(HistoryPoint{
		TS:      now,
		Running: derefInt(parsed.Running),
		Waiting: derefInt(parsed.Waiting),
		KV:      kvPct(parsed.KV),
		GenTokS: gen,
		Ext:     ext,
		Missing: missing,
	})
	ss.mu.Unlock()
	m.ext.SetExternal(name, ext)
}

// Sample — свежий срез (для ServerView).
func (m *Monitor) Sample(name string) Sample {
	ss, ok := m.srvs[name]
	if !ok {
		return Sample{Missing: true}
	}
	ss.mu.Lock()
	defer ss.mu.Unlock()
	s := Sample{Missing: ss.missing}
	if ss.latest != nil && !ss.missing {
		s.Running = ss.latest.Running
		s.Waiting = ss.latest.Waiting
		s.KV = kvPct(ss.latest.KV)
		gen, prompt := ss.rates.Rate()
		s.GenTokS = gen
		s.PromptTokS = prompt
	}
	if !ss.missing {
		e := ss.ext
		s.Ext = &e
	}
	return s
}

// HistoryPoints — окно истории сервера (спарклайны, раздел 10 ТЗ).
func (m *Monitor) HistoryPoints(name string) []HistoryPoint {
	ss, ok := m.srvs[name]
	if !ok {
		return nil
	}
	ss.mu.Lock()
	defer ss.mu.Unlock()
	return ss.hist.Points()
}

// FoundMetrics — найденные vllm: имена последнего разбора (doctor).
func (m *Monitor) FoundMetrics(name string) []string {
	ss, ok := m.srvs[name]
	if !ok {
		return nil
	}
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if ss.latest == nil {
		return nil
	}
	out := make([]string, len(ss.latest.Found))
	copy(out, ss.latest.Found)
	return out
}

// LastMetrics — последний разобранный ответ (doctor, /api/v1/doctor).
func (m *Monitor) LastMetrics(name string) (*VLLMMetrics, bool) {
	ss, ok := m.srvs[name]
	if !ok {
		return nil, false
	}
	ss.mu.Lock()
	defer ss.mu.Unlock()
	return ss.latest, ss.latest != nil
}

func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

// kvPct — доля vllm:kv_cache_usage_perc (0..1) → проценты для отображения
// (API-поле kv_pct).
func kvPct(f *float64) *float64 {
	if f == nil {
		return nil
	}
	p := *f * percentWhole
	return &p
}
