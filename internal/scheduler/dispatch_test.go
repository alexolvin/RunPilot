package scheduler

// Таблица dispatch (раздел 8 ТЗ) и правило UNKNOWN-панели:
// ход не закрывается, пока панель UNKNOWN; после unknown_max_sec —
// аренда снята, сессия HOLD(PANE_UNKNOWN), строка turn остаётся открытой.

import (
	"errors"
	"testing"
	"time"

	"runpilot/internal/model"
	"runpilot/internal/proto"
)

// grantPending — сессия выдана: аренда PENDING, сессия DISPATCHING.
func (h *harn) grantPending(sid string) {
	h.t.Helper()
	h.addSession(sid, sid, "host", model.SessionQueued, model.ClassNormal, model.NoConstraint)
	h.idlePane(sid, "host")
	h.enqueue(sid, model.ClassNormal, model.NoConstraint, model.QueueModeSubmit)
	h.advance(3 * time.Second)
	h.tick()
	if l, ok := h.leaseOf(sid); !ok || l.State != model.LeasePending {
		h.t.Fatalf("grantPending %s: аренды PENDING нет (reason=%q)", sid, h.queueReason(sid))
	}
}

// Нет ответа узла на dispatch: аренда снята, запись возвращена с прежним
// рангом, сессия QUEUED, attempts без изменения.
func TestDispatchNoReply(t *testing.T) {
	h := newHarn(t, []ServerView{srv("s", 10, 1)})
	h.grantPending("s1")
	h.sch.InjectReply("s1", proto.Msg{}, errors.New("нет ответа узла"))
	if _, ok := h.leaseOf("s1"); ok {
		t.Fatalf("нет ответа: аренда не снята")
	}
	if st := h.sessionState("s1"); st != model.SessionQueued {
		t.Fatalf("нет ответа: state=%s, хочу QUEUED", st)
	}
	if _, ok := h.queueEntry("s1"); !ok {
		t.Fatalf("нет ответа: запись не возвращена в очередь")
	}
	if a := h.session("s1").Attempts; a != 0 {
		t.Fatalf("нет ответа: attempts=%d, хочу 0", a)
	}
}

// PANE_CHANGED: аренда снята, запись в очередь с прежним рангом,
// attempts без изменения.
func TestDispatchPaneChanged(t *testing.T) {
	h := newHarn(t, []ServerView{srv("s", 10, 1)})
	h.grantPending("s1")
	h.sch.InjectReply("s1", proto.Msg{Result: proto.ResPaneChanged}, nil)
	if _, ok := h.leaseOf("s1"); ok {
		t.Fatalf("PANE_CHANGED: аренда не снята")
	}
	if st := h.sessionState("s1"); st != model.SessionQueued {
		t.Fatalf("PANE_CHANGED: state=%s, хочу QUEUED", st)
	}
	if a := h.session("s1").Attempts; a != 0 {
		t.Fatalf("PANE_CHANGED: attempts=%d, хочу 0", a)
	}
}

// EMPTY_INPUT: аренда снята, HOLD(EMPTY_INPUT).
func TestDispatchEmptyInput(t *testing.T) {
	h := newHarn(t, []ServerView{srv("s", 10, 1)})
	h.grantPending("s1")
	h.sch.InjectReply("s1", proto.Msg{Result: proto.ResEmptyInput}, nil)
	if _, ok := h.leaseOf("s1"); ok {
		t.Fatalf("EMPTY_INPUT: аренда не снята")
	}
	if st := h.sessionState("s1"); st != model.SessionHold {
		t.Fatalf("EMPTY_INPUT: state=%s, хочу HOLD", st)
	}
	if hr := h.session("s1").HoldReason; hr != model.HoldEmptyInput {
		t.Fatalf("EMPTY_INPUT: hold_reason=%s, хочу EMPTY_INPUT", hr)
	}
}

// SENT без подтверждения за start_confirm_sec → HOLD(START_NOT_CONFIRMED).
func TestStartNotConfirmed(t *testing.T) {
	h := newHarn(t, []ServerView{srv("s", 10, 1)})
	h.grantPending("s1")
	h.sch.InjectReply("s1", proto.Msg{Result: proto.ResSent}, nil)
	confirm := time.Duration(h.cfg.Dispatch.StartConfirmSec) * time.Second
	// До дедлайна: всё ещё DISPATCHING.
	h.advance(confirm - time.Second)
	h.tick()
	if st := h.sessionState("s1"); st != model.SessionDispatching {
		t.Fatalf("до дедлайна: state=%s, хочу DISPATCHING", st)
	}
	// После дедлайна: HOLD(START_NOT_CONFIRMED).
	h.advance(2 * time.Second)
	h.tick()
	if st := h.sessionState("s1"); st != model.SessionHold {
		t.Fatalf("после дедлайна: state=%s, хочу HOLD", st)
	}
	if hr := h.session("s1").HoldReason; hr != model.HoldStartNotConfirmed {
		t.Fatalf("после дедлайна: hold_reason=%s, хочу START_NOT_CONFIRMED", hr)
	}
}

