package scheduler

// Сценарии планировщика S1–S14 (раздел 5 ТЗ) на виртуальных часах.
//
// Каждый сценарий: детерминированные часы, фэйковые серверы/панели,
// dispatch без асинхронных горушин (reply — через InjectReply).

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"runpilot/internal/clock"
	"runpilot/internal/config"
	"runpilot/internal/model"
	"runpilot/internal/proto"
	"runpilot/internal/store"
)

// --- фэйки ---

type fakeServers struct{ list []ServerView }

func (f *fakeServers) List() []ServerView { return f.list }
func (f *fakeServers) set(name string, st model.ServerState) {
	for i := range f.list {
		if f.list[i].Name == name {
			f.list[i].State = st
		}
	}
}

type fakePanes struct {
	bySID  map[string]PaneSnap
	byPane map[string]PaneSnap
}

func (f *fakePanes) PaneBySID(sid string) (PaneSnap, bool)      { p, ok := f.bySID[sid]; return p, ok }
func (f *fakePanes) PaneByPaneID(id string) (PaneSnap, bool)    { p, ok := f.byPane[id]; return p, ok }

// harn — испытательный стенд планировщика.
type harn struct {
	t     *testing.T
	clk   *clock.Virtual
	st    *store.Store
	srv   *fakeServers
	panes *fakePanes
	sch   *Scheduler
	cfg   *config.Config
}

func testConfig() *config.Config {
	cfg := config.Defaults()
	cfg.Scheduler.TickMS = 100
	cfg.Scheduler.AgingSec = 60
	cfg.Scheduler.PreferWaitSec = 30
	cfg.Scheduler.PinUnavailableSec = 10
	cfg.Scheduler.AffinityTTLSec = 300
	cfg.Scheduler.ResumeBackoffSec = 5
	cfg.Scheduler.AutoRequeueMax = 3
	cfg.Scheduler.SnapshotMaxAgeSec = 1000
	cfg.Scheduler.DispatchStableSec = 2
	cfg.Scheduler.ExternalConfirmSec = 3
	cfg.Scheduler.CooldownSec = 2
	cfg.Dispatch.StartConfirmSec = 10
	cfg.Turn.DoneQuietSec = 5
	cfg.Turn.DoneStableSec = 5
	cfg.Turn.UnknownMaxSec = 60
	return cfg
}

func newHarn(t *testing.T, servers []ServerView) *harn {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "runpilot.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	clk := clock.NewVirtual(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))

	// v2 (CONTROL 2): рабочие настройки живут в БД. Задаём их в БД
	// (ревизия "migration"), затем загружаем в конфиг — планировщик читает
	// настройки, загруженные из БД, и пере-read их на каждом тике (4.4).
	src := testConfig()
	snap, err := src.WorkingSnapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if _, err := st.SaveWorkingSettings(snap, "test", "migration", clk.Now()); err != nil {
		t.Fatalf("save working: %v", err)
	}
	cfg := config.Defaults()
	if err := st.LoadWorkingInto(cfg); err != nil {
		t.Fatalf("load working: %v", err)
	}

	fs := &fakeServers{list: servers}
	panes := &fakePanes{bySID: map[string]PaneSnap{}, byPane: map[string]PaneSnap{}}
	sch := New(Options{
		Cfg: cfg, Store: st, Clk: clk, Servers: fs, Panes: panes,
		Dispatch: func(ctx context.Context, paneID string, m proto.Msg) (proto.Msg, error) {
			return proto.Msg{}, errors.New("dispatch отключён в тесте")
		},
		ResumeText:      "continue",
		NoAsyncDispatch: true,
		Reload:          func() error { return st.LoadWorkingInto(cfg) },
	})
	return &harn{t: t, clk: clk, st: st, srv: fs, panes: panes, sch: sch, cfg: cfg}
}

// srv — сервер с accept на все классы.
func srv(name string, priority, slots int, accept ...string) ServerView {
	if len(accept) == 0 {
		accept = []string{"resume", "high", "normal", "low"}
	}
	return ServerView{Name: name, Priority: priority, Slots: slots,
		Accept: accept, State: model.ServerUp}
}

// --- помощники ---

func (h *harn) addSession(sid, name, host string, state model.SessionState, class model.QueueClass, constraint model.Constraint) {
	h.t.Helper()
	rec := store.SessionRecord{
		SID: sid, Name: name, Host: host, HostIP: "127.0.0.1",
		TmuxSession: name, PaneID: "p-" + sid, Profile: "qwen",
		State: state, StateChangedAt: h.clk.Now(), Class: class,
		ConstraintKind: constraint.Kind, ConstraintServer: constraint.Server,
		CreatedAt: h.clk.Now(),
	}
	if err := h.st.CreateSession(rec); err != nil {
		h.t.Fatalf("create session %s: %v", sid, err)
	}
}

