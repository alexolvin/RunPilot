package scheduler

// Момент применения рабочих настроек (v2 раздел 4.4). Настройки загружаются
// из БД и пере-read'ятся планировщиком на каждом тике; изменения в БД
// действуют «со следующего тика» (scheduler/monitor/notify) и для новых
// диспетчеризаций/ходов (dispatch/turn/gateway).

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"runpilot/internal/clock"
	"runpilot/internal/config"
	"runpilot/internal/model"
	"runpilot/internal/proto"
	"runpilot/internal/store"
)

// setWorking — изменить один рабочий ключ в БД (новым сохранением).
func (h *harn) setWorking(key string, val any) {
	h.t.Helper()
	cfg := config.Defaults()
	if err := h.st.LoadWorkingInto(cfg); err != nil {
		h.t.Fatal(err)
	}
	data, err := json.Marshal(val)
	if err != nil {
		h.t.Fatal(err)
	}
	if err := cfg.SetFieldJSON(key, data); err != nil {
		h.t.Fatal(err)
	}
	snap, err := cfg.WorkingSnapshot()
	if err != nil {
		h.t.Fatal(err)
	}
	if _, err := h.st.SaveWorkingSettings(snap, "test", "web", h.clk.Now()); err != nil {
		h.t.Fatal(err)
	}
}

// Строка 4.4 «scheduler.* — со следующего тика»: изменение
// scheduler.dispatch_stable_sec в БД действует на следующем тике.
func TestApplyWhenNextTickScheduler(t *testing.T) {
	h := newHarn(t, []ServerView{srv("s", 10, 1)})
	// Изменяем порог до первой выдачи: новая панель должна ждать 100с.
	h.setWorking("scheduler.dispatch_stable_sec", 100)
	h.addSession("A", "A", "host", model.SessionQueued, model.ClassNormal, model.NoConstraint)
	h.idlePane("A", "host")
	h.enqueue("A", model.ClassNormal, model.NoConstraint, model.QueueModeSubmit)
	// 3с (по старому значению 2с дало бы выдачу) — ещё не стабильна (100с).
	h.advance(3 * time.Second)
	h.tick()
	if _, ok := h.leaseOf("A"); ok {
		t.Fatal("A выдана до dispatch_stable_sec=100 — новое значение не применилось")
	}
	// После 100с стабильности — выдача.
	h.advance(100 * time.Second)
	h.tick()
	if _, ok := h.leaseOf("A"); !ok {
		t.Fatalf("A не выдана после dispatch_stable_sec=100 (reason=%q)", h.queueReason("A"))
	}
}

