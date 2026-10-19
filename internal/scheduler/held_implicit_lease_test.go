package scheduler

import (
	"testing"
	"time"

	"runpilot/internal/model"
)

// TestIdleSessionActiveLease — воспроизведение stale-аренды (2026-10-15,
// аренда #531 / 1YHT3GSSYS, 3p14 «занят» NextLife): ход закрыт, сессия IDLE,
// аренда ACTIVE (неявная, от held-запроса).
//
// До фикса: checkTurn вызывается только для RUNNING/DETACHED; сессия IDLE →
// аренда не снимается (stale).
// После фикса: checkTurn к любой ACTIVE-аренде → аренда снимается по
// done_quiet_sec/done_stable_sec.
func TestIdleSessionActiveLease(t *testing.T) {
	h := newHarn(t, []ServerView{srv("a", 10, 1)})
	h.addSession("s1", "s1", "host", model.SessionIdle, model.ClassNormal, model.NoConstraint)
	// Аренда ACTIVE (неявная, от held-запроса).
	if _, err := h.st.LeaseCreate(model.Lease{SID: "s1", Server: "a", Slot: 1,
		State: model.LeaseActive, Origin: model.LeaseOriginImplicit, GrantedAt: h.clk.Now()}); err != nil {
		t.Fatal(err)
	}
	// Ход открыт.
	if _, err := h.st.TurnStart("s1", h.clk.Now()); err != nil {
		t.Fatal(err)
	}
	// Панель IDLE, хеш изменился ПОСЛЕ старта хода (анти-фантом).
	h.advance(time.Second)
	h.setPane("s1", "host", model.PaneIdle, 2, true)

	// Ждём done_quiet_sec + done_stable_sec (+ запас).
	h.advance(time.Duration(h.cfg.Turn.DoneQuietSec+h.cfg.Turn.DoneStableSec+1) * time.Second)
	h.tick()

	// Аренда снята (ход завершён), сессия IDLE.
	if l, ok := h.leaseOf("s1"); ok {
		t.Fatalf("аренда не снята (stale): %s", l.State)
	}
	if st := h.sessionState("s1"); st != model.SessionIdle {
		t.Fatalf("сессия %s, хочу IDLE (ход завершён)", st)
	}
}
