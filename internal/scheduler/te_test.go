package scheduler

// TE-<ID> — строки раздела 7 ТЗ (CONTROL W7), которые решаются в
// планировщике: исход хода и снятие аренды по реакции сервера/слотов.

import (
	"errors"
	"strings"
	"testing"
	"time"

	"runpilot/internal/model"
	"runpilot/internal/proto"
)

// TestTE_S6 — контекст-лимит: последний генерирующий запрос хода — 4xx →
// ход завершён ошибкой, аренда снята, сессия HOLD(UPSTREAM_4XX), без
// автоперепостановки (перепостановка — только ERROR_TRANSIENT).
func TestTE_S6(t *testing.T) {
	h := newHarn(t, []ServerView{srv("s", 10, 1)})
	h.grantPending("s1")
	h.sch.InjectReply("s1", proto.Msg{Result: proto.ResAlreadyBusy}, nil)
	if st := h.sessionState("s1"); st != model.SessionRunning {
		t.Fatalf("предпосылка: state=%s, хочу RUNNING", st)
	}
	now := h.clk.Now()
	// Ход: 4xx (контекст-лимит) — последний генерирующий запрос.
	h.sch.RequestTick("s1", true)
	if err := h.st.RequestRecord(model.Request{
		SID: "s1", Server: "s", Path: "v1/chat/completions", Status: 400,
		TStart: now, TFirstByte: now.Add(500 * time.Millisecond), TEnd: now.Add(time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	// Запрос асинхронный: дождаться коммита (иначе ход сочтён OK).
	waitLastGenerative(t, h, "s1", 400)
	h.sch.RequestTick("s1", false) // запрос завершился → last_request_end
	h.idlePane("s1", "host")
	quiet := time.Duration(h.cfg.Turn.DoneQuietSec) * time.Second
	stable := time.Duration(h.cfg.Turn.DoneStableSec) * time.Second
	h.advance(quiet + stable + time.Second)
	h.tick()

	if st := h.sessionState("s1"); st != model.SessionHold {
		t.Fatalf("state=%s, хочу HOLD", st)
	}
	if hr := h.session("s1").HoldReason; hr != model.HoldUpstream4XX {
		t.Fatalf("hold_reason=%s, хочу UPSTREAM_4XX", hr)
	}
	// Ход закрыт с ошибкой (не OK).
	turns, err := h.st.TurnPage(0, 50)
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	for _, tr := range turns {
		if tr.SID == "s1" && !tr.EndedAt.IsZero() && tr.Outcome != model.TurnOK {
			closed = true
		}
	}
	if !closed {
		t.Fatal("ход не завершён ошибкой")
	}
	// 4xx — не transient: автоперепостановки (RESUME-запись) нет.
	if _, ok := h.queueEntry("s1"); ok {
		t.Fatal("4xx не перепостанавливается автоматически (нет RESUME-записи)")
	}
}

// TestTE_S12 — слоты уменьшены ниже живых аренд: ходы завершаются сами,
// новые выдачи не выдаются (свободных слотов нет), живые аренды на месте.
func TestTE_S12(t *testing.T) {
	h := newHarn(t, []ServerView{srv("s", 10, 2)})
	// Две живые аренды заняли оба слота.
	for i, sid := range []string{"L1", "L2"} {
		h.addSession(sid, sid, "host", model.SessionRunning, model.ClassNormal, model.NoConstraint)
		if _, err := h.st.LeaseCreate(model.Lease{SID: sid, Server: "s", Slot: i + 1,
			State: model.LeaseActive, Origin: model.LeaseOriginImplicit, GrantedAt: h.clk.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	// Оператор уменьшил слоты: 2 → 1 (ниже числа живых аренд).
	for i := range h.srv.list {
		if h.srv.list[i].Name == "s" {
			h.srv.list[i].Slots = 1
		}
	}
	// Новая запись — слотов нет, выдавать нечего.
	h.addSession("N1", "N1", "host", model.SessionQueued, model.ClassNormal, model.NoConstraint)
	h.idlePane("N1", "host")
	h.enqueue("N1", model.ClassNormal, model.NoConstraint, model.QueueModeSubmit)
	h.advance(3 * time.Second)
	h.tick()
	if _, ok := h.leaseOf("N1"); ok {
		t.Fatalf("N1 выдана, хотя слоты (1) < живых аренд (2)")
	}
	if got := h.queueReason("N1"); got == "" {
		t.Fatal("у N1 нет ineligible_reason (должен быть слоты/недоступность)")
	}
	// Живые аренды не сняты принудительно — «отживают» сами.
	if _, ok := h.leaseOf("L1"); !ok {
		t.Fatal("L1: живая аренда снята, хотя слоты уменьшены")
	}
	if _, ok := h.leaseOf("L2"); !ok {
		t.Fatal("L2: живая аренда снята, хотя слоты уменьшены")
	}
}

// runningWithLease — RUNNING-сессия с активной арендой и открытым ходом.
func runningWithLease(t *testing.T, h *harn, sid, host, server string) {
	t.Helper()
	h.addSession(sid, sid, host, model.SessionRunning, model.ClassNormal, model.NoConstraint)
	if _, err := h.st.LeaseCreate(model.Lease{SID: sid, Server: server, Slot: 1,
		State: model.LeaseActive, Origin: model.LeaseOriginImplicit, GrantedAt: h.clk.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.st.TurnStart(sid, h.clk.Now()); err != nil {
		t.Fatal(err)
	}
}

// TestTE_C2 — кодер завис: BUSY без запросов turn.tool_hold_max_sec →
// DETACHED (аренда снята TOOL_TIMEOUT).
func TestTE_C2(t *testing.T) {
	h := newHarn(t, []ServerView{srv("s", 10, 1)})
	runningWithLease(t, h, "s1", "host", "s")
	h.setPane("s1", "host", model.PaneBusy, 1, false)
	toolHold := time.Duration(h.cfg.Turn.ToolHoldMaxSec) * time.Second
	h.advance(toolHold + time.Second)
	h.tick()
	if st := h.sessionState("s1"); st != model.SessionDetached {
		t.Fatalf("state=%s, хочу DETACHED (BUSY без запросов tool_hold_max_sec)", st)
	}
	if _, ok := h.leaseOf("s1"); ok {
		t.Fatal("аренда не снята (TOOL_TIMEOUT)")
	}
}

// TestTE_C3 — запрос разрешения: панель PROMPT approval_hold_max_sec →
// DETACHED (аренда снята APPROVAL_TIMEOUT).
func TestTE_C3(t *testing.T) {
	h := newHarn(t, []ServerView{srv("s", 10, 1)})
	runningWithLease(t, h, "s1", "host", "s")
	h.setPane("s1", "host", model.PanePrompt, 1, false)
	approval := time.Duration(h.cfg.Turn.ApprovalHoldMaxSec) * time.Second
	h.advance(approval + time.Second)
	h.tick()
	if st := h.sessionState("s1"); st != model.SessionDetached {
		t.Fatalf("state=%s, хочу DETACHED (PROMPT approval_hold_max_sec)", st)
	}
	if _, ok := h.leaseOf("s1"); ok {
		t.Fatal("аренда не снята (APPROVAL_TIMEOUT)")
	}
}

// TestTE_C5 — экран не распознан: панель UNKNOWN unknown_max_sec →
// HOLD(PANE_UNKNOWN), аренда снята, ход остаётся открытым.
func TestTE_C5(t *testing.T) {
	h := newHarn(t, []ServerView{srv("s", 10, 1)})
	runningWithLease(t, h, "s1", "host", "s")
	h.setPane("s1", "host", model.PaneUnknown, 1, false)
	umax := time.Duration(h.cfg.Turn.UnknownMaxSec) * time.Second
	h.advance(umax + time.Second)
	h.tick()
	if st := h.sessionState("s1"); st != model.SessionHold {
		t.Fatalf("state=%s, хочу HOLD", st)
	}
	if hr := h.session("s1").HoldReason; hr != model.HoldPaneUnknown {
		t.Fatalf("hold_reason=%s, хочу PANE_UNKNOWN", hr)
	}
	if _, ok := h.leaseOf("s1"); ok {
		t.Fatal("PANE_UNKNOWN: аренда не снята")
	}
}

// TestTE_C9 — панель закрыта вне runpilot: панель исчезла → GONE, аренда снята.
func TestTE_C9(t *testing.T) {
	h := newHarn(t, []ServerView{srv("s", 10, 1)})
	runningWithLease(t, h, "s1", "host", "s")
	h.sch.PaneGone("p-s1", "s1")
	if st := h.sessionState("s1"); st != model.SessionGone {
		t.Fatalf("state=%s, хочу GONE (панель закрыта вне runpilot)", st)
	}
	if _, ok := h.leaseOf("s1"); ok {
		t.Fatal("GONE: аренда не снята")
	}
}

// TestTE_C13 — ошибки подряд: attempts > scheduler.auto_requeue_max после
// transient-ошибки → HOLD(REQUEUE_LIMIT).
func TestTE_C13(t *testing.T) {
	h := newHarn(t, []ServerView{srv("s", 10, 1)})
	runningWithLease(t, h, "s1", "host", "s")
	if err := h.st.SetSessionAttempts("s1", h.cfg.Scheduler.AutoRequeueMax); err != nil {
		t.Fatal(err)
	}
	now := h.clk.Now()
	if err := h.st.RequestRecord(model.Request{
		SID: "s1", Server: "s", Path: "v1/chat/completions", Status: 500,
		TStart: now, TFirstByte: now.Add(500 * time.Millisecond), TEnd: now.Add(time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	// Запрос пишется асинхронно: дождаться коммита, чтобы завершение хода
	// увидело 500 как последний генерирующий запрос (иначе ход сочтён OK).
	waitLastGenerative(t, h, "s1", 500)
	h.sch.RequestTick("s1", true)
	h.sch.RequestTick("s1", false)
	h.setPane("s1", "host", model.PaneIdle, 1, false)
	quiet := time.Duration(h.cfg.Turn.DoneQuietSec) * time.Second
	stable := time.Duration(h.cfg.Turn.DoneStableSec) * time.Second
	h.advance(quiet + stable + time.Second)
	h.tick()
	if st := h.sessionState("s1"); st != model.SessionHold {
		t.Fatalf("state=%s, хочу HOLD", st)
	}
	if hr := h.session("s1").HoldReason; hr != model.HoldRequeueLimit {
		t.Fatalf("hold_reason=%s, хочу REQUEUE_LIMIT", hr)
	}
}

// waitLastGenerative — ждать, пока асинхронная запись запроса со статусом
// status закоммичится (deadline 2 с). Нужно, чтобы завершение хода (которое
// читает LastGenerative из БД) увидело запрос, а не сочло ход OK.
func waitLastGenerative(t *testing.T, h *harn, sid string, status int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		last, _ := h.st.LastGenerative(sid, time.Time{})
		if last != nil && last.Status == status {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: запрос %d не закоммичен за 2 с", sid, status)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestTE_N1 — узел отключился: без трафика turn.node_lost_release_sec →
// аренда RUNNING-сессии снята (NODE_LOST), сессия HOLD(NODE_LOST).
func TestTE_N1(t *testing.T) {
	h := newHarn(t, []ServerView{srv("s", 10, 1)})
	runningWithLease(t, h, "s1", "node1", "s")
	h.setPane("s1", "node1", model.PaneBusy, 1, false)
	h.sch.NodeLost("node1")
	rel := time.Duration(h.cfg.Turn.NodeLostReleaseSec) * time.Second
	h.advance(rel + time.Second)
	h.tick()
	if st := h.sessionState("s1"); st != model.SessionHold {
		t.Fatalf("state=%s, хочу HOLD", st)
	}
	if hr := h.session("s1").HoldReason; hr != model.HoldNodeLost {
		t.Fatalf("hold_reason=%s, хочу NODE_LOST", hr)
	}
	if _, ok := h.leaseOf("s1"); ok {
		t.Fatal("NODE_LOST: аренда не снята")
	}
}

// TestTE_N3 — tmux-сервер перезапущен: все панели узла пропали в одном
// скане → сессии GONE, аренды сняты.
func TestTE_N3(t *testing.T) {
	h := newHarn(t, []ServerView{srv("s", 10, 2)})
	for _, sid := range []string{"a", "b"} {
		runningWithLease(t, h, sid, "node1", "s")
	}
	// Все панели узла исчезли разом.
	h.sch.PaneGone("p-a", "a")
	h.sch.PaneGone("p-b", "b")
	for _, sid := range []string{"a", "b"} {
		if st := h.sessionState(sid); st != model.SessionGone {
			t.Fatalf("%s: state=%s, хочу GONE (tmux перезапущен)", sid, st)
		}
		if _, ok := h.leaseOf(sid); ok {
			t.Fatalf("%s: GONE, аренда не снята", sid)
		}
	}
}

// TestTE_C1 — кодер завершился/убит (@runpilot_exit, SIGKILL=137): аренда снята
// AGENT_EXITED, ход LOST, сессия HOLD(AGENT_EXITED).
func TestTE_C1(t *testing.T) {
	h := runningWithTurn(t, "s1")
	h.sch.AgentExit("s1", 137)
	if st := h.sessionState("s1"); st != model.SessionHold {
		t.Fatalf("state=%s, хочу HOLD", st)
	}
	if hr := h.session("s1").HoldReason; hr != model.HoldAgentExited {
		t.Fatalf("hold_reason=%s, хочу AGENT_EXITED", hr)
	}
	if _, ok := h.leaseOf("s1"); ok {
		t.Fatal("аренда не снята (AGENT_EXITED)")
	}
}

// TestTE_C4 — служебный экран: панель WAIT_UI → submit не диспетчеризуется
// (ineligible_reason = wait_ui).
func TestTE_C4(t *testing.T) {
	h := newHarn(t, []ServerView{srv("s", 10, 1)})
	h.addSession("E", "E", "host", model.SessionQueued, model.ClassNormal, model.NoConstraint)
	h.setPane("E", "host", model.PaneWaitUI, 1, false)
	h.enqueue("E", model.ClassNormal, model.NoConstraint, model.QueueModeSubmit)
	h.advance(3 * time.Second)
	h.tick()
	if _, ok := h.leaseOf("E"); ok {
		t.Fatal("E выдана при WAIT_UI (submit не должен диспетчеризоваться)")
	}
	if got := h.queueReason("E"); got != model.ReasonWaitUI {
		t.Fatalf("reason=%q, хочу wait_ui", got)
	}
}

// TestTE_C7 — оператор печатает в tmux: хеш экрана изменился (PANE_CHANGED)
// → аренда снята, запись возвращена в очередь, attempts без изменения.
func TestTE_C7(t *testing.T) {
	h := newHarn(t, []ServerView{srv("s", 10, 1)})
	h.grantPending("s1")
	h.sch.InjectReply("s1", proto.Msg{Result: proto.ResPaneChanged}, nil)
	if _, ok := h.leaseOf("s1"); ok {
		t.Fatal("PANE_CHANGED: аренда не снята")
	}
	if st := h.sessionState("s1"); st != model.SessionQueued {
		t.Fatalf("state=%s, хочу QUEUED (экран изменился — повторите)", st)
	}
	if a := h.session("s1").Attempts; a != 0 {
		t.Fatalf("attempts=%d, хочу 0 (PANE_CHANGED не считается ошибкой)", a)
	}
}

// TestTE_N2 — узел вернулся: после NodeLost+снимания аренды и NodeSeen
// оператор resume → сессия снова выдана в аренду.
func TestTE_N2(t *testing.T) {
	h := newHarn(t, []ServerView{srv("s", 10, 1)})
	runningWithLease(t, h, "s1", "h1", "s")
	h.setPane("s1", "h1", model.PaneBusy, 1, false)
	h.sch.NodeLost("h1")
	h.advance(time.Duration(h.cfg.Turn.NodeLostReleaseSec)*time.Second + time.Second)
	h.tick()
	if st := h.sessionState("s1"); st != model.SessionHold {
		t.Fatalf("после NodeLost: want HOLD, got %s", st)
	}
	// Узел вернулся, оператор повторяет ход (Requeue: HOLD → очередь).
	h.sch.NodeSeen("h1")
	h.idlePane("s1", "h1")
	if err := h.sch.Requeue("s1", false); err != nil {
		t.Fatalf("Requeue: %v", err)
	}
	h.advance(time.Second)
	h.tick() // выдача (аренда PENDING, сессия DISPATCHING)
	if _, ok := h.leaseOf("s1"); !ok {
		t.Fatalf("после NodeSeen+Requeue: аренда не выдана (reason=%q)", h.queueReason("s1"))
	}
	// Узел подтверждает старт → RUNNING.
	h.sch.InjectReply("s1", proto.Msg{Result: proto.ResAlreadyBusy}, nil)
	if st := h.sessionState("s1"); st != model.SessionRunning {
		t.Fatalf("после NodeSeen+resume: want RUNNING, got %s", st)
	}
}

// TestTE_C14 — долгое ожидание в очереди: QUEUED дольше
// notify.alerts.queue_wait_min → уведомление «Ждёт N мин: why.text».
func TestTE_C14(t *testing.T) {
	h := newHarn(t, []ServerView{srv("s", 10, 1)})
	var notified []string
	h.sch.notify = func(text string) { notified = append(notified, text) }
	// Слот занят → запись E ждёт в очереди.
	h.addSession("holder", "holder", "host", model.SessionRunning, model.ClassNormal, model.NoConstraint)
	if _, err := h.st.LeaseCreate(model.Lease{SID: "holder", Server: "s", Slot: 1,
		State: model.LeaseActive, Origin: model.LeaseOriginImplicit, GrantedAt: h.clk.Now()}); err != nil {
		t.Fatal(err)
	}
	h.addSession("E", "E", "host", model.SessionQueued, model.ClassNormal, model.NoConstraint)
	h.idlePane("E", "host")
	h.enqueue("E", model.ClassNormal, model.NoConstraint, model.QueueModeSubmit)
	h.advance(3 * time.Second)
	h.tick()
	if _, ok := h.leaseOf("E"); ok {
		t.Fatal("E выдана, а слот занят (должна ждать)")
	}
	// Ждём дольше queue_wait_min (10 мин).
	h.advance(11 * time.Minute)
	h.tick()
	found := false
	for _, n := range notified {
		if strings.Contains(n, "ждёт в очереди") {
			found = true
		}
	}
	if !found {
		t.Fatalf("нет уведомления о долгом ожидании: %v", notified)
	}
}

// TestTE_K2 — kill -9 посреди хода (правило 6.5): габ > heartbeat → ход
// LOST = ERROR_TRANSIENT, аренда снята, сессия → очередь (resume), без
// двойной аренды после перезапуска.
func TestTE_K2(t *testing.T) {
	h := crashRunning(t, "s1")
	hb := time.Duration(h.cfg.Coordinator.HeartbeatSec) * time.Second
	if err := h.st.SetLastAliveAt(h.clk.Now().Add(-hb - 2*time.Second)); err != nil {
		t.Fatal(err)
	}
	lost, err := h.sch.RecoverFromCrash()
	if err != nil {
		t.Fatal(err)
	}
	if lost != 1 {
		t.Fatalf("lost=%d, хочу 1", lost)
	}
	if _, ok := h.leaseOf("s1"); ok {
		t.Fatal("после recovery: аренда не снята (двойная аренда)")
	}
	if st := h.sessionState("s1"); st != model.SessionQueued {
		t.Fatalf("state=%s, хочу QUEUED (resume)", st)
	}
	e, ok := h.queueEntry("s1")
	if !ok || e.Mode != model.QueueModeResume {
		t.Fatalf("queue entry: ok=%v mode=%q, хочу resume", ok, e.Mode)
	}
}

// TestTE_K3 — потеря питания: после рестарта (6.5) ход LOST → RESUME; адрес
// узла (Tailscale) появляется позже → узел переподключается, сессия выдана.
func TestTE_K3(t *testing.T) {
	h := crashRunning(t, "s1")
	hb := time.Duration(h.cfg.Coordinator.HeartbeatSec) * time.Second
	if err := h.st.SetLastAliveAt(h.clk.Now().Add(-hb - 2*time.Second)); err != nil {
		t.Fatal(err)
	}
	lost, err := h.sch.RecoverFromCrash()
	if err != nil {
		t.Fatal(err)
	}
	if lost != 1 {
		t.Fatalf("lost=%d, хочу 1", lost)
	}
	if st := h.sessionState("s1"); st != model.SessionQueued {
		t.Fatalf("после recovery: state=%s, хочу QUEUED (resume)", st)
	}
	// Узел (Tailscale) переподключился: панель жива, сессия выдана снова.
	h.sch.NodeSeen("host")
	h.idlePane("s1", "host")
	h.advance(time.Second)
	h.tick()
	if _, ok := h.leaseOf("s1"); !ok {
		t.Fatalf("после переподключения узла: аренда не выдана (reason=%q)", h.queueReason("s1"))
	}
	h.sch.InjectReply("s1", proto.Msg{Result: proto.ResAlreadyBusy}, nil)
	if st := h.sessionState("s1"); st != model.SessionRunning {
		t.Fatalf("после переподключения: state=%s, хочу RUNNING", st)
	}
}

// TestTE_O8 — аварийная остановка (6.2): все идущие ходы прерваны, сессии →
// HOLD(EMERGENCY).
func TestTE_O8(t *testing.T) {
	h := newHarn(t, []ServerView{srv("s", 10, 2)})
	runningWithLease(t, h, "A", "host", "s")
	runningWithLease(t, h, "B", "host", "s")
	if err := h.sch.SetEmergency(); err != nil {
		t.Fatal(err)
	}
	for _, sid := range []string{"A", "B"} {
		if st := h.sessionState(sid); st != model.SessionHold {
			t.Fatalf("%s: state=%s, хочу HOLD", sid, st)
		}
		if hr := h.session(sid).HoldReason; hr != model.HoldEmergency {
			t.Fatalf("%s: hold_reason=%s, хочу EMERGENCY", sid, hr)
		}
	}
	if m := h.sch.Mode(); m != string(model.ModeEmergency) {
		t.Fatalf("mode=%q, хочу EMERGENCY", m)
	}
}

// TestTE_K5 — безопасный режим (6.4): ошибка записи SQLite → новые аренды
// не выдаются; после успешной пробной записи (≤ safe_probe_sec) режим снят
// и выдачи восстановлены.
func TestTE_K5(t *testing.T) {
	h := newHarn(t, []ServerView{srv("s", 10, 1)})
	// Внедрённая ошибка записи → SAFE_MODE.
	h.sch.onStoreWriteError(errors.New("disk full"))
	if m := h.sch.Mode(); m != string(model.ModeSafeMode) {
		t.Fatalf("mode=%q, хочу SAFE_MODE", m)
	}
	// Новая запись — в SAFE_MODE не выдаётся.
	h.addSession("N1", "N1", "host", model.SessionQueued, model.ClassNormal, model.NoConstraint)
	h.idlePane("N1", "host")
	h.enqueue("N1", model.ClassNormal, model.NoConstraint, model.QueueModeSubmit)
	h.advance(3 * time.Second)
	h.tick()
	if _, ok := h.leaseOf("N1"); ok {
		t.Fatal("SAFE_MODE: новая аренда выдана (должна быть заблокирована)")
	}
	// Устранение: пробная запись успешна → выход ≤ safe_probe_sec.
	h.advance(time.Duration(h.cfg.Monitor.SafeProbeSec)*time.Second + time.Second)
	h.tick()
	if m := h.sch.Mode(); m != string(model.ModeNormal) {
		t.Fatalf("после safe_probe: mode=%q, хочу NORMAL", m)
	}
	// Выдачи восстановлены.
	for i := 0; i < 20 && h.sessionState("N1") != model.SessionRunning; i++ {
		h.advance(200 * time.Millisecond)
		h.tick()
	}
	if _, ok := h.leaseOf("N1"); !ok {
		t.Fatalf("после выхода из SAFE_MODE: аренда не восстановлена (reason=%q)", h.queueReason("N1"))
	}
}

// TestTE_K8 — монотонные часы: скачок времени вперёд (аналог NTP-шага)
// не ломает планировщик — not_before соблюдается, выдача происходит ровно
// когда время прошло.
func TestTE_K8(t *testing.T) {
	h := newHarn(t, []ServerView{srv("s", 10, 1)})
	h.addSession("s1", "s1", "host", model.SessionQueued, model.ClassNormal, model.NoConstraint)
	h.idlePane("s1", "host")
	h.enqueue("s1", model.ClassNormal, model.NoConstraint, model.QueueModeSubmit)
	// not_before = +1ч: до этого выдачи нет.
	if e, err := h.st.QueueGet("s1"); err == nil {
		e.NotBefore = h.clk.Now().Add(time.Hour)
		if err := h.st.QueueUpsert(*e); err != nil {
			t.Fatal(err)
		}
	}
	h.advance(30 * time.Minute)
	h.idlePane("s1", "host") // свежий снимок, чтобы не stale_snapshot
	h.tick()
	if _, ok := h.leaseOf("s1"); ok {
		t.Fatal("выдано раньше not_before (+1ч)")
	}
	// Скачок вперёд за not_before → выдача.
	h.advance(2 * time.Hour)
	h.idlePane("s1", "host") // свежий снимок после скачка
	h.advance(5 * time.Second) // стабильность хеша
	h.tick()
	if _, ok := h.leaseOf("s1"); !ok {
		t.Fatalf("после скачка за not_before: аренда не выдана (reason=%q)", h.queueReason("s1"))
	}
}

// TestTE_X3 — cron runpilot exec: задание вида JOB попадает в очередь и
// получает слот (JOB-аренда).
func TestTE_X3(t *testing.T) {
	h := newHarn(t, []ServerView{srv("s", 10, 2)})
	h.addJobSession("J0", model.SessionQueued)
	h.enqueue("J0", model.ClassNormal, model.NoConstraint, model.QueueModeSubmit)
	h.advance(3 * time.Second)
	h.tick()
	if _, ok := h.leaseOf("J0"); !ok {
		t.Fatalf("JOB (cron runpilot exec) не выдан (reason=%q)", h.queueReason("J0"))
	}
	if st := h.sessionState("J0"); st != model.SessionRunning {
		t.Fatalf("JOB: state=%s, хочу RUNNING", st)
	}
}

// TestTE_X5 — клиент runpilot exec пропал: нет пульса heartbeat_timeout_sec →
// аренда CLIENT_LOST, сессия GONE, без перепостановки.
func TestTE_X5(t *testing.T) {
	h := newHarn(t, []ServerView{srv("s", 10, 2)})
	h.cfg.Jobs.HeartbeatTimeoutSec = 1
	snap, err := h.cfg.WorkingSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.st.SaveWorkingSettings(snap, "test", "test", h.clk.Now()); err != nil {
		t.Fatal(err)
	}
	sid := "JX5"
	h.addJobSession(sid, model.SessionQueued)
	h.enqueue(sid, model.ClassNormal, model.NoConstraint, model.QueueModeSubmit)
	h.tick()
	if _, ok := h.leaseOf(sid); !ok {
		t.Fatalf("X5: JOB не выдан (reason=%q)", h.queueReason(sid))
	}
	if err := h.st.JobBeat(sid, h.clk.Now()); err != nil {
		t.Fatal(err)
	}
	h.advance(2 * time.Second) // heartbeat_timeout_sec=1 истёк
	h.tick()
	if _, ok := h.leaseOf(sid); ok {
		t.Fatal("X5: аренда не снята после CLIENT_LOST")
	}
	if st := h.sessionState(sid); st != model.SessionGone {
		t.Fatalf("X5: state=%s, хочу GONE", st)
	}
}
