package scheduler

import (
	"testing"
	"time"

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

// п.6: строка статуса tmux «… Q N/M held» (текст для @runpilot_status).
func TestQueueHeldStatusLine(t *testing.T) {
	cases := []struct {
		held, total int
		want        string
	}{
		{0, 0, ""},     // пустая очередь — фрагмент не показывается
		{0, 3, "Q 3"},  // очередь есть, удерживающих нет
		{2, 5, "Q 2/5 held"}, // есть удерживающие — «Q N/M held»
		{4, 4, "Q 4/4 held"},
	}
	for _, c := range cases {
		if got := QueueHeldStatus(c.held, c.total); got != c.want {
			t.Fatalf("QueueHeldStatus(%d, %d) = %q, хочу %q", c.held, c.total, got, c.want)
		}
	}
}

// п.6: удерживающая запись — строка очереди несёт held=true и held_since
// (данные для бейджа HELD и таймера now−held_since в веб).
func TestQueueViewHeld(t *testing.T) {
	h := newHarn(t, []ServerView{srv("s1", 1, 2)})
	enq := h.clk.Now()
	h.addSession("h1", "heldsess", "n1", model.SessionQueued, model.ClassHigh, model.NoConstraint)
	if err := h.st.QueueUpsert(model.QueueEntry{
		SID: "h1", Class: model.ClassHigh, EnqueuedAt: enq, NotBefore: enq,
		Mode: model.QueueModeSubmit, HeldRequest: true, HeldSince: enq,
	}); err != nil {
		t.Fatal(err)
	}
	q := h.sch.QueueView()
	if len(q) != 1 {
		t.Fatalf("QueueView: %d строк, хочу 1", len(q))
	}
	if !q[0].Held {
		t.Fatalf("строка удерживающей записи: held=false: %+v", q[0])
	}
	if q[0].HeldSince == nil || !q[0].HeldSince.Equal(enq) {
		t.Fatalf("held_since: %v, хочу %v", q[0].HeldSince, enq)
	}
	// Неудерживающая запись — held не ставится.
	h.addSession("p1", "plain", "n1", model.SessionQueued, model.ClassNormal, model.NoConstraint)
	if err := h.st.QueueUpsert(model.QueueEntry{
		SID: "p1", Class: model.ClassNormal,
		EnqueuedAt: h.clk.Now().Add(time.Second), NotBefore: h.clk.Now().Add(time.Second),
		Mode: model.QueueModeSubmit, HeldRequest: false,
	}); err != nil {
		t.Fatal(err)
	}
	foundPlain := false
	for _, r := range h.sch.QueueView() {
		if r.SID == "p1" {
			foundPlain = true
			if r.Held {
				t.Fatalf("неудерживающая запись: held=true: %+v", r)
			}
		}
	}
	if !foundPlain {
		t.Fatal("запись p1 не найдена в QueueView")
	}
}
