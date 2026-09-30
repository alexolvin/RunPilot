package scheduler

// Раздел 8.1 ТЗ, CONTROL 6: 20 заданий runpilot exec на jobs.slots=2 —
// одновременных JOB-аренд не больше слотов на каждом сэмпле (тик 100 мс).

import (
	"fmt"
	"testing"
	"time"

	"runpilot/internal/model"
	"runpilot/internal/store"
)

// addJobSession — сессия вида JOB в состоянии state (раздел 8.1).
func (h *harn) addJobSession(sid string, state model.SessionState) {
	h.t.Helper()
	rec := store.SessionRecord{
		SID: sid, Name: "job-" + sid, Host: "host", HostIP: "127.0.0.1",
		Profile: "qwen", Kind: model.KindJob,
		State: state, StateChangedAt: h.clk.Now(), Class: model.ClassNormal,
		CreatedAt: h.clk.Now(),
	}
	if err := h.st.CreateSession(rec); err != nil {
		h.t.Fatalf("create job %s: %v", sid, err)
	}
}

// jobLeaseCount — число активных JOB-аренд (по БД).
func (h *harn) jobLeaseCount() int {
	h.t.Helper()
	leases, err := h.st.LeaseListActive()
	if err != nil {
		h.t.Fatalf("lease list: %v", err)
	}
	n := 0
	for _, l := range leases {
		if sess, err := h.st.GetSession(l.SID); err == nil && sess.Kind == model.KindJob {
			n++
		}
	}
	return n
}

// CONTROL 6: потолок одновременных JOB-аренд = jobs.slots.
func TestJobSlotCapControl6(t *testing.T) {
	// 8 физических слотов, но jobs.slots=2 — cap обязано ограничить выдачи.
	h := newHarn(t, []ServerView{srv("s1", 10, 4), srv("s2", 10, 4)})
	h.cfg.Jobs.Slots = 2
	snap, err := h.cfg.WorkingSnapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if _, err := h.st.SaveWorkingSettings(snap, "test", "test", h.clk.Now()); err != nil {
		t.Fatalf("save working: %v", err)
	}

	const nJobs = 20
	for i := 0; i < nJobs; i++ {
		sid := fmt.Sprintf("J%03d", i)
		h.addJobSession(sid, model.SessionQueued)
		h.enqueue(sid, model.ClassNormal, model.NoConstraint, model.QueueModeSubmit)
	}

	// Тики с шагом 100 мс; на каждом сэмпле JOB-аренд <= jobs.slots.
	maxSeen := 0
	for i := 0; i < 30; i++ {
		h.tick()
		if n := h.jobLeaseCount(); n > h.cfg.Jobs.Slots {
			t.Fatalf("сэмпл %d: JOB-аренд %d > jobs.slots=%d", i, n, h.cfg.Jobs.Slots)
		}
		if n := h.jobLeaseCount(); n > maxSeen {
			maxSeen = n
		}
		h.advance(100 * time.Millisecond)
	}
	// Cap реально достигнут (не «0 выдач»): использовалось ровно jobs.slots.
	if maxSeen != 2 {
		t.Fatalf("макс. одновременных JOB-аренд = %d, хочу 2 (= jobs.slots)", maxSeen)
	}
	// Остальные 18 ждут с причиной job_slots.
	if got := h.queueReason("J002"); got != model.ReasonJobSlots {
		t.Fatalf("J002: reason=%q, хочу job_slots", got)
	}
}

// X5: клиент runpilot exec пропал — нет пульса heartbeat_timeout_sec → аренда
// CLIENT_LOST, сессия GONE, без перепостановки.
func TestJobClientLost(t *testing.T) {
	h := newHarn(t, []ServerView{srv("s1", 10, 2)})
	// heartbeat_timeout_sec = 1 (краткий для теста).
	h.cfg.Jobs.HeartbeatTimeoutSec = 1
	snap, err := h.cfg.WorkingSnapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if _, err := h.st.SaveWorkingSettings(snap, "test", "test", h.clk.Now()); err != nil {
		t.Fatalf("save working: %v", err)
	}

	h.addJobSession("J0", model.SessionQueued)
	h.enqueue("J0", model.ClassNormal, model.NoConstraint, model.QueueModeSubmit)
	h.tick() // выдача: J0 → RUNNING, активная аренда.
	if _, ok := h.leaseOf("J0"); !ok {
		t.Fatalf("J0: аренда не выдана")
	}
	// Пульс зафиксирован.
	if err := h.st.JobBeat("J0", h.clk.Now()); err != nil {
		t.Fatal(err)
	}
	// Проходит heartbeat_timeout_sec без пульса.
	h.advance(2 * time.Second)
	h.tick()
	// Аренда снята, сессия GONE, перепостановки нет.
	if _, ok := h.leaseOf("J0"); ok {
		t.Fatalf("X5: аренда не снята после timeout")
	}
	if st := h.sessionState("J0"); st != model.SessionGone {
		t.Fatalf("X5: состояние=%s, хочу GONE", st)
	}
	if e, err := h.st.QueueGet("J0"); err == nil && e.SID != "" {
		t.Fatalf("X5: запись очереди не удалена (без перепостановки)")
	}
}
