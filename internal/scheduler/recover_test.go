package scheduler

// Восстановление после аварийного завершения координатора (v2 раздел 6.5,
// CONTROL 5: kill -9 посреди хода → исход LOST → RESUME).

import (
	"testing"
	"time"

	"runpilot/internal/model"
	"runpilot/internal/proto"
)

// предпосылка: сессия RUNNING с активной арендой и открытым ходом.
func crashRunning(t *testing.T, sid string) *harn {
	t.Helper()
	h := newHarn(t, []ServerView{srv("s", 10, 1)})
	h.grantPending(sid)
	h.sch.InjectReply(sid, proto.Msg{Result: proto.ResAlreadyBusy}, nil)
	if st := h.sessionState(sid); st != model.SessionRunning {
		t.Fatalf("предпосылка: state=%s, хочу RUNNING", st)
	}
	if _, ok := h.leaseOf(sid); !ok {
		t.Fatalf("предпосылка: нет активной аренды %s", sid)
	}
	return h
}

// CONTROL 5 (6.5): kill -9 посреди хода — габ по last_alive_at > heartbeat →
// ход LOST = ERROR_TRANSIENT: аренда снята (CRASH), ход LOST, attempts+=1,
// сессия → очередь (resume).
func TestRecoverFromCrashLost(t *testing.T) {
	h := crashRunning(t, "s1")
	// Координатор «умер»: последний пульс раньше, чем heartbeat-габ.
	hb := time.Duration(h.cfg.Coordinator.HeartbeatSec) * time.Second
	if hb <= 0 {
		hb = 5 * time.Second
	}
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
		t.Fatal("после recovery: аренда не снята")
	}
	if st := h.sessionState("s1"); st != model.SessionQueued {
		t.Fatalf("после recovery: state=%s, хочу QUEUED (resume)", st)
	}
	if a := h.session("s1").Attempts; a != 1 {
		t.Fatalf("attempts=%d, хочу 1", a)
	}
	if e, ok := h.queueEntry("s1"); !ok || e.Mode != model.QueueModeResume {
		t.Fatalf("queue entry: ok=%v mode=%q, хочу resume", ok, e.Mode)
	}
	turns, err := h.st.TurnPage(0, 50)
	if err != nil {
		t.Fatal(err)
	}
	lostTurn := false
	for _, tr := range turns {
		if tr.SID == "s1" && tr.Outcome == model.TurnLost {
			lostTurn = true
		}
	}
	if !lostTurn {
		t.Fatal("хода с исходом LOST нет")
	}
}

// CONTROL 5 (6.5): чистое завершение (пульс свежий, габ ≤ heartbeat) —
// ничего не меняется.
func TestRecoverFromCrashNoGap(t *testing.T) {
	h := crashRunning(t, "s1")
	if err := h.st.SetLastAliveAt(h.clk.Now()); err != nil {
		t.Fatal(err)
	}
	lost, err := h.sch.RecoverFromCrash()
	if err != nil {
		t.Fatal(err)
	}
	if lost != 0 {
		t.Fatalf("lost=%d, хочу 0 (нет габ)", lost)
	}
	if _, ok := h.leaseOf("s1"); !ok {
		t.Fatal("нет габ: аренда не должна сниматься")
	}
	if st := h.sessionState("s1"); st != model.SessionRunning {
		t.Fatalf("нет габ: state=%s, хочу RUNNING (без изменений)", st)
	}
}

// 6.5.3: ход завершён 2xx ДО last_alive_at − done_quiet_sec → OK (аренда
// снята TURN_DONE, сессия IDLE), а не LOST.
func TestRecoverFromCrashTurnOK(t *testing.T) {
	h := crashRunning(t, "s1")
	hb := time.Duration(h.cfg.Coordinator.HeartbeatSec) * time.Second
	if hb <= 0 {
		hb = 5 * time.Second
	}
	last := h.clk.Now().Add(-hb - 2*time.Second)
	// Последний запрос хода: 2xx, завершился до cutoff.
	quiet := time.Duration(h.cfg.Turn.DoneQuietSec) * time.Second
	if quiet <= 0 {
		quiet = 3 * time.Second
	}
	if err := h.st.RequestRecord(model.Request{
		SID: "s1", Server: "s", Path: "/v1/chat/completions", Status: 200,
		TStart: last.Add(-30 * time.Second), TFirstByte: last.Add(-29 * time.Second),
		TEnd: last.Add(-quiet - time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.st.SetLastAliveAt(last); err != nil {
		t.Fatal(err)
	}

	lost, err := h.sch.RecoverFromCrash()
	if err != nil {
		t.Fatal(err)
	}
	if lost != 0 {
		t.Fatalf("lost=%d, хочу 0 (ход завершён OK)", lost)
	}
	if _, ok := h.leaseOf("s1"); ok {
		t.Fatal("OK-ход: аренда не снята")
	}
	if st := h.sessionState("s1"); st != model.SessionIdle {
		t.Fatalf("OK-ход: state=%s, хочу IDLE", st)
	}
}
