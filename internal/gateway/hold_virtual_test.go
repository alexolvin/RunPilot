package gateway

// П.10 — тест на виртуальных часах: удержанный запрос выдаётся ПЛАНИРОВЩИКОМ
// (событие LEASE_GRANT, «held»:true) в ТОМ ЖЕ тике, в котором освобождён слот.
// Три пути освобождения: COOLDOWN→FREE, EXTERNAL→FREE (метрики), DOWN→UP.
//
// Критерий «свой LEASE_GRANT, не чужой release»: ждущий получает аренду только
// по LEASE_GRANT своего sid (эмитит планировщик); self-grant шлюза такого
// события не создаёт — поэтому тест проверяет именно событие LEASE_GRANT.

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"runpilot/internal/clock"
	"runpilot/internal/config"
	"runpilot/internal/model"
	"runpilot/internal/proto"
	"runpilot/internal/scheduler"
	"runpilot/internal/sid"
	"runpilot/internal/store"
	"runpilot/internal/testutil/fakellm"
)

// --- адаптеры для планировщика (без монитора/hub) ---

type vclockServers struct{ gw *Servers }

func (a *vclockServers) List() []scheduler.ServerView {
	all := a.gw.All()
	out := make([]scheduler.ServerView, 0, len(all))
	for _, s := range all {
		out = append(out, scheduler.ServerView{
			Name: s.Cfg.Name, Priority: s.Cfg.Priority, Slots: s.Cfg.Slots,
			Accept: s.Cfg.Accept, State: s.State(),
		})
	}
	return out
}

type vclockPanes struct{}

func (p *vclockPanes) PaneBySID(string) (scheduler.PaneSnap, bool)     { return scheduler.PaneSnap{}, false }
func (p *vclockPanes) PaneByPaneID(model.PaneKey) (scheduler.PaneSnap, bool)  { return scheduler.PaneSnap{}, false }