func (h *harn) setPane(sid, host string, st model.PaneState, hash uint64, inputEmpty bool) {
	h.t.Helper()
	paneID := "p-" + sid
	snap := PaneSnap{PaneID: paneID, SID: sid, State: st, Hash: hash,
		InputEmpty: inputEmpty, ReceivedAt: h.clk.Now(), Host: host}
	h.panes.bySID[sid] = snap
	h.panes.byPane[paneID] = snap
	h.sch.PaneUpdate(paneID, sid, st, hash, h.clk.Now())
}

// idlePane — панель готова к диспетчеризации: IDLE, ввод непуст.
func (h *harn) idlePane(sid, host string) { h.setPane(sid, host, model.PaneIdle, 1, false) }

func (h *harn) enqueue(sid string, class model.QueueClass, constraint model.Constraint, mode model.QueueMode) {
	h.t.Helper()
	h.enqueueAt(sid, class, constraint, mode, false, h.clk.Now())
}

func (h *harn) enqueueAt(sid string, class model.QueueClass, constraint model.Constraint, mode model.QueueMode, held bool, enqAt time.Time) {
	h.t.Helper()
	e := model.QueueEntry{SID: sid, Class: class, EnqueuedAt: enqAt,
		NotBefore: enqAt, Constraint: constraint, Mode: mode, HeldRequest: held}
	if err := h.st.QueueUpsert(e); err != nil {
		h.t.Fatalf("enqueue %s: %v", sid, err)
	}
}

func (h *harn) advance(d time.Duration) { h.clk.Advance(d) }
func (h *harn) tick()                   { h.sch.Tick() }

func (h *harn) leaseOf(sid string) (model.Lease, bool) {
	l, err := h.st.LeaseGet(sid)
	if err != nil {
		return model.Lease{}, false
	}
	return *l, true
}

func (h *harn) sessionState(sid string) model.SessionState {
	r, err := h.st.GetSession(sid)
	if err != nil {
		h.t.Fatalf("get session %s: %v", sid, err)
	}
	return r.State
}

func (h *harn) session(sid string) store.SessionRecord {
	r, err := h.st.GetSession(sid)
	if err != nil {
		h.t.Fatalf("get session %s: %v", sid, err)
	}
	return r
}

func (h *harn) queueEntry(sid string) (model.QueueEntry, bool) {
	e, err := h.st.QueueGet(sid)
	if err != nil {
		return model.QueueEntry{}, false
	}
	return *e, true
}

func (h *harn) queueReason(sid string) string {
	e, ok := h.queueEntry(sid)
	if !ok {
		return ""
	}
	return e.IneligibleReason
}

// --- S1 ---

// S1: оба сервера свободны, одна запись NORMAL → сервер с большим priority.
func TestS1Priority(t *testing.T) {
	h := newHarn(t, []ServerView{srv("a", 10, 1), srv("b", 5, 1)})
	h.addSession("s1", "s1", "host", model.SessionQueued, model.ClassNormal, model.NoConstraint)
	h.idlePane("s1", "host")
	h.enqueue("s1", model.ClassNormal, model.NoConstraint, model.QueueModeSubmit)
	h.advance(3 * time.Second) // стабильность панели
	h.tick()
	l, ok := h.leaseOf("s1")
	if !ok {
		t.Fatalf("S1: аренды нет, reason=%q", h.queueReason("s1"))
	}
	if l.Server != "a" {
		t.Fatalf("S1: сервер %s, хочу a (priority 10 > 5)", l.Server)
	}
}

// --- S2 ---

// S2: голова pin:srv2, свободен только aaa → слот получает следующая;
// у головы ineligible_reason=pin_mismatch.
func TestS2PinMismatch(t *testing.T) {
	h := newHarn(t, []ServerView{srv("aaa", 100, 1), srv("srv2", 50, 1)})
	// srv2 занят чужой арендой.
	h.addSession("holder", "holder", "host", model.SessionRunning, model.ClassNormal, model.NoConstraint)
	if _, err := h.st.LeaseCreate(model.Lease{SID: "holder", Server: "srv2", Slot: 1,
		State: model.LeaseActive, Origin: model.LeaseOriginImplicit, GrantedAt: h.clk.Now()}); err != nil {
		t.Fatal(err)
	}
	h.addSession("A", "A", "host", model.SessionQueued, model.ClassNormal,
		model.Constraint{Kind: model.ConstraintPin, Server: "srv2"})
	h.idlePane("A", "host")
	h.addSession("B", "B", "host", model.SessionQueued, model.ClassNormal, model.NoConstraint)
	h.idlePane("B", "host")
	h.enqueue("A", model.ClassNormal, model.Constraint{Kind: model.ConstraintPin, Server: "srv2"}, model.QueueModeSubmit)
	h.enqueue("B", model.ClassNormal, model.NoConstraint, model.QueueModeSubmit)
	h.advance(3 * time.Second)
	h.tick()
	if got := h.queueReason("A"); got != model.ReasonPinMismatch {
		t.Fatalf("S2: у A reason=%q, хочу pin_mismatch", got)
	}
	l, ok := h.leaseOf("B")
	if !ok {
		t.Fatalf("S2: у B нет аренды (reason=%q)", h.queueReason("B"))
	}
	if l.Server != "aaa" {
		t.Fatalf("S2: B на %s, хочу aaa", l.Server)
	}
}

