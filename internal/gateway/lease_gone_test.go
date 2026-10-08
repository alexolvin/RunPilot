package gateway

// TestReleaseOnGone — сессия GONE: аренда снимается ТОЛЬКО после
// inflight(sid)=0. inflight=0 → сразу; inflight>0 → после завершения
// запроса (unBumpInflight), до того слот занят.
import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"runpilot/internal/config"
	"runpilot/internal/model"
	"runpilot/internal/store"
	"runpilot/internal/testutil/fakellm"
)

func TestReleaseOnGone(t *testing.T) {
	a := fakellm.Start("srv-model")
	defer a.Close()
	t.Setenv("RUNPILOT_TEST_KEY", "secret-key")
	st, gw, _ := testEnv(t, []config.Server{srvCfg("a", 10, 2, a.URL, "RUNPILOT_TEST_KEY")})

	mkLease := func(sid string, slot int) {
		t.Helper()
		if _, err := st.LeaseCreateIfAbsent(model.Lease{
			SID: sid, Server: "a", Slot: slot, State: model.LeaseActive,
			Origin: model.LeaseOriginDispatch,
		}, 2, true); err != nil {
			t.Fatal(err)
		}
	}
	leased := func(sid string) bool {
		_, err := st.LeaseGet(sid)
		return err == nil
	}

	// (A) inflight=0 → аренда снимается сразу.
	s1 := mkSession(t, st, model.SessionRunning)
	mkLease(s1, 1)
	gw.ReleaseOnGone(s1, model.ReleaseCrash)
	if leased(s1) {
		t.Errorf("inflight=0: аренда должна быть снята сразу")
	}

	// (B) inflight>0 → не снимается до завершения запроса.
	s2 := mkSession(t, st, model.SessionRunning)
	mkLease(s2, 2)
	gw.bumpInflight(s2, "a") // идущий запрос сессии
	gw.ReleaseOnGone(s2, model.ReleaseCrash)
	if !leased(s2) {
		t.Fatalf("inflight=1: аренда снята ДО завершения запроса")
	}
	gw.unBumpInflight(s2, "a") // запрос завершён (t_end записан)
	if leased(s2) {
		t.Errorf("после unBump (inflight=0): аренда не снята")
	}
}

// TestCancelSID — отмена только запросов данной сессии, чужие не трогает.
func TestCancelSID(t *testing.T) {
	g := &Gateway{
		inflightCtx: map[int]context.CancelCauseFunc{},
		inflightSID: map[int]string{},
	}
	ctxA, cancelA := context.WithCancelCause(context.Background())
	ctxB, cancelB := context.WithCancelCause(context.Background())
	idA := g.registerInflight("sidA", cancelA)
	idB := g.registerInflight("sidB", cancelB)
	defer func() {
		g.unregisterInflight(idA)
		g.unregisterInflight(idB)
	}()

	g.CancelSID("sidA")
	if ctxA.Err() == nil {
		t.Errorf("CancelSID(sidA): контекст sidA не отменён")
	}
	if ctxB.Err() != nil {
		t.Errorf("CancelSID(sidA): чужой контекст sidB отменён")
	}
}

// TestGoneBreaksStreamBeforeRelease — сценарий GONE end-to-end (тест
// задачи 1b): потоковый запрос идёт, сессия переходит в GONE →
// (а) клиент видит обрыв потока (нет [DONE]);
// (б) fakellm фиксирует разрыв соединения;
// (в) LEASE_RELEASE не раньше разрыва (аренда снята после inflight=0).
func TestGoneBreaksStreamBeforeRelease(t *testing.T) {
	a := fakellm.Start("srv-model")
	defer a.Close()
	a.SetDelays(0, 30*time.Millisecond, 60) // поток ~1.8 с
	t.Setenv("RUNPILOT_TEST_KEY", "secret-key")
	st, gw, ts := testEnv(t, []config.Server{srvCfg("a", 10, 1, a.URL, "RUNPILOT_TEST_KEY")})

	s := mkSession(t, st, model.SessionRunning)
	leaseID, err := st.LeaseCreate(model.Lease{
		SID: s, Server: "a", Slot: 1, State: model.LeaseActive,
		Origin: model.LeaseOriginDispatch, GrantedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Потоковый запрос (клиент) — в отдельной горутине.
	clientBody := make(chan string, 1)
	go func() {
		resp, err := http.Post(ts.URL+"/s/"+s+"/v1/chat/completions",
			"application/json", strings.NewReader(chatBody("runpilot", `"stream":true`)))
		if err != nil {
			clientBody <- ""
			return
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		clientBody <- string(raw)
	}()

	// Дождаться, пока запрос войдёт в inflight.
	deadline := time.Now().Add(3 * time.Second)
	for {
		gw.im.Lock()
		n := gw.bySID[s]
		gw.im.Unlock()
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("запрос не вошёл в inflight")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// GONE: отмена идущих + аренда после inflight=0.
	gw.ReleaseOnGone(s, model.ReleaseCrash)

	// (а) клиент видит обрыв: поток не завершился [DONE].
	if body := <-clientBody; strings.Contains(body, "[DONE]") {
		t.Errorf("поток завершён [DONE] после GONE — отмена не сработала")
	}

	// (б) fakellm зафиксировал разрыв соединения.
	deadline = time.Now().Add(3 * time.Second)
	for a.Disconnects() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("fakellm не зафиксировал разрыв соединения")
		}
		time.Sleep(5 * time.Millisecond)
	}
	tBreak := a.LastDisconnect()

	// (в) аренда снята и LEASE_RELEASE не раньше разрыва.
	deadline = time.Now().Add(3 * time.Second)
	for {
		if _, err := st.LeaseGet(s); errors.Is(err, store.ErrNotFound) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("аренда не снята после GONE")
		}
		time.Sleep(5 * time.Millisecond)
	}
	l, err := st.LeaseGetByID(leaseID)
	if err != nil {
		t.Fatal(err)
	}
	if l.State != model.LeaseReleased {
		t.Fatalf("аренда не RELEASED: %s", l.State)
	}
	if l.ReleasedAt.IsZero() || l.ReleasedAt.Before(tBreak) {
		t.Errorf("LEASE_RELEASE %s раньше разрыва %s", l.ReleasedAt, tBreak)
	}
}
