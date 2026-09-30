package scheduler

import (
	"testing"

	"runpilot/internal/model"
)

// W1 CONTROL: схема ответа сущности содержит actions (раздел 15.2) —
// строки серверов и очереди несут вычисленные сервером действия.

func TestViewsCarryActions(t *testing.T) {
	h := newHarn(t, []ServerView{srv("s1", 1, 2)})

	srvRows := h.sch.ServersView()
	if len(srvRows) != 1 {
		t.Fatalf("ServersView: %d строк, хочу 1", len(srvRows))
	}
	if len(srvRows[0].Actions) == 0 {
		t.Fatalf("ServerRow без actions: %+v", srvRows[0])
	}

	// Очередь: сессия + запись очереди → строка с actions.
	h.addSession("S1", "sess", "h1", model.SessionQueued, model.ClassNormal, model.NoConstraint)
	if err := h.sch.Enqueue("sess", model.QueueEntry{Class: model.ClassNormal}, false); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	q := h.sch.QueueView()
	if len(q) == 0 {
		t.Fatalf("QueueView пуст после enqueue")
	}
	if len(q[0].Actions) == 0 {
		t.Fatalf("QueueRow без actions: %+v", q[0])
	}
}
