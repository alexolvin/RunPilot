package scheduler

// Тесты поведения QueueUpsert (перезапись) и QueueUpsertHeld (слияние).

import (
	"testing"
	"time"

	"runpilot/internal/model"
)

// QueueUpsert (общий путь) — перезапись класса: normal→low.
func TestQueueUpsertClassDowngrade(t *testing.T) {
	h := newHarn(t, []ServerView{srv("s", 10, 1)})
	h.addSession("s1", "s1", "host", model.SessionQueued, model.ClassNormal, model.NoConstraint)
	// Исходная запись: class=NORMAL.
	h.enqueue("s1", model.ClassNormal, model.NoConstraint, model.QueueModeSubmit)
	// Перепостановка: class=LOW (перезапись, не MIN).
	_ = h.st.QueueUpsert(model.QueueEntry{
		SID: "s1", Class: model.ClassLow, EnqueuedAt: h.clk.Now(),
		NotBefore: h.clk.Now(), Constraint: model.NoConstraint,
		Mode: model.QueueModeSubmit,
	})
	e, ok := h.queueEntry("s1")
	if !ok {
		t.Fatal("запись потеряна")
	}
	if e.Class != model.ClassLow {
		t.Fatalf("class: %d, хочу LOW=%d (перезапись, не MIN)", e.Class, model.ClassLow)
	}
}

// Requeue(back=true) — enqueued_at=now (конец класса), перезапись.
func TestRequeueBackEndOfClass(t *testing.T) {
	h := newHarn(t, []ServerView{srv("s", 10, 1)})
	s := "s1"
	h.addSession(s, s, "host", model.SessionHold, model.ClassNormal, model.NoConstraint)
	// Исходная запись: enqueued_at = T1 (старая).
	t1 := h.clk.Now().Add(-10 * time.Minute)
	_ = h.st.QueueUpsert(model.QueueEntry{
		SID: s, Class: model.ClassNormal, EnqueuedAt: t1,
		NotBefore: t1, Constraint: model.NoConstraint, Mode: model.QueueModeSubmit,
	})
	// Requeue(back=true): enqueued_at = now.
	if err := h.sch.Requeue(s, true); err != nil {
		t.Fatalf("Requeue: %v", err)
	}
	e, ok := h.queueEntry(s)
	if !ok {
		t.Fatal("запись потеряна")
	}
	if !e.EnqueuedAt.After(t1) {
		t.Fatalf("enqueued_at: %v, должен быть > %v (requeue --back)", e.EnqueuedAt, t1)
	}
}

// Attempts обнуляется после хода OK.
func TestAttemptsResetAfterOK(t *testing.T) {
	h := newHarn(t, []ServerView{srv("s", 10, 1)})
	s := "s1"
	h.addSession(s, s, "host", model.SessionRunning, model.ClassNormal, model.NoConstraint)
	//_attempts = 3 (до хода).
	_ = h.st.SetSessionAttempts(s, 3)
	// Аренда + ход.
	if _, err := h.st.LeaseCreate(model.Lease{SID: s, Server: "s", Slot: 1,
		State: model.LeaseActive, Origin: model.LeaseOriginImplicit, GrantedAt: h.clk.Now()}); err != nil {
		t.Fatal(err)
	}
	turnStart := h.clk.Now()
	if _, err := h.st.TurnStart(s, turnStart); err != nil {
		t.Fatal(err)
	}
	h.idlePane(s, "host")
	// Завершаем ход (OK).
	stable := time.Duration(h.cfg.Turn.DoneStableSec) * time.Second
	h.advance(stable + time.Second)
	h.tick()
	// attempts = 0.
	sess, err := h.st.GetSession(s)
	if err != nil {
		t.Fatal(err)
	}
	if sess.Attempts != 0 {
		t.Fatalf("attempts: %d, хочу 0 (сброс после OK)", sess.Attempts)
	}
}

// QueueUpsertHeld — слияние (MIN/MAX): enqueued_at=MIN, attempts=MAX, class=MIN.
func TestQueueUpsertHeldMerge(t *testing.T) {
	h := newHarn(t, []ServerView{srv("s", 10, 1)})
	s := "s1"
	h.addSession(s, s, "host", model.SessionIdle, model.ClassNormal, model.NoConstraint)
	// Исходная запись: enqueued_at=T1, attempts=3, class=NORMAL.
	t1 := h.clk.Now().Add(-10 * time.Second)
	_ = h.st.QueueUpsertHeld(model.QueueEntry{
		SID: s, Class: model.ClassNormal, EnqueuedAt: t1,
		NotBefore: t1, Constraint: model.NoConstraint,
		Mode: model.QueueModeSubmit, HeldRequest: true, Attempts: 3,
	})
	// Повтор (holdPass): enqueued_at=now, attempts=0, class=HIGH.
	_ = h.st.QueueUpsertHeld(model.QueueEntry{
		SID: s, Class: model.ClassHigh, EnqueuedAt: h.clk.Now(),
		NotBefore: h.clk.Now(), Constraint: model.NoConstraint,
		Mode: model.QueueModeSubmit, HeldRequest: true, Attempts: 0,
	})
	e, ok := h.queueEntry(s)
	if !ok {
		t.Fatal("запись потеряна")
	}
	// enqueued_at = MIN(T1, now) = T1.
	if !e.EnqueuedAt.Equal(t1) && !e.EnqueuedAt.Before(t1.Add(time.Second)) {
		t.Fatalf("enqueued_at: %v, хочу ≈ %v (MIN)", e.EnqueuedAt, t1)
	}
	// attempts = MAX(3, 0) = 3.
	if e.Attempts != 3 {
		t.Fatalf("attempts: %d, хочу 3 (MAX)", e.Attempts)
	}
	// class = MIN(NORMAL=2, HIGH=1) = HIGH=1.
	if e.Class != model.ClassHigh {
		t.Fatalf("class: %d, хочу HIGH=%d (MIN)", e.Class, model.ClassHigh)
	}
}