// --- S3 ---

// S3: prefer:srv2, srv2 занят → ждёт prefer_wait_sec, затем любой сервер.
func TestS3PreferWait(t *testing.T) {
	h := newHarn(t, []ServerView{srv("a", 10, 1), srv("srv2", 5, 1)})
	h.addSession("holder", "holder", "host", model.SessionRunning, model.ClassNormal, model.NoConstraint)
	if _, err := h.st.LeaseCreate(model.Lease{SID: "holder", Server: "srv2", Slot: 1,
		State: model.LeaseActive, Origin: model.LeaseOriginImplicit, GrantedAt: h.clk.Now()}); err != nil {
		t.Fatal(err)
	}
	h.addSession("E", "E", "host", model.SessionQueued, model.ClassNormal,
		model.Constraint{Kind: model.ConstraintPrefer, Server: "srv2"})
	h.idlePane("E", "host")
	h.enqueue("E", model.ClassNormal, model.Constraint{Kind: model.ConstraintPrefer, Server: "srv2"}, model.QueueModeSubmit)
	h.advance(3 * time.Second)
	h.tick()
	if _, ok := h.leaseOf("E"); ok {
		t.Fatalf("S3: E получила слот до prefer_wait_sec")
	}
	if got := h.queueReason("E"); got != model.ReasonPreferWait {
		t.Fatalf("S3: reason=%q, хочу prefer_wait", got)
	}
	// Ожидаем prefer_wait_sec (30).
	h.advance(30 * time.Second)
	h.tick()
	l, ok := h.leaseOf("E")
	if !ok {
		t.Fatalf("S3: после prefer_wait_sec слота нет (reason=%q)", h.queueReason("E"))
	}
	if l.Server != "a" {
		t.Fatalf("S3: E на %s, хочу a (любой свободный)", l.Server)
	}
}

// --- S4 ---

// S4: тёплый кэш на сервере с меньшим priority → сервер кэша; после чужой
// аренды на нём — по priority.
func TestS4WarmCache(t *testing.T) {
	h := newHarn(t, []ServerView{srv("a", 10, 1), srv("b", 5, 1)})
	now := h.clk.Now()
	h.addSession("s1", "s1", "host", model.SessionQueued, model.ClassNormal, model.NoConstraint)
	// Тёплый кэш: last_server=b, ход закончился недавно.
	_ = h.st.SetSessionLastServer("s1", "b")
	_ = h.st.SetSessionLastTurnEnd("s1", now)
	h.idlePane("s1", "host")
	h.enqueue("s1", model.ClassNormal, model.NoConstraint, model.QueueModeSubmit)
	h.advance(3 * time.Second)
	h.tick()
	l, ok := h.leaseOf("s1")
	if !ok {
		t.Fatalf("S4: нет аренды (reason=%q)", h.queueReason("s1"))
	}
	if l.Server != "b" {
		t.Fatalf("S4: тёплый кэш — хочу b, получил %s", l.Server)
	}

	// Часть 2: чужая аренда на b после конца хода s1 → по priority.
	_ = h.st.LeaseRelease("s1", "TURN_DONE", h.clk.Now())
	_ = h.st.SetSessionState("s1", model.SessionQueued, "", h.clk.Now())
	h.addSession("s2", "s2", "host", model.SessionRunning, model.ClassNormal, model.NoConstraint)
	if _, err := h.st.LeaseCreate(model.Lease{SID: "s2", Server: "b", Slot: 1,
		State: model.LeaseActive, Origin: model.LeaseOriginImplicit, GrantedAt: h.clk.Now()}); err != nil {
		t.Fatal(err)
	}
	h.enqueue("s1", model.ClassNormal, model.NoConstraint, model.QueueModeSubmit)
	h.advance(3 * time.Second)
	h.tick()
	l2, ok := h.leaseOf("s1")
	if !ok {
		t.Fatalf("S4b: нет аренды (reason=%q)", h.queueReason("s1"))
	}
	if l2.Server != "a" {
		t.Fatalf("S4: после чужой аренды на b — по priority, хочу a, получил %s", l2.Server)
	}
}

// --- S4b ---

