package model

import "errors"

// ErrInvalidTransition — переход не описан автоматом.
var ErrInvalidTransition = errors.New("model: invalid transition")

// SessionEvent — события автомата сессии (раздел 4 ТЗ).
type SessionEvent string

const (
	// Очередь.
	EvEnqueue      SessionEvent = "enqueue"
	EvDequeue      SessionEvent = "dequeue"
	EvLeaseGranted SessionEvent = "lease_granted"

	// Диспетчеризация (ответы узла и таймеры).
	EvStartConfirmed    SessionEvent = "start_confirmed"
	EvPaneChanged       SessionEvent = "pane_changed"
	EvEmptyInput        SessionEvent = "empty_input"
	EvStartNotConfirmed SessionEvent = "start_not_confirmed"

	// Ручной Enter (неявная аренда).
	EvEnterHasSlot SessionEvent = "enter_has_slot"
	EvEnterNoSlot  SessionEvent = "enter_no_slot"

	// Ход.
	EvTurnDone       SessionEvent = "turn_done"
	EvLeaseRevoked   SessionEvent = "lease_revoked"
	EvResumeRequest  SessionEvent = "resume_request"
	EvIdleNoError    SessionEvent = "idle_no_error"
	EvErrorRetryable SessionEvent = "error_retryable"
	EvErrorFatal     SessionEvent = "error_fatal"

	// Оператор и зависимости.
	EvRequeue          SessionEvent = "requeue"
	EvUnhold           SessionEvent = "unhold"
	EvHold             SessionEvent = "hold"
	EvPinUnavailable   SessionEvent = "pin_unavailable"
	EvDependencyFailed SessionEvent = "dependency_failed"
	EvNodeDrain        SessionEvent = "node_drain"

	// Панель исчезла.
	EvPaneGone SessionEvent = "pane_gone"

	// v2 (6.2): аварийная остановка — DISPATCHING/RUNNING/DETACHED → HOLD
	// (hold_reason=EMERGENCY передаётся явно в SetSessionState).
	EvEmergency SessionEvent = "emergency"

	// v2 (7.3, C1): кодер в панели завершился/убит (@runpilot_exit) —
	// DISPATCHING/RUNNING/DETACHED → HOLD(AGENT_EXITED).
	EvAgentExit SessionEvent = "agent_exit"
)

// AllSessionEvents — все события автомата.
var AllSessionEvents = []SessionEvent{
	EvEnqueue, EvDequeue, EvLeaseGranted,
	EvStartConfirmed, EvPaneChanged, EvEmptyInput, EvStartNotConfirmed,
	EvEnterHasSlot, EvEnterNoSlot,
	EvTurnDone, EvLeaseRevoked, EvResumeRequest, EvIdleNoError,
	EvErrorRetryable, EvErrorFatal,
	EvRequeue, EvUnhold, EvHold, EvPinUnavailable, EvDependencyFailed, EvNodeDrain,
	EvPaneGone, EvEmergency, EvAgentExit,
}

// Transition — одна строка таблицы автомата.
type Transition struct {
	From SessionState
	Ev   SessionEvent
	To   SessionState
}

