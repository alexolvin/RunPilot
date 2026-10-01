package monitor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"runpilot/internal/clock"
	"runpilot/internal/config"
	"runpilot/internal/model"
	"runpilot/internal/proto"
	"runpilot/internal/scheduler"
	"runpilot/internal/store"
)

// --- фэйки ---

// fakeFetcher — управляемые /health и /metrics (маршрутизация по URL).
type fakeFetcher struct {
	healthStatus  int
	metricsStatus int
	metricsBody   string
	metricsErr    error
	modelsStatus  int
	modelsBody    string
	lastBearer    string // последний bearer (проверка аутентификации /v1/models)
}

func (f *fakeFetcher) get(ctx context.Context, url string, bearer string) ([]byte, int, error) {
	f.lastBearer = bearer
	if strings.HasSuffix(url, "/health") {
		return []byte("ok"), f.healthStatus, nil
	}
	if strings.HasSuffix(url, "/v1/models") {
		return []byte(f.modelsBody), f.modelsStatus, nil
	}
	if f.metricsErr != nil {
		return nil, 0, f.metricsErr
	}
	return []byte(f.metricsBody), f.metricsStatus, nil
}

// fakeInfl — фиксированный inflight шлюза.
type fakeInfl struct{ n int }

func (f fakeInfl) InflightByServer(string) int { return f.n }

// fakeSink — записи переходов состояния (health-серии).
type fakeSink struct{ changes []string }

func (f *fakeSink) SetState(name string, st model.ServerState) {
	f.changes = append(f.changes, fmt.Sprintf("%s=%s", name, st))
}

// srvList — scheduler.Servers (статичный список).
type srvList struct{ list []scheduler.ServerView }

func (f *srvList) List() []scheduler.ServerView { return f.list }

// panesSrc — scheduler.PaneSource.
type panesSrc struct {
	bySID  map[string]scheduler.PaneSnap
	byPane map[string]scheduler.PaneSnap
}

func (p *panesSrc) PaneBySID(sid string) (scheduler.PaneSnap, bool)   { s, ok := p.bySID[sid]; return s, ok }
func (p *panesSrc) PaneByPaneID(id string) (scheduler.PaneSnap, bool) { s, ok := p.byPane[id]; return s, ok }

// mHarn — стенд: планировщик + монитор на виртуальных часах.
type mHarn struct {
	t     *testing.T
	clk   *clock.Virtual
	cfg   *config.Config
	st    *store.Store
	sch   *scheduler.Scheduler
	mon   *Monitor
	fetch *fakeFetcher
	sink  *fakeSink
}