// S4b: миграция в этом ходе (last_migrated_from=S1) → S1 не тёплый кэш.
func TestS4bMigratedNotWarm(t *testing.T) {
	h := newHarn(t, []ServerView{srv("s1srv", 5, 1), srv("s2srv", 10, 1)})
	now := h.clk.Now()
	h.addSession("s1", "s1", "host", model.SessionQueued, model.ClassNormal, model.NoConstraint)
	// Миграция S1→S2 в этом ходе: last_server=S1 (старый), migrated_from=S1.
	_ = h.st.SetSessionLastServer("s1", "s1srv")
	_ = h.st.SetSessionLastTurnEnd("s1", now)
	_ = h.st.SetSessionMigratedFrom("s1", "s1srv")
	h.idlePane("s1", "host")
	h.enqueue("s1", model.ClassNormal, model.NoConstraint, model.QueueModeSubmit)
	h.advance(3 * time.Second)
	h.tick()
	l, ok := h.leaseOf("s1")
	if !ok {
		t.Fatalf("S4b: нет аренды (reason=%q)", h.queueReason("s1"))
	}
	// s1srv — migrated_from, тёплым не считается → по priority s2srv.
	if l.Server != "s2srv" {
		t.Fatalf("S4b: migrated_from не тёплый, хочу s2srv, получил %s", l.Server)
	}
}

// --- S5 ---

// S5: запись LOW ждёт → HIGH ровно через 2*aging_sec.
func TestS5Aging(t *testing.T) {
	// Сервер принимает только high/resume: LOW не допустим, HIGH — да.
	h := newHarn(t, []ServerView{srv("s", 10, 1, "resume", "high")})
	h.addSession("A", "A", "host", model.SessionQueued, model.ClassLow, model.NoConstraint)
	h.idlePane("A", "host")
	h.enqueue("A", model.ClassLow, model.NoConstraint, model.QueueModeSubmit)
	aging := time.Duration(h.cfg.Scheduler.AgingSec) * time.Second
	// t0: LOW → не accept.
	h.advance(3 * time.Second)
	h.tick()
	if _, ok := h.leaseOf("A"); ok {
		t.Fatalf("S5: LOW получила слот сразу")
	}
	if got := h.queueReason("A"); got != model.ReasonAccept {
		t.Fatalf("S5: reason=%q, хочу accept", got)
	}
	// Через aging (60s): NORMAL → всё ещё не accept.
	h.advance(aging)
	h.tick()
	if _, ok := h.leaseOf("A"); ok {
		t.Fatalf("S5: NORMAL (1*aging) получила слот")
	}
	// Через 2*aging (120s от старта): HIGH → accept.
	h.advance(aging)
	h.tick()
	l, ok := h.leaseOf("A")
	if !ok {
		t.Fatalf("S5: через 2*aging_sec слота нет (reason=%q)", h.queueReason("A"))
	}
	if l.Server != "s" {
		t.Fatalf("S5: сервер %s", l.Server)
	}
}

// --- S6 ---

// S6: RESUME с attempts=2 (not_before=2*resume_backoff) и HIGH → RESUME
// не раньше not_before, затем раньше HIGH.
func TestS6ResumeBackoff(t *testing.T) {
	h := newHarn(t, []ServerView{srv("s", 10, 1)})
	backoff := time.Duration(h.cfg.Scheduler.ResumeBackoffSec) * time.Second
	notBefore := h.clk.Now().Add(2 * backoff) // attempts=2
	h.addSession("H", "H", "host", model.SessionQueued, model.ClassHigh, model.NoConstraint)
	h.idlePane("H", "host")
	h.addSession("R", "R", "host", model.SessionQueued, model.ClassResume, model.NoConstraint)
	h.idlePane("R", "host")
	// R: RESUME, not_before = now + 2*backoff.
	h.enqueueAt("R", model.ClassResume, model.NoConstraint, model.QueueModeResume, false, h.clk.Now())
	_ = h.st.QueueUpsert(model.QueueEntry{SID: "R", Class: model.ClassResume,
		EnqueuedAt: h.clk.Now(), NotBefore: notBefore, Mode: model.QueueModeResume})
	h.enqueue("H", model.ClassHigh, model.NoConstraint, model.QueueModeSubmit)
	h.advance(3 * time.Second)
	h.tick()
	// До not_before: RESUME заблокирована, слот берёт HIGH.
	if _, ok := h.leaseOf("R"); ok {
		t.Fatalf("S6: RESUME раньше not_before")
	}
	lh, ok := h.leaseOf("H")
	if !ok || lh.Server != "s" {
		t.Fatalf("S6: HIGH должна получить слот, reason_H=%q", h.queueReason("H"))
	}
	// Освобождаем слот, доходим до not_before, добавляем нового HIGH.
	_ = h.st.LeaseRelease("H", "TURN_DONE", h.clk.Now())
	_ = h.st.QueueDelete("H")
	h.advance(2 * backoff)
	h.addSession("H2", "H2", "host", model.SessionQueued, model.ClassHigh, model.NoConstraint)
	h.idlePane("H2", "host")
	h.enqueue("H2", model.ClassHigh, model.NoConstraint, model.QueueModeSubmit)
	h.tick()
	lr, ok := h.leaseOf("R")
	if !ok {
		t.Fatalf("S6: RESUME после not_before не получила слот (reason=%q)", h.queueReason("R"))
	}
	if h.sessionState("H2") == model.SessionDispatching {
		t.Fatalf("S6: RESUME должна быть раньше HIGH")
	}
	_ = lr
}

