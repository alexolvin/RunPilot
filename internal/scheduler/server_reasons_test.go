package scheduler

// W13 этап 2: причины по серверам (server_reasons) в строке очереди —
// почему сервер не даёт слот записи (busy / external / free / cooldown /
// down / class / pin).

import (
	"testing"
	"time"

	"runpilot/internal/model"
)

// findReason — server_reasons строки по sid → причина по имени сервера.
func findReason(t *testing.T, rows []QueueRow, sid, server string) (ServerReason, bool) {
	t.Helper()
	for _, r := range rows {
		if r.SID != sid {
			continue
		}
		for _, sr := range r.ServerReasons {
			if sr.Server == server {
				return sr, true
			}
		}
		return ServerReason{}, false
	}
	t.Fatalf("строка %s не найдена в очереди", sid)
	return ServerReason{}, false
}

// TestServerReasonsBusyExternal — сервер a занят (busy), сервер b под
// внешней нагрузкой (external): причины по серверам в строке очереди.
func TestServerReasonsBusyExternal(t *testing.T) {
	h := newHarn(t, []ServerView{srv("a", 10, 1), srv("b", 5, 1)})
	confirm := time.Duration(h.cfg.Scheduler.ExternalConfirmSec) * time.Second

	// a: занят сессией "other" (аренда ACTIVE).
	h.addSession("other", "other", "host", model.SessionRunning, model.ClassNormal, model.NoConstraint)
	if _, err := h.st.LeaseCreate(model.Lease{
		SID: "other", Server: "a", Slot: 1,
		State: model.LeaseActive, Origin: model.LeaseOriginImplicit,
		GrantedAt: h.clk.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	// b: внешняя нагрузка ext=1 (1 слот → весь EXTERNAL).
	h.sch.SetExternal("b", 1)
	h.advance(confirm)
	h.tick()

	// s1 в очереди (idle-панель) — слотов нет: a busy, b external.
	h.addSession("s1", "s1", "host", model.SessionQueued, model.ClassNormal, model.NoConstraint)
	h.idlePane("s1", "host")
	h.enqueue("s1", model.ClassNormal, model.NoConstraint, model.QueueModeSubmit)
	h.advance(3 * time.Second)
	h.tick()

	rows := h.sch.QueueView()
	if _, ok := h.leaseOf("s1"); ok {
		t.Fatal("s1 выдана, хотя слотов нет (a busy, b external)")
	}
	a, _ := findReason(t, rows, "s1", "a")
	if a.Reason != "busy" || a.Detail != "other" {
		t.Fatalf("a: reason=%q detail=%q, хочу busy/other", a.Reason, a.Detail)
	}
	b, _ := findReason(t, rows, "s1", "b")
	if b.Reason != "external" || b.Detail != "1" {
		t.Fatalf("b: reason=%q detail=%q, хочу external/1", b.Reason, b.Detail)
	}
}

// TestServerReasonsFree — оба сервера свободны: причина free (сессию держит
// не слоты, а другое — например, панель WAIT_UI).
func TestServerReasonsFree(t *testing.T) {
	h := newHarn(t, []ServerView{srv("a", 10, 1), srv("b", 5, 1)})
	h.addSession("s1", "s1", "host", model.SessionQueued, model.ClassNormal, model.NoConstraint)
	h.setPane("s1", "host", model.PaneWaitUI, 1, false)
	h.enqueue("s1", model.ClassNormal, model.NoConstraint, model.QueueModeSubmit)
	h.advance(3 * time.Second)
	h.tick()

	rows := h.sch.QueueView()
	a, _ := findReason(t, rows, "s1", "a")
	if a.Reason != "free" {
		t.Fatalf("a: reason=%q, хочу free", a.Reason)
	}
	b, _ := findReason(t, rows, "s1", "b")
	if b.Reason != "free" {
		t.Fatalf("b: reason=%q, хочу free", b.Reason)
	}
}

// TestServerReasonsPin — pin на b (b занят): сервер a причина pin, b — busy.
func TestServerReasonsPin(t *testing.T) {
	h := newHarn(t, []ServerView{srv("a", 10, 1), srv("b", 5, 1)})
	// b занят сессией "other".
	h.addSession("other", "other", "host", model.SessionRunning, model.ClassNormal, model.NoConstraint)
	if _, err := h.st.LeaseCreate(model.Lease{
		SID: "other", Server: "b", Slot: 1,
		State: model.LeaseActive, Origin: model.LeaseOriginImplicit,
		GrantedAt: h.clk.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	cons := model.Constraint{Kind: model.ConstraintPin, Server: "b"}
	h.addSession("s1", "s1", "host", model.SessionQueued, model.ClassNormal, cons)
	h.idlePane("s1", "host")
	h.enqueue("s1", model.ClassNormal, cons, model.QueueModeSubmit)
	h.advance(3 * time.Second)
	h.tick()

	rows := h.sch.QueueView()
	if _, ok := h.leaseOf("s1"); ok {
		t.Fatal("s1 выдана, хотя pin-сервер b занят")
	}
	a, _ := findReason(t, rows, "s1", "a")
	if a.Reason != "pin" {
		t.Fatalf("a: reason=%q, хочу pin", a.Reason)
	}
	b, _ := findReason(t, rows, "s1", "b")
	if b.Reason != "busy" || b.Detail != "other" {
		t.Fatalf("b: reason=%q detail=%q, хочу busy/other", b.Reason, b.Detail)
	}
}