func newMHarn(t *testing.T, slots int) *mHarn {
	t.Helper()
	cfg := config.Defaults()
	cfg.Scheduler.TickMS = 100
	cfg.Scheduler.AgingSec = 60
	cfg.Scheduler.PreferWaitSec = 30
	cfg.Scheduler.PinUnavailableSec = 10
	cfg.Scheduler.AffinityTTLSec = 300
	cfg.Scheduler.ResumeBackoffSec = 5
	cfg.Scheduler.SnapshotMaxAgeSec = 1000
	cfg.Scheduler.DispatchStableSec = 2
	cfg.Scheduler.ExternalConfirmSec = 3
	cfg.Scheduler.CooldownSec = 2
	cfg.Dispatch.StartConfirmSec = 10
	cfg.Turn.DoneQuietSec = 5
	cfg.Turn.DoneStableSec = 5
	cfg.Monitor.MetricsIntervalSec = 1
	cfg.Monitor.HealthIntervalSec = 60
	cfg.Monitor.RateWindowSec = 10
	cfg.Monitor.HistoryWindowMin = 10
	cfg.Servers = []config.Server{{
		Name: "s", Priority: 10, Slots: slots,
		Accept:     []string{"resume", "high", "normal", "low"},
		HealthURL:  "http://127.0.0.1:1/health",
		MetricsURL: "http://127.0.0.1:1/metrics",
		Upstreams:  config.Upstreams{OpenAI: config.UpstreamOpenAI{URL: "http://127.0.0.1:1"}},
	}}

	clk := clock.NewVirtual(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	st, err := store.Open(filepath.Join(t.TempDir(), "runpilot.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	fetch := &fakeFetcher{healthStatus: 200, metricsStatus: 200}
	sink := &fakeSink{}
	panes := &panesSrc{bySID: map[string]scheduler.PaneSnap{}, byPane: map[string]scheduler.PaneSnap{}}
	sch := scheduler.New(scheduler.Options{
		Cfg: cfg, Store: st, Clk: clk,
		Servers: &srvList{list: []scheduler.ServerView{{
			Name: "s", Priority: 10, Slots: slots,
			Accept: []string{"resume", "high", "normal", "low"},
			State:  model.ServerUp,
		}}},
		Panes: panes,
		Dispatch: func(ctx context.Context, paneID string, m proto.Msg) (proto.Msg, error) {
			return proto.Msg{}, errors.New("dispatch отключён в тесте")
		},
		ResumeText:      "continue",
		NoAsyncDispatch: true,
		Log:             slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	mon := New(cfg, clk, fetch.get, sch, sink, fakeInfl{0},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	return &mHarn{t: t, clk: clk, cfg: cfg, st: st, sch: sch, mon: mon, fetch: fetch, sink: sink}
}

// --- помощники ---

func (h *mHarn) advance(d time.Duration) { h.clk.Advance(d) }
func (h *mHarn) tick()                   { h.sch.Tick() }

func (h *mHarn) addSession(sid string) {
	h.t.Helper()
	now := h.clk.Now()
	rec := store.SessionRecord{
		SID: sid, Name: sid, Host: "host", HostIP: "127.0.0.1",
		TmuxSession: sid, PaneID: "p-" + sid, Profile: "qwen",
		State: model.SessionQueued, StateChangedAt: now,
		Class: model.ClassNormal, CreatedAt: now,
	}
	if err := h.st.CreateSession(rec); err != nil {
		h.t.Fatalf("create session %s: %v", sid, err)
	}
}

// idlePane — панель через публичный хук планировщика (как узел).
func (h *mHarn) idlePane(sid string) {
	h.t.Helper()
	h.sch.PaneUpdate("p-"+sid, sid, model.PaneIdle, 1, h.clk.Now())
}

func (h *mHarn) enqueue(sid string) {
	h.t.Helper()
	now := h.clk.Now()
	e := model.QueueEntry{SID: sid, Class: model.ClassNormal,
		EnqueuedAt: now, NotBefore: now, Mode: model.QueueModeSubmit}
	if err := h.st.QueueUpsert(e); err != nil {
		h.t.Fatalf("enqueue %s: %v", sid, err)
	}
}

// release — ход «завершён»: аренда освобождена, запись очереди удалена.
func (h *mHarn) release(sid string) {
	h.t.Helper()
	if err := h.st.LeaseRelease(sid, "test", h.clk.Now()); err != nil {
		h.t.Fatalf("release %s: %v", sid, err)
	}
	if err := h.st.QueueDelete(sid); err != nil {
		h.t.Fatalf("queue delete %s: %v", sid, err)
	}
}

func (h *mHarn) leased(sid string) bool {
	_, err := h.st.LeaseGet(sid)
	return err == nil
}

func (h *mHarn) reason(sid string) string {
	e, err := h.st.QueueGet(sid)
	if err != nil {
		return ""
	}
	return e.IneligibleReason
}

// metricsBody — vLLM-метрики с заданными running/waiting.
func metricsBody(running, waiting int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "vllm:num_requests_running{engine=\"0\",model_name=\"m\"} %d.0\n", running)
	fmt.Fprintf(&b, "vllm:num_requests_waiting{engine=\"0\",model_name=\"m\"} %d.0\n", waiting)
	fmt.Fprintf(&b, "vllm:kv_cache_usage_perc{engine=\"0\",model_name=\"m\"} 0.5\n")
	fmt.Fprintf(&b, "vllm:generation_tokens_total{engine=\"0\",model_name=\"m\"} 100.0\n")
	fmt.Fprintf(&b, "vllm:prompt_tokens_total{engine=\"0\",model_name=\"m\"} 10.0\n")
	return b.String()
}

// --- тесты ---

// TestExternalExactlyConfirm — строка CONTROL Э6: EXTERNAL ровно через
// external_confirm_sec на виртуальных часах. ext=2 (все слоты):
//   - до confirm: оба слота свободны — две записи получают аренды;
//   - ровно confirm: все слоты EXTERNAL — запись не выдаётся,
//     reason=external (и держится: extCount не сносится,
//     регрессия externalWant);
//   - ext=0: FREE ровно через confirm (не раньше).
func TestExternalExactlyConfirm(t *testing.T) {
	h := newMHarn(t, 2)
	confirm := time.Duration(h.cfg.Scheduler.ExternalConfirmSec) * time.Second
	h.fetch.metricsBody = metricsBody(2, 0) // ext = 2 − 0 = 2

	// t0: сэмпл ext=2; записи E1/E2 с готовыми панелями (стабильность
	// накапливается с t0).
	h.mon.Tick()
	h.tick()
	h.addSession("E1")
	h.idlePane("E1")
	h.enqueue("E1")
	h.addSession("E2")
	h.idlePane("E2")
	h.enqueue("E2")

	// t0 + confirm − 0.1с: EXTERNAL ещё не начался — обе записи
	// получают аренды (2 свободных слота).
	h.advance(confirm - 100*time.Millisecond)
	h.tick()
	if !h.leased("E1") || !h.leased("E2") {
		t.Fatalf("до confirm оба слота свободны: E1=%v E2=%v reason=%q",
			h.leased("E1"), h.leased("E2"), h.reason("E1"))
	}
	h.release("E1")
	h.release("E2")

	// t0 + confirm: EXTERNAL берёт оба слота.
	h.advance(100 * time.Millisecond)
	h.tick()
	h.addSession("E3")
	h.idlePane("E3")
	h.enqueue("E3")
	h.advance(2 * time.Second) // стабильность панели
	h.tick()
	if h.leased("E3") {
		t.Fatal("при ext=2 запись E3 не должна быть выдана")
	}
	if got := h.reason("E3"); got != model.ReasonExternal {
		t.Fatalf("E3 reason=%q, хочу external", got)
	}
	// EXTERNAL держится и на следующих тиках (regression: extCount
	// не сносится, когда ext ≥ числа свободных слотов).
	h.tick()
	h.tick()
	if h.leased("E3") || h.reason("E3") != model.ReasonExternal {
		t.Fatalf("EXTERNAL не держится: leased=%v reason=%q",
			h.leased("E3"), h.reason("E3"))
	}

	// ext=0: FREE ровно через confirm (не раньше).
	h.fetch.metricsBody = metricsBody(0, 0)
	h.mon.Tick()
	h.tick()
	h.advance(confirm - 100*time.Millisecond)
	h.tick()
	if h.leased("E3") {
		t.Fatal("до confirm после ext=0 запись E3 не должна быть выдана")
	}
	h.advance(100 * time.Millisecond)
	h.tick()
	if !h.leased("E3") {
		t.Fatalf("после confirm FREE запись E3 выдана не была (reason=%q)",
			h.reason("E3"))
	}
}

// TestMetricsMissing — метрики недоступны → ext = 0 и metrics_missing =
// true (не молчаливое 0: Sample несёт флаг, история помечена, Found пусто).
func TestMetricsMissing(t *testing.T) {
	h := newMHarn(t, 1)
	h.fetch.metricsErr = errors.New("connection refused")
	h.mon.Tick()

	s := h.mon.Sample("s")
	if !s.Missing {
		t.Fatal("Missing = false, хочу true")
	}
	if s.Ext != nil {
		t.Errorf("Ext = %d, хочу nil (не молчаливое 0)", *s.Ext)
	}
	if s.Running != nil || s.KV != nil || s.GenTokS != nil {
		t.Error("значения должны отсутствовать при metrics_missing")
	}
	pts := h.mon.HistoryPoints("s")
	if len(pts) != 1 || !pts[0].Missing {
		t.Errorf("история = %+v, хочу 1 точку с Missing", pts)
	}
	if len(h.mon.FoundMetrics("s")) != 0 {
		t.Error("FoundMetrics должны быть пустыми")
	}

	// Возврат: метрики доступны → флаг снимается, ext считается.
	h.fetch.metricsErr = nil
	h.fetch.metricsBody = metricsBody(1, 0)
	h.advance(time.Duration(h.cfg.Monitor.MetricsIntervalSec) * time.Second)
	h.mon.Tick()
	s = h.mon.Sample("s")
	if s.Missing {
		t.Fatal("после возврата Missing = true, хочу false")
	}
	if s.Ext == nil || *s.Ext != 1 {
		t.Errorf("Ext = %v, хочу 1", s.Ext)
	}
	if s.Running == nil || *s.Running != 1 {
		t.Errorf("Running = %v, хочу 1", s.Running)
	}
}

// TestInflightSubtraction — ext = max(0, running + waiting − inflight_gw).
func TestInflightSubtraction(t *testing.T) {
	h := newMHarn(t, 1)
	// Заменяем inflight: 2 открытых запроса шлюза.
	h.mon = New(h.cfg, h.clk, h.fetch.get, h.sch, h.sink, fakeInfl{2},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.fetch.metricsBody = metricsBody(2, 0) // 2 + 0 − 2 = 0
	h.mon.Tick()
	s := h.mon.Sample("s")
	if s.Ext == nil || *s.Ext != 0 {
		t.Fatalf("Ext = %v, хочу 0 (running+waiting−inflight)", s.Ext)
	}
	// 3 + 0 − 2 = 1.
	h.fetch.metricsBody = metricsBody(3, 0)
	h.advance(time.Duration(h.cfg.Monitor.MetricsIntervalSec) * time.Second)
	h.mon.Tick()
	s = h.mon.Sample("s")
	if s.Ext == nil || *s.Ext != 1 {
		t.Fatalf("Ext = %v, хочу 1", s.Ext)
	}
}

// TestHealthUpDown — health-серии: down_after → DOWN, up_after → UP
// (переходы доходят до наблюдателей; DOWN сбрасывает rate-окно).
func TestHealthUpDown(t *testing.T) {
	h := newMHarn(t, 1)
	h.cfg.Monitor.HealthIntervalSec = 5
	h.fetch.metricsBody = metricsBody(0, 0)

	// 3 неудачных health подряд → DOWN.
	h.fetch.healthStatus = 503
	for i := 0; i < 3; i++ {
		h.advance(5 * time.Second)
		h.mon.Tick()
	}
	if h.sink.last() != "s=DOWN" {
		t.Fatalf("переходы = %v, хочу s=DOWN в конце", h.sink.changes)
	}
	// 2 успешных → UP (health_up_after).
	h.fetch.healthStatus = 200
	for i := 0; i < 2; i++ {
		h.advance(5 * time.Second)
		h.mon.Tick()
	}
	if h.sink.last() != "s=UP" {
		t.Fatalf("переходы = %v, хочу s=UP в конце", h.sink.changes)
	}
}

func (f *fakeSink) last() string {
	if len(f.changes) == 0 {
		return ""
	}
	return f.changes[len(f.changes)-1]
}

// TestRateWindow — tok/s за rate_window_sec: (конец − начало)/Δt.
func TestRateWindow(t *testing.T) {
	h := newMHarn(t, 1)
	// Счётчики растут: gen +100 за интервал.
	body := func(gen, prompt float64) string {
		var b strings.Builder
		fmt.Fprintf(&b, "vllm:num_requests_running{engine=\"0\",model_name=\"m\"} 0.0\n")
		fmt.Fprintf(&b, "vllm:num_requests_waiting{engine=\"0\",model_name=\"m\"} 0.0\n")
		fmt.Fprintf(&b, "vllm:generation_tokens_total{engine=\"0\",model_name=\"m\"} %v\n", gen)
		fmt.Fprintf(&b, "vllm:prompt_tokens_total{engine=\"0\",model_name=\"m\"} %v\n", prompt)
		return b.String()
	}
	h.fetch.metricsBody = body(100, 10)
	h.mon.Tick()
	// Одна точка — rate ещё нет.
	s := h.mon.Sample("s")
	if s.GenTokS != nil {
		t.Fatalf("GenTokS = %v, хочу nil (одна точка)", *s.GenTokS)
	}
	// Через интервал: +100 gen → 100 tok/s (окно 10 с, шаг 1 с).
	h.fetch.metricsBody = body(200, 20)
	h.advance(time.Duration(h.cfg.Monitor.MetricsIntervalSec) * time.Second)
	h.mon.Tick()
	s = h.mon.Sample("s")
	if s.GenTokS == nil || *s.GenTokS != 100 {
		t.Fatalf("GenTokS = %v, хочу 100", s.GenTokS)
	}
	if s.PromptTokS == nil || *s.PromptTokS != 10 {
		t.Fatalf("PromptTokS = %v, хочу 10", s.PromptTokS)
	}
}