// --- S7 ---

// S7: accept [resume, high] → NORMAL не получает сервер, why=accept.
func TestS7Accept(t *testing.T) {
	h := newHarn(t, []ServerView{srv("s", 10, 1, "resume", "high")})
	h.addSession("E", "E", "host", model.SessionQueued, model.ClassNormal, model.NoConstraint)
	h.idlePane("E", "host")
	h.enqueue("E", model.ClassNormal, model.NoConstraint, model.QueueModeSubmit)
	h.advance(3 * time.Second)
	h.tick()
	if _, ok := h.leaseOf("E"); ok {
		t.Fatalf("S7: NORMAL получила слот при accept [resume,high]")
	}
	why, err := h.sch.Why("E")
	if err != nil {
		t.Fatal(err)
	}
	if why.Reason != model.ReasonAccept {
		t.Fatalf("S7: why.Reason=%q, хочу accept", why.Reason)
	}
}

// --- S8 ---

// S8: внешняя нагрузка ext=1 → EXTERNAL через external_confirm_sec,
// FREE через тот же интервал после ext=0.
func TestS8External(t *testing.T) {
	h := newHarn(t, []ServerView{srv("s", 10, 2)})
	confirm := time.Duration(h.cfg.Scheduler.ExternalConfirmSec) * time.Second
	// ext=1 подтверждаем интервалом ДО постановки записей: 1 слот EXTERNAL.
	h.sch.SetExternal("s", 1)
	h.advance(confirm)
	h.tick()
	// Две записи: свободен 1 слот (второй — EXTERNAL).
	for _, sid := range []string{"E1", "E2"} {
		h.addSession(sid, sid, "host", model.SessionQueued, model.ClassNormal, model.NoConstraint)
		h.idlePane(sid, "host")
		h.enqueue(sid, model.ClassNormal, model.NoConstraint, model.QueueModeSubmit)
	}
	h.advance(3 * time.Second)
	h.tick()
	_, ok1 := h.leaseOf("E1")
	_, ok2 := h.leaseOf("E2")
	if ok1 == ok2 {
		t.Fatalf("S8: ожидалось ровно 1 аренда, E1=%v E2=%v", ok1, ok2)
	}
	skipped := "E1"
	if ok1 {
		skipped = "E2"
	}
	if got := h.queueReason(skipped); got != model.ReasonExternal {
		t.Fatalf("S8: reason=%q, хочу external", got)
	}
	// ext=0: возврат FREE через тот же интервал.
	h.sch.SetExternal("s", 0)
	h.tick()
	h.advance(confirm)
	h.tick()
	h.tick()
	if _, ok := h.leaseOf(skipped); !ok {
		t.Fatalf("S8: после возврата FREE запись не выдана (reason=%q)", h.queueReason(skipped))
	}
}

// --- S8b (Э6) ---

