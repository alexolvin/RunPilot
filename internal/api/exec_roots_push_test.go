package api

// EXEC-корни (8.1 ТЗ): per-host пуш + отчёт узла о мёртвом корне (T17).

import (
	"net/http"
	"testing"
	"time"

	"runpilot/internal/clock"
	"runpilot/internal/model"
	"runpilot/internal/proto"
	"runpilot/internal/store"
)

// fakeExecRootsSrc — ExecRootsSource с фиксированным набором живых корней.
type fakeExecRootsSrc struct{ recs []store.ExecRootRecord }

func (f *fakeExecRootsSrc) LiveExecRoots() ([]store.ExecRootRecord, error) {
	return f.recs, nil
}

// lastExecRoots — ExecRoots из последнего KindExecRoots-сообщения (nil, если
// таких не было). recConn определён в status_push_test.go (тот же пакет).
func (c *recConn) lastExecRoots() []proto.ExecRoot {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := len(c.msgs) - 1; i >= 0; i-- {
		if c.msgs[i].Type == proto.KindExecRoots {
			return c.msgs[i].ExecRoots
		}
	}
	return nil
}

// per-host: корень hostA пушится только hostA, не hostB (не broadcast) — узел
// хоста сверяет /proc локально + только он сообщает о смерти корня.
func TestExecRootsPushPerHost(t *testing.T) {
	hub := NewHub(clock.NewReal())
	connA := &recConn{}
	connB := &recConn{}
	hub.Register(&NodeInfo{Host: "hostA", HostIP: "127.0.0.1", Conn: connA})
	hub.Register(&NodeInfo{Host: "hostB", HostIP: "127.0.0.1", Conn: connB})
	t.Cleanup(func() { hub.Unregister("hostA"); hub.Unregister("hostB") })

	src := &fakeExecRootsSrc{recs: []store.ExecRootRecord{
		{Host: "hostA", SID: "sidA", PID: 100, Starttime: 111},
	}}
	NewExecRootsPusher(hub, src, nil).PushAll()

	gotA := connA.lastExecRoots()
	if len(gotA) != 1 || gotA[0].PID != 100 || gotA[0].SID != "sidA" {
		t.Fatalf("hostA: %+v, хочу 1 корень 100/sidA", gotA)
	}
	if gotB := connB.lastExecRoots(); gotB != nil {
		t.Fatalf("hostB получил корень hostA: %+v, хочу nil (per-host)", gotB)
	}
}

// корень hostA исчез (finish) → hostA получает пустой набор (сброс map узла).
func TestExecRootsPushHostGone(t *testing.T) {
	hub := NewHub(clock.NewReal())
	connA := &recConn{}
	hub.Register(&NodeInfo{Host: "hostA", HostIP: "127.0.0.1", Conn: connA})
	t.Cleanup(func() { hub.Unregister("hostA") })

	src := &fakeExecRootsSrc{recs: []store.ExecRootRecord{
		{Host: "hostA", SID: "sidA", PID: 100, Starttime: 111},
	}}
	p := NewExecRootsPusher(hub, src, nil)
	p.PushAll()
	if got := connA.lastExecRoots(); len(got) != 1 {
		t.Fatalf("после появления: %+v, хочу 1 корень", got)
	}
	src.recs = nil // корень исчез (finish)
	p.PushAll()
	if got := connA.lastExecRoots(); got != nil {
		t.Fatalf("после исчезновения: %+v, хочу nil (сброс map узла)", got)
	}
}

// onExecRootDead — узел хоста сообщил: корень мёртв без finish → сессия GONE +
// аренда снята (T17). Идемпотентно (повторный отчёт по GONE — без эффекта).
func TestExecRootDead(t *testing.T) {
	ts, st, srv := jobTestServer(t, "")
	_, out := jobReq(t, ts, "", http.MethodPost, "/api/v1/jobs",
		map[string]any{"name": "deadroot"})
	sid, _ := out["sid"].(string)
	// Аренда выдана (имитация: слот занят).
	if _, err := st.LeaseCreateIfAbsent(model.Lease{
		SID: sid, Server: "srv1", Slot: 1, State: model.LeaseActive,
		Origin: model.LeaseOriginDispatch, GrantedAt: time.Now(),
	}, 1, false); err != nil {
		t.Fatal(err)
	}
	// Узел хоста сообщил: корень мёртв (без finish) → GONE + аренда снята.
	srv.onExecRootDead(sid, 500)
	if _, err := st.LeaseGet(sid); err == nil {
		t.Fatalf("exec_root_dead: аренда не снята")
	}
	if rec, err := st.GetSession(sid); err != nil || rec.State != model.SessionGone {
		t.Fatalf("exec_root_dead: сессия=%+v err=%v, хочу GONE", rec, err)
	}
	// Идемпотентно: повторный отчёт по уже GONE-сессии — без ошибки/эффекта.
	srv.onExecRootDead(sid, 500)
}

// на несуществующей сессии — без паники (нет записи → ignore).
func TestExecRootDeadNotFound(t *testing.T) {
	_, _, srv := jobTestServer(t, "")
	srv.onExecRootDead("NOSUCHSID", 500) // не должно паниковать
}
