package scheduler

// П.5 — нижние границы закрытия хода (виртуальные часы): ход НЕ закрывается
// раньше (a) last_request_end + done_quiet_sec (при reqCount>0) и
// (b) stable_since (hashSince) + done_stable_sec. Каждую границу проверяем
// изолированно: одна уже выполнена, вторая — на грани.
//
// checkTurn (complete.go): p1 панель IDLE; p2 inflight=0;
// p3 reqCount>0 → now-lastEnd >= doneQuiet; p4 now-hashSince >= doneStable.

import (
	"testing"
	"time"

	"runpilot/internal/model"
)

// newLowerBoundHarn — RUNNING-сессия с арендой, открытым ходом, IDLE-панелью
// (hashSince = 0s) и завершённым запросом (reqCount>0, lastEnd задаётся).
func newLowerBoundHarn(t *testing.T) *harn {
	t.Helper()
	h := newHarn(t, []ServerView{srv("s", 10, 1)})
	runningWithLease(t, h, "s1", "host", "s")
	h.setPane("s1", "host", model.PaneIdle, 1, false) // hashSince = 0s
	return h
}

// finishRequestAt — запрос, завершающийся в текущий момент (lastEnd = now).
func finishRequestAt(t *testing.T, h *harn, sid string) {
	t.Helper()
	now := h.clk.Now()
	h.sch.RequestTick(sid, true)
	if err := h.st.RequestRecord(model.Request{
		SID: sid, Server: "s", Path: "v1/chat/completions", Status: 200,
		TStart: now.Add(-time.Second), TFirstByte: now.Add(-500 * time.Millisecond), TEnd: now,
	}); err != nil {
		t.Fatal(err)
	}
	waitLastGenerative(t, h, sid, 200)
	h.sch.RequestTick(sid, false) // inflight=0, lastReqEnd[sid] = now
}

func TestTurnCloseQuietLowerBound(t *testing.T) {
	// (a) quiet-граница доминирует: stable_since ранний (0s), lastEnd=3s.
	// doneQuiet=doneStable=5s → ход закроется на lastEnd+5s=8s, а не раньше.
	h := newLowerBoundHarn(t)
	q := time.Duration(h.cfg.Turn.DoneQuietSec) * time.Second // 5s
	h.advance(3 * time.Second)
	finishRequestAt(t, h, "s1") // lastEnd = 3s, hashSince = 0s
	// now=3s → +4s = 7s: quiet 7-3=4s < 5s (не), stable 7-0=7s >= 5s (да) → НЕ закрыт.
	h.advance(q - time.Second) // +4s → now=7s
	h.tick()
	if _, ok := h.leaseOf("s1"); !ok {
		t.Fatalf("(a) аренда снята на 7s, раньше quiet-границы lastEnd+%d=8s", h.cfg.Turn.DoneQuietSec)
	}
	// +1s → now=8s: quiet 8-3=5s >= 5s, stable 8-0=8s >= 5s → закрыт.
	h.advance(time.Second)
	h.tick()
	if _, ok := h.leaseOf("s1"); ok {
		t.Fatal("(a) аренда не снята на lastEnd+done_quiet_sec (8s)")
	}
}

func TestTurnCloseStableLowerBound(t *testing.T) {
	// (b) stable-граница доминирует: lastEnd=0s (ранний), stable_since=3s.
	// doneQuiet=doneStable=5s → ход закроется на stableSince+5s=8s, а не раньше.
	h := newLowerBoundHarn(t)
	s := time.Duration(h.cfg.Turn.DoneStableSec) * time.Second // 5s
	finishRequestAt(t, h, "s1") // lastEnd = 0s, hashSince = 0s
	// now=0s → +3s = 3s: панель с НОВЫМ hash → hashSince = 3s.
	h.advance(3 * time.Second)
	h.setPane("s1", "host", model.PaneIdle, 2, false) // hashSince = 3s (hash 1→2)
	// +3s → now=6s: quiet 6-0=6s >= 5s (да), stable 6-3=3s < 5s (не) → НЕ закрыт.
	h.advance(3 * time.Second)
	h.tick()
	if _, ok := h.leaseOf("s1"); !ok {
		t.Fatalf("(b) аренда снята на 6s, раньше stable-границы stableSince+%d=8s", h.cfg.Turn.DoneStableSec)
	}
	// +2s → now=8s: quiet 8-0=8s >= 5s, stable 8-3=5s >= 5s → закрыт.
	h.advance(2 * time.Second)
	h.tick()
	if _, ok := h.leaseOf("s1"); ok {
		t.Fatal("(b) аренда не снята на stable_since+done_stable_sec (8s)")
	}
	_ = s
}