// Строка 4.4 «turn.* — для новых ходов»: изменение turn.done_stable_sec
// действует для нового хода.
func TestApplyWhenNewTurn(t *testing.T) {
	h := newHarn(t, []ServerView{srv("s", 10, 1)})
	// Новое значение для хода, начатого после изменения.
	h.setWorking("turn.done_stable_sec", 100)
	h.addSession("s1", "s1", "host", model.SessionRunning, model.ClassNormal, model.NoConstraint)
	if _, err := h.st.LeaseCreate(model.Lease{SID: "s1", Server: "s", Slot: 1,
		State: model.LeaseActive, Origin: model.LeaseOriginImplicit, GrantedAt: h.clk.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.st.TurnStart("s1", h.clk.Now()); err != nil {
		t.Fatal(err)
	}
	h.idlePane("s1", "host")
	h.enqueue("s1", model.ClassNormal, model.NoConstraint, model.QueueModeSubmit)
	// По старому значению (5с) ход завершился бы; по новому (100с) — нет.
	h.advance(6 * time.Second)
	h.tick()
	if _, ok := h.leaseOf("s1"); !ok {
		t.Fatal("аренда снята до done_stable_sec=100 — новое значение не применилось")
	}
	// После 100с — ход завершён, аренда снята.
	h.advance(100 * time.Second)
	h.tick()
	if _, ok := h.leaseOf("s1"); ok {
		t.Fatal("аренда не снята после done_stable_sec=100")
	}
}

// dbServers — источник серверов из БД (таблица server, v2 раздел 3.4).
type dbServers struct{ st *store.Store }

func (d *dbServers) List() []ServerView {
	servers, _ := d.st.ListServers()
	out := make([]ServerView, 0, len(servers))
	for _, s := range servers {
		out = append(out, ServerView{
			Name: s.Name, Priority: s.Priority, Slots: s.Slots,
			Accept: s.Accept, State: model.ServerUp,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Priority > out[j].Priority })
	return out
}

// newHarnDB — стенд с серверами из БД (для строк 4.4 про серверы).
func newHarnDB(t *testing.T, servers []config.Server) *harn {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "runpilot.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	clk := clock.NewVirtual(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	src := testConfig()
	snap, _ := src.WorkingSnapshot()
	if _, err := st.SaveWorkingSettings(snap, "test", "migration", clk.Now()); err != nil {
		t.Fatalf("save working: %v", err)
	}
	cfg := config.Defaults()
	if err := st.LoadWorkingInto(cfg); err != nil {
		t.Fatal(err)
	}
	for _, s := range servers {
		if err := st.UpsertServer(s, clk.Now()); err != nil {
			t.Fatalf("server %s: %v", s.Name, err)
		}
	}
	panes := &fakePanes{bySID: map[string]PaneSnap{}, byPane: map[string]PaneSnap{}}
	sch := New(Options{
		Cfg: cfg, Store: st, Clk: clk, Servers: &dbServers{st: st}, Panes: panes,
		Dispatch: func(ctx context.Context, paneID string, m proto.Msg) (proto.Msg, error) {
			return proto.Msg{}, errors.New("dispatch отключён в тесте")
		},
		ResumeText:      "continue",
		NoAsyncDispatch: true,
		Reload:          func() error { return st.LoadWorkingInto(cfg) },
	})
	return &harn{t: t, clk: clk, st: st, srv: nil, panes: panes, sch: sch, cfg: cfg}
}

func dbServer(name string, priority, slots int) config.Server {
	return config.Server{
		Name: name, Priority: priority, Slots: slots,
		Accept: []string{"resume", "high", "normal", "low"},
		HealthURL: "http://192.0.2.1:8004/health", MetricsURL: "http://192.0.2.1:8004/metrics",
		MaxOutputTokens: 16384, FirstByteTimeoutSec: 900,
		Upstreams: config.Upstreams{OpenAI: config.UpstreamOpenAI{
			URL: "http://192.0.2.1:8004", Model: "m", KeyEnv: "K"}},
	}
}

// Строка 4.4 «priority, accept сервера — со следующего тика»: смена
// priority в БД действует на следующем тике.
func TestApplyWhenServerPriority(t *testing.T) {
	h := newHarnDB(t, []config.Server{dbServer("a", 5, 2), dbServer("b", 10, 2)})
	// S1 → b (priority 10 > 5).
	h.addSession("S1", "S1", "host", model.SessionQueued, model.ClassNormal, model.NoConstraint)
	h.idlePane("S1", "host")
	h.enqueue("S1", model.ClassNormal, model.NoConstraint, model.QueueModeSubmit)
	h.advance(3 * time.Second)
	h.tick()
	l1, ok := h.leaseOf("S1")
	if !ok || l1.Server != "b" {
		t.Fatalf("S1 на %q, хочу b (priority 10)", l1.Server)
	}
	// Меняем priority a → 100 в БД.
	a, _ := h.st.GetServer("a")
	a.Priority = 100
	if err := h.st.UpsertServer(a, h.clk.Now()); err != nil {
		t.Fatal(err)
	}
	// S2 → a (теперь priority 100 > 10) — на следующем тике.
	h.addSession("S2", "S2", "host", model.SessionQueued, model.ClassNormal, model.NoConstraint)
	h.idlePane("S2", "host")
	h.enqueue("S2", model.ClassNormal, model.NoConstraint, model.QueueModeSubmit)
	h.advance(3 * time.Second)
	h.tick()
	l2, ok := h.leaseOf("S2")
	if !ok || l2.Server != "a" {
		t.Fatalf("S2 на %q, хочу a (priority 100 после смены)", l2.Server)
	}
}

// Строка 4.4 «slots сервера — со следующего тика»: увеличение слотов в БД
// даёт новые выдачи на следующем тике.
func TestApplyWhenServerSlots(t *testing.T) {
	h := newHarnDB(t, []config.Server{dbServer("a", 10, 1)})
	h.addSession("S1", "S1", "host", model.SessionQueued, model.ClassNormal, model.NoConstraint)
	h.idlePane("S1", "host")
	h.addSession("S2", "S2", "host", model.SessionQueued, model.ClassNormal, model.NoConstraint)
	h.idlePane("S2", "host")
	h.enqueue("S1", model.ClassNormal, model.NoConstraint, model.QueueModeSubmit)
	h.enqueue("S2", model.ClassNormal, model.NoConstraint, model.QueueModeSubmit)
	h.advance(3 * time.Second)
	h.tick()
	// 1 слот: S1 выдана, S2 ждёт.
	if _, ok := h.leaseOf("S1"); !ok {
		t.Fatalf("S1 не выдана (reason=%q)", h.queueReason("S1"))
	}
	if _, ok := h.leaseOf("S2"); ok {
		t.Fatal("S2 выдана при 1 слоте")
	}
	// Увеличиваем slots a → 2 в БД.
	a, _ := h.st.GetServer("a")
	a.Slots = 2
	if err := h.st.UpsertServer(a, h.clk.Now()); err != nil {
		t.Fatal(err)
	}
	// Следующий тик: второй слот свободен, S2 выдана.
	h.tick()
	if _, ok := h.leaseOf("S2"); !ok {
		t.Fatalf("S2 не выдана после увеличения slots (reason=%q)", h.queueReason("S2"))
	}
}