// vclockEnv — окружение: gateway + scheduler на виртуальных часах (прод-вайринг
// из serve.go: Wake=gw.Wake, Servers().OnChange→sch.ServerState).
func vclockEnv(t *testing.T, servers []config.Server) (
	*store.Store, *Gateway, *scheduler.Scheduler, *clock.Virtual, *httptest.Server) {
	t.Helper()
	cfg := config.Defaults()
	cfg.Servers = servers
	// HoldMaxSec — большой: real-timer удержания не должен сработать в тесте
	// (виртуальные часы его не двигают); выдача идёт раньше.
	cfg.Gateway.HoldMaxSec = 3600
	cfg.Gateway.RetryAfterSec = 30
	cfg.Scheduler.TickMS = 100
	cfg.Scheduler.CooldownSec = 2
	cfg.Scheduler.ExternalConfirmSec = 3
	cfg.Turn.DoneQuietSec = 5
	cfg.Turn.DoneStableSec = 5
	st, err := store.Open(filepath.Join(t.TempDir(), "runpilot.db"))
	if err != nil {
		t.Fatal(err)
	}
	clk := clock.NewVirtual(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	gw, err := NewGateway(cfg, st, clk, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		st.Close()
		t.Fatal(err)
	}
	ts := httptest.NewServer(gw.Handler())
	sch := scheduler.New(scheduler.Options{
		Cfg:    cfg,
		Store:  st,
		Clk:    clk,
		Servers: &vclockServers{gw: gw.Servers()},
		Panes:   &vclockPanes{},
		Dispatch: func(ctx context.Context, key model.PaneKey, m proto.Msg) (proto.Msg, error) {
			return proto.Msg{}, errors.New("dispatch отключён в тесте")
		},
		ResumeText:      "continue",
		NoAsyncDispatch: true,
		Wake:            gw.Wake,
		Reload:          func() error { return nil },
	})
	gw.Servers().OnChange(sch.ServerState)
	t.Cleanup(func() {
		ts.Close()
		_ = st.Close()
	})
	return st, gw, sch, clk, ts
}

// vcSession — сессия в БД (SID, имя, state).
func vcSession(t *testing.T, st *store.Store, state model.SessionState) string {
	t.Helper()
	s, err := sid.New()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := st.CreateSession(store.SessionRecord{
		SID: s, Name: "vc-" + s, Host: "vc", HostIP: "127.0.0.1",
		TmuxSession: "vc", PaneID: "%1", Profile: "qwen",
		State: state, StateChangedAt: now, Class: model.ClassNormal,
		ConstraintKind: "none", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	return s
}

// vcHeld — сессия, чей запрос удержан: запись очереди held_request=true (как
// создаёт holdPass). Возвращает SID.
func vcHeld(t *testing.T, st *store.Store, clk *clock.Virtual) string {
	t.Helper()
	s := vcSession(t, st, model.SessionIdle)
	now := clk.Now()
	if err := st.QueueUpsert(model.QueueEntry{
		SID: s, Class: model.ClassNormal, EnqueuedAt: now, NotBefore: now,
		Constraint: model.NoConstraint, Mode: model.QueueModeSubmit, HeldRequest: true,
	}); err != nil {
		t.Fatal(err)
	}
	return s
}

// vcGrantEvent — последнее LEASE_GRANT для sid (есть/нет + ts).
func vcGrantEvent(t *testing.T, st *store.Store, sid string) (time.Time, bool) {
	t.Helper()
	evs, err := st.EventList(0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var last time.Time
	found := false
	for _, e := range evs {
		if e.Kind == model.KindLeaseGrant && e.SID == sid {
			last = e.TS
			found = true
		}
	}
	return last, found
}

// occupy — слот занят аренду holder'а (ACTIVE, IMPLICIT).
func occupy(t *testing.T, st *store.Store, clk *clock.Virtual, holder, server string, slot int) {
	t.Helper()
	l := model.Lease{SID: holder, Server: server, Slot: slot, State: model.LeaseActive,
		Origin: model.LeaseOriginImplicit, GrantedAt: clk.Now()}
	if _, err := st.LeaseCreate(l); err != nil {
		t.Fatal(err)
	}
}

// assertHeld — ждущий ещё НЕ выдан (нет аренды, нет LEASE_GRANT).
func assertHeld(t *testing.T, st *store.Store, waiter, label string) {
	t.Helper()
	if l, err := st.LeaseGet(waiter); err == nil {
		t.Fatalf("[%s] ждущий уже выдан (%s) до освобождения слота", label, l.Server)
	}
	if _, ok := vcGrantEvent(t, st, waiter); ok {
		t.Fatalf("[%s] LEASE_GRANT до освобождения слота", label)
	}
}

// assertGrantedByScheduler — ждущий выдан ПЛАНИРОВЩИКОМ: аренда ACTIVE/IMPLICIT +
// событие LEASE_GRANT (self-grant шлюза LEASE_GRANT не эмитит).
func assertGrantedByScheduler(t *testing.T, st *store.Store, waiter, label, wantServer string) {
	t.Helper()
	l, err := st.LeaseGet(waiter)
	if err != nil {
		t.Fatalf("[%s] аренды нет после освобождения: %v", label, err)
	}
	if l.Server != wantServer || l.State != model.LeaseActive || l.Origin != model.LeaseOriginImplicit {
		t.Fatalf("[%s] аренда %s/%s/%s, хочу %s/ACTIVE/IMPLICIT",
			label, l.Server, l.State, l.Origin, wantServer)
	}
	if _, ok := vcGrantEvent(t, st, waiter); !ok {
		t.Fatalf("[%s] нет события LEASE_GRANT — выдачу сделал не планировщик (self-grant?)", label)
	}
	if _, err := st.QueueGet(waiter); err == nil {
		t.Fatalf("[%s] запись очереди должна быть удалена при выдаче", label)
	}
}

// --- П.10: три пути освобождения слота ---

func TestP10ExternalFree(t *testing.T) {
	a := fakellm.Start("srv-model")
	defer a.Close()
	t.Setenv("VC_KEY", "k")
	st, _, sch, clk, _ := vclockEnv(t, []config.Server{srvCfg("s", 10, 1, a.URL, "VC_KEY")})
	waiter := vcHeld(t, st, clk)
	confirm := time.Duration(cfgExternalConfirmSec) * time.Second

	// Слот EXTERNAL (внешняя нагрузка = 1): ждущий не выдаётся.
	sch.SetExternal("s", 1)
	clk.Advance(confirm)
	sch.Tick()
	assertHeld(t, st, waiter, "external")

	// Возврат FREE (внешняя нагрузка = 0, подтверждение интервалом): слот
	// освобождается (tickExternal) и ждущий выдаётся (tickGrant) в ТОМ ЖЕ тике.
	sch.SetExternal("s", 0)
	sch.Tick() // отметка возврата
	clk.Advance(confirm)
	sch.Tick() // tickExternal→FREE + tickGrant в одном тике
	assertGrantedByScheduler(t, st, waiter, "external", "s")
}

func TestP10DownUp(t *testing.T) {
	a := fakellm.Start("srv-model")
	defer a.Close()
	t.Setenv("VC_KEY", "k")
	st, gw, sch, clk, _ := vclockEnv(t, []config.Server{srvCfg("s", 10, 1, a.URL, "VC_KEY")})
	waiter := vcHeld(t, st, clk)

	// Сервер DOWN: freeSlots пропускает, ждущий не выдаётся.
	gw.Servers().SetState("s", model.ServerDown) // → sch.ServerState через OnChange
	clk.Advance(time.Second)
	sch.Tick()
	assertHeld(t, st, waiter, "down")

	// DOWN→UP: сервер снова UP, слот свободен → выдача в том же тике.
	gw.Servers().SetState("s", model.ServerUp)
	sch.Tick()
	assertGrantedByScheduler(t, st, waiter, "down-up", "s")
}

func TestP10CooldownFree(t *testing.T) {
	a := fakellm.Start("srv-model")
	defer a.Close()
	t.Setenv("VC_KEY", "k")
	st, _, sch, clk, _ := vclockEnv(t, []config.Server{srvCfg("s", 10, 1, a.URL, "VC_KEY")})
	waiter := vcHeld(t, st, clk)

	// Ход holder'а RUNNING + ACTIVE-аренда + idle-панель → closeTurn (TURN_DONE)
	// → слот в COOLDOWN. Ждущий не выдаётся, пока cooldown.
	holder := vcSession(t, st, model.SessionRunning)
	occupy(t, st, clk, holder, "s", 1)
	st.TurnStart(holder, clk.Now())
	st.SetSessionState(holder, model.SessionRunning, "", clk.Now())
	// Панель holder'а IDLE (условие closeTurn: пр IDLE + hashSince стабильно).
	sch.PaneUpdate(model.PaneKeyOf("vc", "%1"), holder, model.PaneIdle, 1, clk.Now())
	clk.Advance(time.Duration(cfgDoneStableSec) * time.Second + time.Second)
	sch.Tick() // closeTurn → releaseLease(TURN_DONE) + setCooldown
	if l, _ := st.LeaseGet(holder); l != nil && l.State == model.LeaseActive {
		t.Fatalf("аренда holder'а не завершена: %s", l.State)
	}
	assertHeld(t, st, waiter, "cooldown-pending")

	// COOLDOWN→FREE (истечение cooldown) + выдача ждущего в ТОМ ЖЕ тике.
	clk.Advance(time.Duration(cfgCooldownSec) * time.Second + 100*time.Millisecond)
	sch.Tick() // tickCooldown→FREE + tickGrant в одном тике
	assertGrantedByScheduler(t, st, waiter, "cooldown", "s")
}

// --- вспомогательные константы (зеркало testConfig) ---

const (
	cfgExternalConfirmSec = 3
	cfgDoneStableSec      = 5
	cfgCooldownSec        = 2
)

// --- П.2: два параллельных запроса одной сессии ---

// П.2: два удержанных запроса, первый оборвался → запись на месте,
// второй получает аренду; оба оборвались → запись удалена, сессия IDLE.
func TestP2TwoParallelHeldRequests(t *testing.T) {
	a := fakellm.Start("srv-model")
	defer a.Close()
	t.Setenv("VC_KEY", "k")
	st, gw, sch, clk, _ := vclockEnv(t, []config.Server{srvCfg("s", 10, 1, a.URL, "VC_KEY")})

	// Слот занят: ждущие не выдаются.
	holder := vcSession(t, st, model.SessionRunning)
	occupy(t, st, clk, holder, "s", 1)

	// Два параллельных запроса одной сессии (как qwen: служебный + основной).
	s := vcSession(t, st, model.SessionIdle)
	now := clk.Now()
	_ = st.QueueUpsert(model.QueueEntry{
		SID: s, Class: model.ClassNormal, EnqueuedAt: now, NotBefore: now,
		Constraint: model.NoConstraint, Mode: model.QueueModeSubmit, HeldRequest: true,
	})

	// Запускаем два удержанных запроса (оба входят в holdPass).
	ctx1, cancel1 := context.WithCancel(context.Background())
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel1()
	defer cancel2()

	done1 := make(chan error, 1)
	done2 := make(chan error, 1)
	go func() {
		_, err := gw.holdPass(ctx1, store.SessionRecord{SID: s, State: model.SessionIdle},
			model.ClassNormal, model.NoConstraint, nil)
		done1 <- err
	}()
	go func() {
		_, err := gw.holdPass(ctx2, store.SessionRecord{SID: s, State: model.SessionIdle},
			model.ClassNormal, model.NoConstraint, nil)
		done2 <- err
	}()

	// Ждём, пока оба запроса войдут в удержание (heldCount == 2).
	for i := 0; i < 100; i++ {
		if gw.heldCountGet(s) >= 2 {
			break
		}
		time.Sleep(time.Millisecond)
	}

	// Запись очереди на месте (оба запроса удержаны).
	if _, err := st.QueueGet(s); err != nil {
		t.Fatalf("запись очереди должна быть на месте: %v", err)
	}

	// Первый запрос оборвался (client_gone).
	cancel1()
	select {
	case <-done1:
	case <-time.After(2 * time.Second):
		t.Fatal("первый запрос не завершён")
	}

	// Запись очереди ЕЩЁ на месте (второй запрос ещё удержан).
	if _, err := st.QueueGet(s); err != nil {
		t.Fatalf("запись очереди должна остаться после первого client_gone: %v", err)
	}

	// Освобождаем слот: планировщик выдаёт аренду второму запросу.
	_ = st.LeaseRelease(holder, "TURN_DONE", clk.Now())
	sch.Tick() // tickGrant: выдать ждущему
	clk.Advance(100 * time.Millisecond)
	select {
	case err := <-done2:
		if err != nil {
			t.Fatalf("второй запрос: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("второй запрос не получил аренду")
	}

	// Аренда выдана второму запросу.
	if l, err := st.LeaseGet(s); err != nil || l.Server != "s" {
		t.Fatalf("аренда не выдана: %v %v", l, err)
	}
}

// П.2: оба запроса оборвались → запись удалена, сессия IDLE.
func TestP2BothRequestsGone(t *testing.T) {
	a := fakellm.Start("srv-model")
	defer a.Close()
	t.Setenv("VC_KEY", "k")
	st, gw, _, clk, _ := vclockEnv(t, []config.Server{srvCfg("s", 10, 1, a.URL, "VC_KEY")})

	// Слот занят.
	holder := vcSession(t, st, model.SessionRunning)
	occupy(t, st, clk, holder, "s", 1)

	// Два параллельных запроса одной сессии.
	s := vcSession(t, st, model.SessionIdle)
	now := clk.Now()
	_ = st.QueueUpsert(model.QueueEntry{
		SID: s, Class: model.ClassNormal, EnqueuedAt: now, NotBefore: now,
		Constraint: model.NoConstraint, Mode: model.QueueModeSubmit, HeldRequest: true,
	})

	ctx1, cancel1 := context.WithCancel(context.Background())
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel1()
	defer cancel2()

	done1 := make(chan error, 1)
	done2 := make(chan error, 1)
	go func() {
		_, err := gw.holdPass(ctx1, store.SessionRecord{SID: s, State: model.SessionIdle},
			model.ClassNormal, model.NoConstraint, nil)
		done1 <- err
	}()
	go func() {
		_, err := gw.holdPass(ctx2, store.SessionRecord{SID: s, State: model.SessionIdle},
			model.ClassNormal, model.NoConstraint, nil)
		done2 <- err
	}()

	// Ждём, пока оба запроса войдут в удержание (heldCount == 2).
	for i := 0; i < 100; i++ {
		if gw.heldCountGet(s) >= 2 {
			break
		}
		time.Sleep(time.Millisecond)
	}

	// Оба запроса оборвались.
	cancel1()
	cancel2()
	select {
	case <-done1:
	case <-time.After(2 * time.Second):
		t.Fatal("первый запрос не завершён")
	}
	select {
	case <-done2:
	case <-time.After(2 * time.Second):
		t.Fatal("второй запрос не завершён")
	}

	// Запись очереди удалена (оба запроса оборвались).
	if _, err := st.QueueGet(s); err == nil {
		t.Fatal("запись очереди должна быть удалена после обоих client_gone")
	}

	// Сессия IDLE.
	sess, err := st.GetSession(s)
	if err != nil {
		t.Fatal(err)
	}
	if sess.State != model.SessionIdle {
		t.Fatalf("сессия = %s, хочу IDLE", sess.State)
	}
}
