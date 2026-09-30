package model

import "testing"

// W1 CONTROL: схема ответа каждой сущности содержит actions — действия
// вычисляет сервер по состоянию.

func TestActionsNonEmpty(t *testing.T) {
	for st := range map[SessionState]bool{
		SessionIdle: true, SessionQueued: true, SessionDispatching: true,
		SessionRunning: true, SessionDetached: true, SessionHold: true, SessionGone: true,
	} {
		if n := len(SessionActions(st)); n != 6 {
			t.Errorf("SessionActions(%s) = %d, хочу 6", st, n)
		}
	}
	if n := len(ServerActions(ServerUp)); n != 2 {
		t.Errorf("ServerActions(UP) = %d, хочу 2", n)
	}
	if n := len(NodeActions()); n != 1 {
		t.Errorf("NodeActions() = %d, хочу 1", n)
	}
	if n := len(QueueActions()); n != 1 {
		t.Errorf("QueueActions() = %d, хочу 1", n)
	}
}

func enabledSet(acts []Action) map[string]bool {
	m := map[string]bool{}
	for _, a := range acts {
		m[a.ID] = a.Enabled
	}
	return m
}

func TestSessionActionsEnabledByState(t *testing.T) {
	idle := enabledSet(SessionActions(SessionIdle))
	if !idle["enqueue"] || !idle["hold"] || !idle["close"] {
		t.Errorf("IDLE: ожидался enqueue/hold/close enabled: %v", idle)
	}
	if idle["dequeue"] || idle["cancel"] || idle["release"] {
		t.Errorf("IDLE: не ожидался dequeue/cancel/release: %v", idle)
	}
	queued := enabledSet(SessionActions(SessionQueued))
	if !queued["dequeue"] || queued["enqueue"] {
		t.Errorf("QUEUED: ожидался dequeue enabled, enqueue disabled: %v", queued)
	}
	running := enabledSet(SessionActions(SessionRunning))
	if !running["cancel"] {
		t.Errorf("RUNNING: ожидался cancel enabled: %v", running)
	}
	hold := enabledSet(SessionActions(SessionHold))
	if !hold["release"] || !hold["enqueue"] {
		t.Errorf("HOLD: ожидался release/enqueue enabled: %v", hold)
	}
}

// Недоступное действие всегда несёт причину (веб показывает её в подсказке).
func TestDisabledActionHasReason(t *testing.T) {
	states := []SessionState{SessionIdle, SessionQueued, SessionRunning,
		SessionDetached, SessionHold, SessionGone, SessionDispatching}
	for _, st := range states {
		for _, a := range SessionActions(st) {
			if !a.Enabled && a.Reason == "" {
				t.Errorf("SessionActions(%s)[%s]: нет причины недоступности", st, a.ID)
			}
		}
	}
}