// S8b: ext = числу слотов (внешняя нагрузка на всё) → все слоты
// EXTERNAL держатся тик за тиком (регрессия: externalWant через
// freeSlots давал 0 и EXTERNAL сносился следующим тиком), запись
// не выдаётся с reason=external; ext=0 → FREE через confirm.
func TestS8bExternalFullLoad(t *testing.T) {
	h := newHarn(t, []ServerView{srv("s", 10, 2)})
	confirm := time.Duration(h.cfg.Scheduler.ExternalConfirmSec) * time.Second
	h.sch.SetExternal("s", 2)
	h.advance(confirm)
	h.tick()
	// EXTERNAL виден в снимке серверов: оба слота «external».
	for _, row := range h.sch.ServersView() {
		if row.Name != "s" {
			continue
		}
		if len(row.SlotInfo) != 2 || row.SlotInfo[0].State != "external" || row.SlotInfo[1].State != "external" {
			t.Fatalf("S8b: slots_info = %+v, хочу оба external", row.SlotInfo)
		}
	}
	// EXTERNAL держится на следующих тиках (2 слота, ext=2).
	h.tick()
	h.tick()
	h.addSession("F1", "F1", "host", model.SessionQueued, model.ClassNormal, model.NoConstraint)
	h.idlePane("F1", "host")
	h.enqueue("F1", model.ClassNormal, model.NoConstraint, model.QueueModeSubmit)
	h.advance(3 * time.Second)
	h.tick()
	if _, ok := h.leaseOf("F1"); ok {
		t.Fatal("S8b: F1 выдана при ext=2 (все слоты EXTERNAL)")
	}
	if got := h.queueReason("F1"); got != model.ReasonExternal {
		t.Fatalf("S8b: reason=%q, хочу external", got)
	}
	// EXTERNAL обязан держаться тик за тиком. Старый код (Э4) давал
	// осцилляцию: externalWant через freeSlots считал 0 свободных
	// слотов (EXTERNAL-слоты исключены) и сносил extCount следующим
	// тиком — F1 получала слот на 2-м тике.
	for i := 0; i < 10; i++ {
		h.advance(100 * time.Millisecond)
		h.tick()
		if _, ok := h.leaseOf("F1"); ok {
			t.Fatalf("S8b: F1 выдана на тике %d — EXTERNAL не держится", i)
		}
	}
	// ext=0 → FREE через confirm → выдача.
	h.sch.SetExternal("s", 0)
	h.tick()
	h.advance(confirm)
	h.tick()
	if _, ok := h.leaseOf("F1"); !ok {
		t.Fatalf("S8b: после ext=0 F1 не выдана (reason=%q)", h.queueReason("F1"))
	}
	// Резерв снят: второй слот снова «free».
	for _, row := range h.sch.ServersView() {
		if row.Name != "s" {
			continue
		}
		if row.SlotInfo[1].State != "free" {
			t.Fatalf("S8b: slot 2 = %q после ext=0, хочу free", row.SlotInfo[1].State)
		}
	}
}

// --- S9 ---

