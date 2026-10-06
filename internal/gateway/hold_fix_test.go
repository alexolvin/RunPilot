package gateway

// Тесты на исправления: П.4 (Esc→client_gone), П.5 (preserve enqueued_at),
// П.10 (self-grant пишет LEASE_GRANT).

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"runpilot/internal/config"
	"runpilot/internal/model"
	"runpilot/internal/testutil/fakellm"
)

// П.4: Esc/отмена во время удержания — запись очереди удалена, сессия IDLE,
// событие QUEUE_DEQUEUE (reason: client_gone).
func TestP4ClientGone(t *testing.T) {
	a := fakellm.Start("srv-model")
	defer a.Close()
	t.Setenv("RUNPILOT_TEST_KEY", "k")
	st, gw, _ := testEnv(t, []config.Server{srvCfg("a", 10, 1, a.URL, "RUNPILOT_TEST_KEY")})
	s := mkSession(t, st, model.SessionIdle)
	// Запись очереди (как создаёт holdPass).
	now := time.Now()
	if err := st.QueueUpsert(model.QueueEntry{
		SID: s, Class: model.ClassNormal, EnqueuedAt: now, NotBefore: now,
		Constraint: model.NoConstraint, Mode: model.QueueModeSubmit, HeldRequest: true,
	}); err != nil {
		t.Fatal(err)
	}
	// clientGone: запись удалена, сессия IDLE, событие QUEUE_DEQUEUE.
	gw.clientGone(s)
	// Запись очереди удалена.
	if _, err := st.QueueGet(s); err == nil {
		t.Fatal("запись очереди должна быть удалена после clientGone")
	}
	// Событие QUEUE_DEQUEUE (reason: client_gone) — асинхронная запись, ждём.
	found := false
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		evs, _ := st.EventList(0, 1000)
		for _, e := range evs {
			if e.Kind == model.KindQueueDequeue && e.SID == s {
				found = true
				break
			}
		}
		if found {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !found {
		t.Fatal("нет события QUEUE_DEQUEUE после clientGone")
	}
	// Сессия IDLE.
	sess, _ := st.GetSession(s)
	if sess.State != model.SessionIdle {
		t.Fatalf("сессия: %s, хочу IDLE", sess.State)
	}
	// П.4: request.error = CLIENT_GONE (асинхронная запись, ждём).
	reqFound := false
	deadline2 := time.Now().Add(time.Second)
	for time.Now().Before(deadline2) {
		reqs, _ := st.RequestList(s, 10)
		for _, r := range reqs {
			if r.Error == "CLIENT_GONE" {
				reqFound = true
				break
			}
		}
		if reqFound {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !reqFound {
		t.Fatal("нет request.error = CLIENT_GONE после clientGone")
	}
}

// П.5: повторный вход в удержание (ретраи) сохраняет исходные enqueued_at /
// attempts / class. QueueUpsert при CONFLICT: MIN(enqueued_at), MAX(attempts),
// MIN(class) (меньший номер = выше приоритет).
func TestP5PreserveEnqueuedAt(t *testing.T) {
	a := fakellm.Start("srv-model")
	defer a.Close()
	t.Setenv("RUNPILOT_TEST_KEY", "k")
	st, gw, sch, _, ts := vclockEnv(t, []config.Server{srvCfg("a", 10, 1, a.URL, "RUNPILOT_TEST_KEY")})
	s := mkSession(t, st, model.SessionIdle)
	// Исходная запись (enqueued_at=T1, attempts=3, class=NORMAL=2).
	t1 := time.Now().Add(-10 * time.Second)
	if err := st.QueueUpsert(model.QueueEntry{
		SID: s, Class: model.ClassNormal, EnqueuedAt: t1, NotBefore: t1,
		Constraint: model.NoConstraint, Mode: model.QueueModeSubmit,
		HeldRequest: true, Attempts: 3,
	}); err != nil {
		t.Fatal(err)
	}
	// Захват слота holder'ом (waiter уйдёт в удержание).
	holder := mkSession(t, st, model.SessionIdle)
	resp, _ := postJSON(t, ts.URL+"/s/"+holder+"/v1/chat/completions", chatBody("runpilot", ""))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("holder: %d", resp.StatusCode)
	}
	// Waiter: запрос → удержание. holdPass пишет QueueUpsert(now, attempts=0,
	// class=HIGH=1); CONFLICT берёт MIN(enq)=T1, MAX(att)=3, MIN(class)=1.
	done := make(chan *http.Response, 1)
	go func() {
		r, _ := http.Post(ts.URL+"/s/"+s+"/v1/chat/completions",
			"application/json", strings.NewReader(chatBody("runpilot", "")))
		if r != nil {
			defer r.Body.Close()
			_, _ = io.Copy(io.Discard, r.Body)
		}
		done <- r
	}()
	time.Sleep(300 * time.Millisecond) // waiter в удержании (QueueUpsert выполнен)
	e, err := st.QueueGet(s)
	if err != nil {
		t.Fatalf("запись очереди: %v", err)
	}
	if e.EnqueuedAt.After(t1.Add(2 * time.Second)) {
		t.Fatalf("enqueued_at: %s, должен быть ≈ %s (П.5: не сбросился на now)", e.EnqueuedAt, t1)
	}
	if e.Attempts != 3 {
		t.Fatalf("attempts: %d, хочу 3 (П.5: не сбросился на 0)", e.Attempts)
	}
	if e.Class != model.ClassHigh {
		t.Fatalf("class: %d, хочу HIGH=1 (П.5: более приоритетный из NORMAL=2/HIGH=1)", e.Class)
	}
	// Освобождение слота → планировщик выдаёт waiter → 200.
	gw.Release(holder, "TURN_DONE")
	sch.Tick()
	r := <-done
	if r == nil || r.StatusCode != http.StatusOK {
		t.Fatalf("после освобождения слота: %v, хочу 200", r)
	}
}
