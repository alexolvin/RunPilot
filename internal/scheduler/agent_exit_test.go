package scheduler

// C1 (7.3): кодер в панели завершён/убит (@runpilot_exit) — аренда AGENT_EXITED,
// ход LOST, сессия → HOLD(AGENT_EXITED).

import (
	"testing"
	"time"

	"runpilot/internal/model"
	"runpilot/internal/proto"
)

// runningWithTurn — RUNNING-сессия с активным ходом (аренда + открытый ход).
func runningWithTurn(t *testing.T, sid string) *harn {
	t.Helper()
	h := newHarn(t, []ServerView{srv("s", 10, 1)})
	h.grantPending(sid)
	h.sch.InjectReply(sid, proto.Msg{Result: proto.ResAlreadyBusy}, nil)
	if st := h.sessionState(sid); st != model.SessionRunning {
		t.Fatalf("предпосылка: state=%s, хочу RUNNING", st)
	}
	if _, err := h.st.OpenTurn(sid); err != nil {
		t.Fatalf("предпосылка: открытого хода нет: %v", err)
	}
	return h
}

func (h *harn) turnOutcome(t *testing.T, sid string) (model.TurnOutcome, bool) {
	t.Helper()
	turns, err := h.st.ListTurnsSince(h.clk.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("turns: %v", err)
	}
	var out model.TurnOutcome
	found := false
	for _, tr := range turns {
		if tr.SID == sid {
			out, found = tr.Outcome, true
		}
	}
	return out, found
}

// TestAgentExitKill — кодер убит KILL (137 = 128+9): HOLD(AGENT_EXITED),
// аренда снята, ход LOST.
func TestAgentExitKill(t *testing.T) {
	h := runningWithTurn(t, "s1")
	h.sch.AgentExit("s1", 137)
	if st := h.sessionState("s1"); st != model.SessionHold {
		t.Fatalf("после C1: state=%s, хочу HOLD", st)
	}
	if hr := h.session("s1").HoldReason; hr != model.HoldAgentExited {
		t.Fatalf("hold_reason=%s, хочу AGENT_EXITED", hr)
	}
	if _, ok := h.leaseOf("s1"); ok {
		t.Fatal("C1: аренда не снята")
	}
	if _, err := h.st.OpenTurn("s1"); err == nil {
		t.Fatal("C1: ход не закрыт")
	}
	if oc, ok := h.turnOutcome(t, "s1"); !ok || oc != model.TurnLost {
		t.Fatalf("исход хода=%q (найдено=%v), хочу LOST", oc, ok)
	}
}

// TestAgentExitNormalCode — штатное/не-сигнальное завершение (код 1):
// те же реакции, код в сообщении.
func TestAgentExitNormalCode(t *testing.T) {
	h := runningWithTurn(t, "s1")
	h.sch.AgentExit("s1", 1)
	if hr := h.session("s1").HoldReason; hr != model.HoldAgentExited {
		t.Fatalf("hold_reason=%s, хочу AGENT_EXITED", hr)
	}
	if oc, _ := h.turnOutcome(t, "s1"); oc != model.TurnLost {
		t.Fatalf("исход хода=%q, хочу LOST", oc)
	}
}

// TestAgentExitIdempotent — повторный вызов не ломает (сессия уже HOLD),
// вызов для сессии вне хода — no-op.
func TestAgentExitIdempotent(t *testing.T) {
	h := runningWithTurn(t, "s1")
	h.sch.AgentExit("s1", 137)
	before := h.sessionState("s1")
	h.sch.AgentExit("s1", 137) // повтор
	if got := h.sessionState("s1"); got != before {
		t.Fatalf("повтор C1: state %s → %s, должен не измениться", before, got)
	}
	// сессия вне хода — no-op.
	h.addSession("s2", "s2", "host", model.SessionIdle, model.ClassNormal, model.NoConstraint)
	h.sch.AgentExit("s2", 1)
	if got := h.sessionState("s2"); got != model.SessionIdle {
		t.Fatalf("no-op: s2 state=%s, хочу IDLE (вне хода не трогаем)", got)
	}
}