// ALREADY_BUSY: старт подтверждён (клавиши не отправлялись) → RUNNING.
func TestDispatchAlreadyBusy(t *testing.T) {
	h := newHarn(t, []ServerView{srv("s", 10, 1)})
	h.grantPending("s1")
	h.sch.InjectReply("s1", proto.Msg{Result: proto.ResAlreadyBusy}, nil)
	if st := h.sessionState("s1"); st != model.SessionRunning {
		t.Fatalf("ALREADY_BUSY: state=%s, хочу RUNNING", st)
	}
	if l, ok := h.leaseOf("s1"); !ok || l.State != model.LeaseActive {
		t.Fatalf("ALREADY_BUSY: аренда должна стать ACTIVE")
	}
}

// UNKNOWN-панель: ход не закрывается, пока панель UNKNOWN; после
// unknown_max_sec аренда снята, сессия HOLD(PANE_UNKNOWN), turn открыт.
func TestUnknownPane(t *testing.T) {
	h := newHarn(t, []ServerView{srv("s", 10, 1)})
	h.addSession("s1", "s1", "host", model.SessionRunning, model.ClassNormal, model.NoConstraint)
	if _, err := h.st.LeaseCreate(model.Lease{SID: "s1", Server: "s", Slot: 1,
		State: model.LeaseActive, Origin: model.LeaseOriginImplicit, GrantedAt: h.clk.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.st.TurnStart("s1", h.clk.Now()); err != nil {
		t.Fatal(err)
	}
	h.setPane("s1", "host", model.PaneUnknown, 1, false)
	// До unknown_max: ход открыт, сессия RUNNING.
	h.advance(30 * time.Second)
	h.tick()
	if st := h.sessionState("s1"); st != model.SessionRunning {
		t.Fatalf("до unknown_max: state=%s, хочу RUNNING", st)
	}
	// После unknown_max (60с): HOLD(PANE_UNKNOWN), аренда снята.
	h.advance(30 * time.Second)
	h.tick()
	if st := h.sessionState("s1"); st != model.SessionHold {
		t.Fatalf("после unknown_max: state=%s, хочу HOLD", st)
	}
	if hr := h.session("s1").HoldReason; hr != model.HoldPaneUnknown {
		t.Fatalf("после unknown_max: hold_reason=%s, хочу PANE_UNKNOWN", hr)
	}
	if _, ok := h.leaseOf("s1"); ok {
		t.Fatalf("PANE_UNKNOWN: аренда не снята")
	}
	// Ход НЕ закрыт: строка turn остаётся открытой.
	if _, err := h.st.OpenTurn("s1"); err != nil {
		t.Fatalf("ход должен остаться открытым: %v", err)
	}
}

// NODE_LOST: узел без трафика node_lost_release_sec → аренда RUNNING-сессии
// снята, сессия HOLD(NODE_LOST).
func TestNodeLost(t *testing.T) {
	h := newHarn(t, []ServerView{srv("s", 10, 1)})
	h.addSession("s1", "s1", "node1", model.SessionRunning, model.ClassNormal, model.NoConstraint)
	if _, err := h.st.LeaseCreate(model.Lease{SID: "s1", Server: "s", Slot: 1,
		State: model.LeaseActive, Origin: model.LeaseOriginImplicit, GrantedAt: h.clk.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.st.TurnStart("s1", h.clk.Now()); err != nil {
		t.Fatal(err)
	}
	// BUSY-панель: завершение хода и TOOL_TIMEOUT (900с) не сработают до
	// node_lost_release (120с).
	h.setPane("s1", "node1", model.PaneBusy, 1, false)
	h.sch.NodeLost("node1")
	h.advance(60 * time.Second)
	h.tick()
	if st := h.sessionState("s1"); st != model.SessionRunning {
		t.Fatalf("до node_lost_release: state=%s, хочу RUNNING", st)
	}
	h.advance(60 * time.Second)
	h.tick()
	if st := h.sessionState("s1"); st != model.SessionHold {
		t.Fatalf("после node_lost_release: state=%s, хочу HOLD", st)
	}
	if hr := h.session("s1").HoldReason; hr != model.HoldNodeLost {
		t.Fatalf("hold_reason=%s, хочу NODE_LOST", hr)
	}
	if _, ok := h.leaseOf("s1"); ok {
		t.Fatalf("NODE_LOST: аренда не снята")
	}
}