// sessionTransitions — полная таблица автомата сессии (раздел 4 ТЗ).
//
// Состав (31 строка), маппинг в отчёте docs/evidence/Э0/automaton-transitions.md:
//   - 21 — стрелки диаграммы раздела 4 (двухстрелочные причины расписаны по строкам)
//   - 6  — «из любого состояния» в GONE при исчезновении панели
//   - 2  — «DETACHED при ошибке ведёт себя как RUNNING»
//   - 2  — ручной hold (код OPERATOR из таблицы hold_reason)
//
// Плюс начальный переход ∅ → IDLE (runpilot run), см. NewSession.
var sessionTransitions = []Transition{
	// Диаграмма раздела 4.
	{SessionIdle, EvEnqueue, SessionQueued},                // enqueue
	{SessionQueued, EvDequeue, SessionIdle},                // dequeue
	{SessionQueued, EvLeaseGranted, SessionDispatching},    // аренда выдана
	{SessionDispatching, EvStartConfirmed, SessionRunning}, // старт подтверждён
	{SessionDispatching, EvPaneChanged, SessionQueued},     // PANE_CHANGED
	{SessionDispatching, EvEmptyInput, SessionHold},        // EMPTY_INPUT
	{SessionDispatching, EvStartNotConfirmed, SessionHold}, // START_NOT_CONFIRMED
	{SessionIdle, EvEnterHasSlot, SessionRunning},          // ручной Enter, слот есть
	{SessionIdle, EvEnterNoSlot, SessionQueued},            // ручной Enter, слота нет
	{SessionRunning, EvTurnDone, SessionIdle},              // ход завершён OK
	{SessionRunning, EvLeaseRevoked, SessionDetached},      // аренда снята в ходе
	{SessionDetached, EvResumeRequest, SessionRunning},     // новый запрос хода
	{SessionDetached, EvIdleNoError, SessionIdle},          // панель IDLE без ошибки
	{SessionRunning, EvErrorRetryable, SessionQueued},      // ошибка, attempts ≤ max
	{SessionRunning, EvErrorFatal, SessionHold},            // ошибка, attempts > max
	{SessionHold, EvRequeue, SessionQueued},                // requeue
	{SessionHold, EvUnhold, SessionIdle},                   // unhold
	{SessionQueued, EvPinUnavailable, SessionHold},         // PIN_UNAVAILABLE
	{SessionQueued, EvDependencyFailed, SessionHold},       // DEPENDENCY_FAILED
	{SessionIdle, EvNodeDrain, SessionHold},                // NODE_DRAIN
	{SessionQueued, EvNodeDrain, SessionHold},              // NODE_DRAIN
	// Из любого состояния — GONE.
	{SessionIdle, EvPaneGone, SessionGone},
	{SessionQueued, EvPaneGone, SessionGone},
	{SessionDispatching, EvPaneGone, SessionGone},
	{SessionRunning, EvPaneGone, SessionGone},
	{SessionDetached, EvPaneGone, SessionGone},
	{SessionHold, EvPaneGone, SessionGone},
	// «DETACHED при ошибке ведёт себя как RUNNING».
	{SessionDetached, EvErrorRetryable, SessionQueued},
	{SessionDetached, EvErrorFatal, SessionHold},
	// Ручной hold (hold_reason = OPERATOR).
	{SessionIdle, EvHold, SessionHold},
	{SessionQueued, EvHold, SessionHold},
	// v2 (6.2): аварийная остановка — идущие ходы → HOLD(EMERGENCY).
	{SessionDispatching, EvEmergency, SessionHold},
	{SessionRunning, EvEmergency, SessionHold},
	{SessionDetached, EvEmergency, SessionHold},
	// v2 (7.3, C1): кодер завершился/убит → HOLD(AGENT_EXITED).
	{SessionDispatching, EvAgentExit, SessionHold},
	{SessionRunning, EvAgentExit, SessionHold},
	{SessionDetached, EvAgentExit, SessionHold},
}

var transitionIndex = func() map[SessionState]map[SessionEvent]SessionState {
	m := make(map[SessionState]map[SessionEvent]SessionState, len(sessionTransitions))
	for _, tr := range sessionTransitions {
		if m[tr.From] == nil {
			m[tr.From] = make(map[SessionEvent]SessionState)
		}
		m[tr.From][tr.Ev] = tr.To
	}
	return m
}()

// SessionTransitions возвращает полную таблицу переходов (для тестов и отчёта).
func SessionTransitions() []Transition {
	out := make([]Transition, len(sessionTransitions))
	copy(out, sessionTransitions)
	return out
}

// NextSessionState — чистая функция автомата сессии.
func NextSessionState(from SessionState, ev SessionEvent) (SessionState, error) {
	if evs, ok := transitionIndex[from]; ok {
		if to, ok := evs[ev]; ok {
			return to, nil
		}
	}
	return "", ErrInvalidTransition
}

// IsTerminal — из состояния нет исходящих переходов.
func (s SessionState) IsTerminal() bool {
	_, ok := transitionIndex[s]
	return !ok
}

// HoldReasonFor — код hold_reason, который событие записывает при переходе в HOLD.
func HoldReasonFor(ev SessionEvent) (HoldReason, bool) {
	switch ev {
	case EvEmptyInput:
		return HoldEmptyInput, true
	case EvStartNotConfirmed:
		return HoldStartNotConfirmed, true
	case EvErrorFatal:
		return HoldRequeueLimit, true
	case EvPinUnavailable:
		return HoldPinUnavailable, true
	case EvDependencyFailed:
		return HoldDependencyFailed, true
	case EvNodeDrain:
		return HoldNodeDrain, true
	case EvHold:
		return HoldOperator, true
	}
	return "", false
}