// S9: slots:2, три записи → две аренды, третья ждёт.
func TestS9Slots(t *testing.T) {
	h := newHarn(t, []ServerView{srv("s", 10, 2)})
	for _, sid := range []string{"E1", "E2", "E3"} {
		h.addSession(sid, sid, "host", model.SessionQueued, model.ClassNormal, model.NoConstraint)
		h.idlePane(sid, "host")
		h.enqueue(sid, model.ClassNormal, model.NoConstraint, model.QueueModeSubmit)
	}
	h.advance(3 * time.Second)
	h.tick()
	n := 0
	for _, sid := range []string{"E1", "E2", "E3"} {
		if _, ok := h.leaseOf(sid); ok {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("S9: аренд %d, хочу 2", n)
	}
	// Третья ждёт: причина записана.
	var waiting string
	for _, sid := range []string{"E1", "E2", "E3"} {
		if _, ok := h.leaseOf(sid); !ok {
			waiting = sid
		}
	}
	if waiting == "" {
		t.Fatalf("S9: все три получили слот")
	}
	if h.queueReason(waiting) == "" {
		t.Fatalf("S9: у ждущей %s нет ineligible_reason", waiting)
	}
}

// --- S10 ---

// S10: пауза → submit не выдаётся; held-запрос получает IMPLICIT.
func TestS10Pause(t *testing.T) {
	h := newHarn(t, []ServerView{srv("a", 10, 1), srv("b", 5, 1)})
	h.addSession("E1", "E1", "host", model.SessionQueued, model.ClassNormal, model.NoConstraint)
	h.idlePane("E1", "host")
	h.addSession("E2", "E2", "host", model.SessionIdle, model.ClassNormal, model.NoConstraint)
	h.idlePane("E2", "host")
	h.enqueue("E1", model.ClassNormal, model.NoConstraint, model.QueueModeSubmit)
	// held_request: удержанный запрос (неявная аренда).
	h.enqueueAt("E2", model.ClassNormal, model.NoConstraint, model.QueueModeSubmit, true, h.clk.Now())
	h.advance(3 * time.Second)
	h.sch.Pause(true)
	h.tick()
	if _, ok := h.leaseOf("E1"); ok {
		t.Fatalf("S10: submit выдан на паузе")
	}
	if got := h.queueReason("E1"); got != model.ReasonPause {
		t.Fatalf("S10: reason=%q, хочу pause", got)
	}
	l2, ok := h.leaseOf("E2")
	if !ok {
		t.Fatalf("S10: held-запрос не выдан на паузе")
	}
	if l2.Origin != model.LeaseOriginImplicit {
		t.Fatalf("S10: held-аренда %s, хочу IMPLICIT", l2.Origin)
	}
}

// --- S11 ---

// S11: enqueue работающей сессии → enqueued_at = время конца хода.
func TestS11EnqueueRunning(t *testing.T) {
	h := newHarn(t, []ServerView{srv("s", 10, 1)})
	h.addSession("s1", "s1", "host", model.SessionRunning, model.ClassNormal, model.NoConstraint)
	if _, err := h.st.LeaseCreate(model.Lease{SID: "s1", Server: "s", Slot: 1,
		State: model.LeaseActive, Origin: model.LeaseOriginImplicit, GrantedAt: h.clk.Now()}); err != nil {
		t.Fatal(err)
	}
	turnStart := h.clk.Now()
	if _, err := h.st.TurnStart("s1", turnStart); err != nil {
		t.Fatal(err)
	}
	h.idlePane("s1", "host")
	// Предварительная постановка RUNNING.
	h.enqueue("s1", model.ClassNormal, model.NoConstraint, model.QueueModeSubmit)
	// Завершаем ход (панель IDLE стабильна, запросов нет).
	stable := time.Duration(h.cfg.Turn.DoneStableSec) * time.Second
	h.advance(stable + time.Second)
	h.tick()
	e, ok := h.queueEntry("s1")
	if !ok {
		t.Fatalf("S11: запись очереди потеряна")
	}
	if !e.EnqueuedAt.Equal(h.clk.Now()) && e.EnqueuedAt.Before(h.clk.Now()) {
		// enqueued_at переписан на время конца хода (≈ now).
		if e.EnqueuedAt.Equal(turnStart) || e.EnqueuedAt.Before(turnStart) {
			t.Fatalf("S11: enqueued_at=%v не переписан на конец хода (turnStart=%v)", e.EnqueuedAt, turnStart)
		}
	}
}

// --- S12 ---

// S12: --after: зависимость OK / HOLD → допустима / HOLD(DEPENDENCY_FAILED).
func TestS12After(t *testing.T) {
	h := newHarn(t, []ServerView{srv("s", 10, 2)})
	now := h.clk.Now()
	// Case A: зависимость D завершена OK после enqueued_at.
	h.addSession("D", "D", "host", model.SessionIdle, model.ClassNormal, model.NoConstraint)
	dTurn, err := h.st.TurnStart("D", now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := h.st.TurnClose(dTurn, now.Add(-time.Minute), model.TurnOK, 1, "s"); err != nil {
		t.Fatal(err)
	}
	h.addSession("EA", "EA", "host", model.SessionQueued, model.ClassNormal, model.NoConstraint)
	h.idlePane("EA", "host")
	// EA поставлена до конца хода D: D завершила OK после enqueued_at.
	eaEnq := now.Add(-30 * time.Minute)
	ea := model.QueueEntry{SID: "EA", Class: model.ClassNormal, EnqueuedAt: eaEnq,
		NotBefore: eaEnq, AfterSID: "D", Mode: model.QueueModeSubmit}
	if err := h.st.QueueUpsert(ea); err != nil {
		t.Fatal(err)
	}
	// Case B: зависимость D2 в HOLD.
	h.addSession("D2", "D2", "host", model.SessionHold, model.ClassNormal, model.NoConstraint)
	h.addSession("EB", "EB", "host", model.SessionQueued, model.ClassNormal, model.NoConstraint)
	h.idlePane("EB", "host")
	eb := model.QueueEntry{SID: "EB", Class: model.ClassNormal, EnqueuedAt: now,
		NotBefore: now, AfterSID: "D2", Mode: model.QueueModeSubmit}
	if err := h.st.QueueUpsert(eb); err != nil {
		t.Fatal(err)
	}
	h.advance(3 * time.Second)
	h.tick()
	// A допустима (получает слот).
	if _, ok := h.leaseOf("EA"); !ok {
		t.Fatalf("S12: EA (зависимость OK) не допустима, reason=%q", h.queueReason("EA"))
	}
	// B → HOLD(DEPENDENCY_FAILED).
	if st := h.sessionState("EB"); st != model.SessionHold {
		t.Fatalf("S12: EB state=%s, хочу HOLD", st)
	}
	if hr := h.session("EB").HoldReason; hr != model.HoldDependencyFailed {
		t.Fatalf("S12: EB hold_reason=%s, хочу DEPENDENCY_FAILED", hr)
	}
}

// --- S13 ---

// S13: pin:srv2 и srv2 DOWN дольше pin_unavailable_sec → HOLD(PIN_UNAVAILABLE).
func TestS13PinUnavailable(t *testing.T) {
	h := newHarn(t, []ServerView{srv("aaa", 100, 1), srv("srv2", 50, 1)})
	h.addSession("E", "E", "host", model.SessionQueued, model.ClassNormal,
		model.Constraint{Kind: model.ConstraintPin, Server: "srv2"})
	h.idlePane("E", "host")
	h.enqueue("E", model.ClassNormal, model.Constraint{Kind: model.ConstraintPin, Server: "srv2"}, model.QueueModeSubmit)
	h.advance(3 * time.Second)
	// srv2 DOWN (фэйк + таймер pin_down).
	h.srv.set("srv2", model.ServerDown)
	h.sch.ServerState("srv2", model.ServerDown)
	h.advance(5 * time.Second)
	h.tick()
	if got := h.queueReason("E"); got != model.ReasonPinDown {
		t.Fatalf("S13: reason=%q, хочу pin_down (таймер ещё не вышел)", got)
	}
	// Держим DOWN дольше pin_unavailable_sec (10).
	h.advance(6 * time.Second)
	h.tick()
	if st := h.sessionState("E"); st != model.SessionHold {
		t.Fatalf("S13: state=%s, хочу HOLD", st)
	}
	if hr := h.session("E").HoldReason; hr != model.HoldPinUnavailable {
		t.Fatalf("S13: hold_reason=%s, хочу PIN_UNAVAILABLE", hr)
	}
}

// --- S14 ---

// S14: снимок со свежим received_at → допустима; старше snapshot_max_age →
// пропуск stale_snapshot.
func TestS14Snapshot(t *testing.T) {
	h := newHarn(t, []ServerView{srv("s", 10, 2)})
	// A: свежий received_at (часы узла в будущем не важны — важён received_at).
	h.addSession("A", "A", "host", model.SessionQueued, model.ClassNormal, model.NoConstraint)
	h.idlePane("A", "host") // received_at = now
	h.enqueue("A", model.ClassNormal, model.NoConstraint, model.QueueModeSubmit)
	// B: received_at старше snapshot_max_age.
	maxAge := time.Duration(h.cfg.Scheduler.SnapshotMaxAgeSec) * time.Second
	h.addSession("B", "B", "host", model.SessionQueued, model.ClassNormal, model.NoConstraint)
	h.setPane("B", "host", model.PaneIdle, 1, false)
	// Старили received_at: переснимаем с давним received_at.
	stale := h.clk.Now().Add(-maxAge - time.Second)
	snap := PaneSnap{PaneID: "p-B", SID: "B", State: model.PaneIdle, Hash: 1,
		InputEmpty: false, ReceivedAt: stale, Host: "host"}
	h.panes.bySID["B"] = snap
	h.panes.byPane["p-B"] = snap
	h.sch.PaneUpdate("p-B", "B", model.PaneIdle, 1, stale)
	h.enqueue("B", model.ClassNormal, model.NoConstraint, model.QueueModeSubmit)
	h.advance(3 * time.Second)
	// B становится стабильной по хешу, но received_at давний.
	h.tick()
	if _, ok := h.leaseOf("A"); !ok {
		t.Fatalf("S14: A (свежий received_at) не допустима, reason=%q", h.queueReason("A"))
	}
	if _, ok := h.leaseOf("B"); ok {
		t.Fatalf("S14: B (устаревший снимок) получила слот")
	}
	if got := h.queueReason("B"); got != model.ReasonStaleSnapshot {
		t.Fatalf("S14: reason=%q, хочу stale_snapshot", got)
	}
}

// --- Регрессия: автопостановка (раздел 9 ТЗ) ---

// TestAutoEnqueueFlow — сессия IDLE + auto_enqueue + панель IDLE с
// непустым вводом: после auto_enqueue_stable_sec тик ставит сессию в
// очередь, следующий тик выдаёт слот. Регрессия на мёртвую петлю: тик
// держит s.mu, и вызов публичного Enqueue (второй Lock) заклинивал
// планировщик — тест вешался бы без поправки на enqueueLocked.
func TestAutoEnqueueFlow(t *testing.T) {
	h := newHarn(t, []ServerView{srv("a", 10, 1)})
	rec := store.SessionRecord{
		SID: "ae", Name: "ae", Host: "host", HostIP: "127.0.0.1",
		TmuxSession: "ae", PaneID: "p-ae", Profile: "qwen",
		State: model.SessionIdle, StateChangedAt: h.clk.Now(),
		Class: model.ClassNormal, AutoEnqueue: true, CreatedAt: h.clk.Now(),
	}
	if err := h.st.CreateSession(rec); err != nil {
		t.Fatalf("create: %v", err)
	}
	h.idlePane("ae", "host")

	// Первый тик: фиксирует autoSince.
	h.tick()
	// Проход auto_enqueue_stable_sec (20 с по дефолту) + запас.
	h.advance(21 * time.Second)
	h.tick() // автопостановка: сессия → QUEUED, запись в очереди.

	if _, ok := h.queueEntry("ae"); !ok {
		t.Fatalf("автопостановка: записи в очереди нет (состояние=%s)", h.sessionState("ae"))
	}
	if st := h.sessionState("ae"); st != model.SessionQueued {
		t.Fatalf("автопостановка: состояние=%s, хочу QUEUED", st)
	}

	// Следующий тик: выдача слота.
	h.tick()
	l, ok := h.leaseOf("ae")
	if !ok {
		t.Fatalf("автопостановка: после постановки нет аренды (reason=%q)", h.queueReason("ae"))
	}
	if l.Server != "a" {
		t.Fatalf("автопостановка: сервер %s, хочу a", l.Server)
	}
}
