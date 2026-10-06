package scheduler

// Граница тика (фикс, Этап 2): окончание COOLDOWN — событие планировщика.
// При освобождении аренды ставится таймер internal/clock на released_at+
// cooldown_sec; его срабатывание запускает Tick, как любое другое событие.
// Выдача слота — РОВНО в released_at+cooldown_sec (не на следующем
// периодическом тике), один now на тик сохраняется.
//
// Виртуальные часы: после освобождения тест НЕ зовёт ручной tick до дедлайна —
// выдачу выполняет только таймер (без него h.advance не тикает и слот не
// выдавался бы). Поэтому GrantedAt == released_at+cooldown ровно.

import (
	"testing"
	"time"

	"runpilot/internal/model"
)

// newTickBoundaryHarn — holder A (RUNNING + ACTIVE-аренда + открытый ход +
// IDLE-панель, без запросов → closeTurn по done_stable) и удержанный
// waiter B (held_request, QUEUED). Один слот на сервере "s".
func newTickBoundaryHarn(t *testing.T) *harn {
	t.Helper()
	h := newHarn(t, []ServerView{srv("s", 10, 1)})
	runningWithLease(t, h, "A", "host", "s")
	h.setPane("A", "host", model.PaneIdle, 1, false) // hashSince = 0
	h.addSession("B", "B", "host", model.SessionQueued, model.ClassNormal, model.NoConstraint)
	h.enqueueAt("B", model.ClassNormal, model.NoConstraint, model.QueueModeSubmit, true, h.clk.Now())
	return h
}

// releaseAt — завершить ход holder'а ровно в абсолютном `at` (closeTurn →
// COOLDOWN + таймер на at+cooldown_sec); B пока не выдан.
func releaseAt(t *testing.T, h *harn, at time.Time) {
	t.Helper()
	h.advance(at.Sub(h.clk.Now()))
	h.tick() // closeTurn(A) в тике `at`
	if _, ok := h.leaseOf("A"); ok {
		t.Fatalf("аренда holder'а не снята на closeTurn (at=%v)", at)
	}
	if _, ok := h.leaseOf("B"); ok {
		t.Fatal("B выдан до конца cooldown")
	}
}

// grantAtCooldown — дойти ровно до released_at+cooldown (таймер срабатывает →
// tick → grant) и вернуть GrantedAt(B).
func grantAtCooldown(t *testing.T, h *harn, releasedAt time.Time) time.Time {
	t.Helper()
	cooldown := time.Duration(h.cfg.Scheduler.CooldownSec) * time.Second
	h.advance(cooldown) // now = releasedAt+cooldown → таймер → tick → grant
	l, ok := h.leaseOf("B")
	if !ok {
		t.Fatalf("B не выдан в released_at+cooldown (reason=%q)", h.queueReason("B"))
	}
	return l.GrantedAt
}

// TestTickBoundaryOffPhase — освобождение вне фазы тика (T = done_stable +
// tick_ms/2, не кратное tick_ms): выдача РОВНО в released_at+cooldown_sec,
// а не на следующем периоде (который был бы позже).
func TestTickBoundaryOffPhase(t *testing.T) {
	h := newTickBoundaryHarn(t)
	tickDur := time.Duration(h.cfg.Scheduler.TickMS) * time.Millisecond // 100ms
	stable := time.Duration(h.cfg.Turn.DoneStableSec) * time.Second      // 5s
	cooldown := time.Duration(h.cfg.Scheduler.CooldownSec) * time.Second
	releasedAt := h.clk.Now().Add(stable + tickDur/2) // 5.05s от старта — не кратное tick_ms
	releaseAt(t, h, releasedAt)
	granted := grantAtCooldown(t, h, releasedAt)
	want := releasedAt.Add(cooldown)
	if !granted.Equal(want) {
		t.Fatalf("off-phase: GrantedAt=%v, хочу ровно released_at+cooldown=%v (без +tick)",
			granted, want)
	}
}

// п.6(d): удерживающая запись при занятых слотах (серверы UP) — why=held_ok
// (нет более конкретной причины). Слот занят у A; B удерживает запрос.
func TestPrimaryReasonHeldOK(t *testing.T) {
	h := newTickBoundaryHarn(t)
	if _, ok := h.leaseOf("A"); !ok {
		t.Fatal("предпосылка: у A нет аренды (слот должен быть занят)")
	}
	h.tick() // B не выдаётся (слот занят) → primaryReason.
	if _, ok := h.leaseOf("B"); ok {
		t.Fatal("B не должен быть выдан: слот занят у A")
	}
	if r := h.queueReason("B"); r != model.ReasonHeldOK {
		t.Fatalf("why=%q, хочу %q (слоты LEASED, сервер UP)", r, model.ReasonHeldOK)
	}
}

// TestTickBoundaryOnPhase — освобождение в фазе тика (T = done_stable, кратное
// tick_ms): выдача тоже РОВНО в released_at+cooldown_sec.
func TestTickBoundaryOnPhase(t *testing.T) {
	h := newTickBoundaryHarn(t)
	stable := time.Duration(h.cfg.Turn.DoneStableSec) * time.Second // 5s = 50*tick_ms (в фазе)
	cooldown := time.Duration(h.cfg.Scheduler.CooldownSec) * time.Second
	releasedAt := h.clk.Now().Add(stable)
	releaseAt(t, h, releasedAt)
	granted := grantAtCooldown(t, h, releasedAt)
	want := releasedAt.Add(cooldown)
	if !granted.Equal(want) {
		t.Fatalf("on-phase: GrantedAt=%v, хочу ровно released_at+cooldown=%v", granted, want)
	}
}
