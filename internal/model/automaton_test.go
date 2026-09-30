package model

import (
	"errors"
	"testing"
	"time"
)

// TestSessionTransitionsComplete — каждая строка таблицы автомата применяется
// функцией NextSessionState.
func TestSessionTransitionsComplete(t *testing.T) {
	seen := map[SessionState]map[SessionEvent]bool{}
	for _, tr := range SessionTransitions() {
		got, err := NextSessionState(tr.From, tr.Ev)
		if err != nil {
			t.Fatalf("NextSessionState(%s, %s): неожиданный отказ: %v", tr.From, tr.Ev, err)
		}
		if got != tr.To {
			t.Fatalf("NextSessionState(%s, %s) = %s, в таблице %s", tr.From, tr.Ev, got, tr.To)
		}
		if seen[tr.From] == nil {
			seen[tr.From] = map[SessionEvent]bool{}
		}
		if seen[tr.From][tr.Ev] {
			t.Fatalf("дублирующая строка таблицы: %s + %s", tr.From, tr.Ev)
		}
		seen[tr.From][tr.Ev] = true
	}
}

// TestTransitionCount — число переходов фиксировано; маппинг строк в раздел 4
// ТЗ — docs/evidence/Э0/automaton-transitions.md:
// 21 строка диаграммы (2 двухпричинные стрелки расписаны) +
// 6 GONE («из любого состояния») + 2 «DETACHED ведёт себя как RUNNING» +
// 2 ручной hold (OPERATOR из таблицы hold_reason) +
// 3 аварийная остановка (v2 6.2: DISPATCHING/RUNNING/DETACHED → HOLD) +
// 3 кодер завершился (v2 7.3 C1: DISPATCHING/RUNNING/DETACHED → HOLD) = 37,
// плюс начальный ∅ → IDLE (runpilot run) = 38 переходов всего.
func TestTransitionCount(t *testing.T) {
	const wantTable = 37
	const wantTotal = 38 // таблица + начальный переход
	if got := len(SessionTransitions()); got != wantTable {
		t.Fatalf("строк в таблице автомата = %d, ждём %d (см. отчёт о маппинге)", got, wantTable)
	}
	if wantTotal != len(SessionTransitions())+1 {
		t.Fatalf("итого переходов (включая ∅ → IDLE) должно быть %d", wantTotal)
	}
}

// TestExhaustiveClassification — 100% покрытие: каждая пара
// (состояние, событие) либо описана таблицей, либо отвергнута.
func TestExhaustiveClassification(t *testing.T) {
	table := map[SessionState]map[SessionEvent]SessionState{}
	for _, tr := range SessionTransitions() {
		if table[tr.From] == nil {
			table[tr.From] = map[SessionEvent]SessionState{}
		}
		table[tr.From][tr.Ev] = tr.To
	}
	pairs := 0
	for _, from := range AllSessionStates {
		for _, ev := range AllSessionEvents {
			pairs++
			to, err := NextSessionState(from, ev)
			want, ok := table[from][ev]
			if ok {
				if err != nil {
					t.Fatalf("(%s, %s): в таблице переход, но функция отказала", from, ev)
				}
				if to != want {
					t.Fatalf("(%s, %s) = %s, в таблице %s", from, ev, to, want)
				}
			} else if !errors.Is(err, ErrInvalidTransition) {
				t.Fatalf("(%s, %s): не в таблице, но функция не дала ErrInvalidTransition: %v", from, ev, err)
			}
		}
	}
	t.Logf("происследовано пар (состояние, событие): %d", pairs)
}

// TestGoneIsTerminal — из GONE нет переходов.
func TestGoneIsTerminal(t *testing.T) {
	if !SessionGone.IsTerminal() {
		t.Fatal("GONE должен быть терминальным")
	}
	for _, ev := range AllSessionEvents {
		if _, err := NextSessionState(SessionGone, ev); !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("GONE + %s: ожидался отказ", ev)
		}
	}
}

// TestHoldReasonFor — каждое событие, ведущее в HOLD, даёт свой код.
func TestHoldReasonFor(t *testing.T) {
	want := map[SessionEvent]HoldReason{
		EvEmptyInput:        HoldEmptyInput,
		EvStartNotConfirmed: HoldStartNotConfirmed,
		EvErrorFatal:        HoldRequeueLimit,
		EvPinUnavailable:    HoldPinUnavailable,
		EvDependencyFailed:  HoldDependencyFailed,
		EvNodeDrain:         HoldNodeDrain,
		EvHold:              HoldOperator,
	}
	for ev, code := range want {
		got, ok := HoldReasonFor(ev)
		if !ok || got != code {
			t.Fatalf("HoldReasonFor(%s) = %v, %v; ждём %v", ev, got, ok, code)
		}
	}
	// События, не ведущие в HOLD, кода не дают.
	for _, ev := range AllSessionEvents {
		if _, isHold := want[ev]; isHold {
			continue
		}
		if _, ok := HoldReasonFor(ev); ok {
			t.Fatalf("HoldReasonFor(%s) не должен давать код", ev)
		}
	}
}

func TestServerHealthUpDown(t *testing.T) {
	const downAfter, upAfter = 3, 2

	h := NewServerHealth()
	if h.State != ServerUp {
		t.Fatalf("старт не UP: %s", h.State)
	}
	// UP: две неудачи — ещё не DOWN.
	for i := 0; i < downAfter-1; i++ {
		h, _ = h.Observe(false, downAfter, upAfter)
	}
	if h.State != ServerUp {
		t.Fatalf("после %d неудач сервер уже не UP: %s", downAfter-1, h.State)
	}
	// Третья — DOWN.
	h, changed := h.Observe(false, downAfter, upAfter)
	if h.State != ServerDown || !changed {
		t.Fatalf("после %d неудач: %s, changed=%v", downAfter, h.State, changed)
	}
	// DOWN: одна удача — ещё не UP.
	h, _ = h.Observe(true, downAfter, upAfter)
	if h.State != ServerDown {
		t.Fatalf("после одной удачи сервер уже не DOWN: %s", h.State)
	}
	h, changed = h.Observe(true, downAfter, upAfter)
	if h.State != ServerUp || !changed {
		t.Fatalf("после %d удач: %s, changed=%v", upAfter, h.State, changed)
	}
	// Серия сбрасывается при смене исхода.
	h, _ = h.Observe(false, downAfter, upAfter)
	h, _ = h.Observe(true, downAfter, upAfter)
	h, _ = h.Observe(false, downAfter, upAfter)
	if h.State != ServerUp {
		t.Fatalf("серия не сброшена: %s", h.State)
	}
}

func TestServerHealthDraining(t *testing.T) {
	const downAfter, upAfter = 3, 2
	h := NewServerHealth()
	h, changed := h.SetDraining(true)
	if h.State != ServerDraining || !changed {
		t.Fatalf("UP → DRAINING: %s, %v", h.State, changed)
	}
	// Health-отказ из DRAINING ведёт в DOWN.
	for i := 0; i < downAfter; i++ {
		h, _ = h.Observe(false, downAfter, upAfter)
	}
	if h.State != ServerDown {
		t.Fatalf("DRAINING + %d неудач: %s", downAfter, h.State)
	}
	// Восстановление из DOWN возвращает в UP.
	for i := 0; i < upAfter; i++ {
		h, _ = h.Observe(true, downAfter, upAfter)
	}
	if h.State != ServerUp {
		t.Fatalf("DOWN + %d удач: %s", upAfter, h.State)
	}
	h, changed = h.SetDraining(false)
	if h.State != ServerUp || changed {
		t.Fatalf("повторный undrain из UP: %s, %v", h.State, changed)
	}
}

func TestSlotLifecycle(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	const cooldown = 2 * time.Second
	const confirm = 3 * time.Second

	s := NewSlot()
	if s.State != SlotFree {
		t.Fatalf("новый слот не FREE: %s", s.State)
	}
	s = s.Leased()
	if s.State != SlotLeased {
		t.Fatalf("Leased() = %s", s.State)
	}
	s = s.ReleasedAt(now, cooldown)
	if s.State != SlotCooldown {
		t.Fatalf("ReleasedAt = %s", s.State)
	}
	// До истечения — COOLDOWN.
	s2 := s.TickCooldown(now.Add(cooldown - time.Millisecond))
	if s2.State != SlotCooldown {
		t.Fatalf("до истечения cooldown слот %s", s2.State)
	}
	// Истечение — FREE.
	s2 = s.TickCooldown(now.Add(cooldown))
	if s2.State != SlotFree {
		t.Fatalf("после cooldown слот %s", s2.State)
	}
	s = s2

	// EXTERNAL: ext > 0 держится confirm.
	s = s.SampleExternal(true, now)
	if s.ExternalReady(now.Add(confirm-time.Millisecond), confirm) {
		t.Fatal("ExternalReady раньше confirm")
	}
	if !s.ExternalReady(now.Add(confirm), confirm) {
		t.Fatal("ExternalReady позже confirm должно быть true")
	}
	s = s.ToExternal()
	if s.State != SlotExternal {
		t.Fatalf("ToExternal = %s", s.State)
	}
	// Возврат: ext = 0 держится confirm.
	t1 := now.Add(10 * time.Second)
	s = s.SampleExternal(false, t1)
	if s.ExternalCleared(t1.Add(confirm-time.Millisecond), confirm) {
		t.Fatal("ExternalCleared раньше confirm")
	}
	if !s.ExternalCleared(t1.Add(confirm), confirm) {
		t.Fatal("ExternalCleared позже confirm должно быть true")
	}
	s = s.ToFree()
	if s.State != SlotFree {
		t.Fatalf("после ToFree = %s", s.State)
	}
}
