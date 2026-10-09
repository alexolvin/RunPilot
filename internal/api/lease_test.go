package api

// Административное снятие аренды: POST /api/v1/leases/release.

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"runpilot/internal/model"
	"runpilot/internal/sid"
	"runpilot/internal/store"
)

func TestLeaseReleaseOperator(t *testing.T) {
	ts, st, _ := testServer(t)
	s, _ := sid.New()
	now := time.Now().UTC()
	// Сессия + аренда ACTIVE.
	_ = st.CreateSession(store.SessionRecord{
		SID: s, Name: "lease-rel", Host: "h", HostIP: "127.0.0.1",
		Profile: "qwen", State: model.SessionRunning, StateChangedAt: now,
		Class: model.ClassNormal, ConstraintKind: model.ConstraintNone, CreatedAt: now,
	})
	_, _ = st.LeaseCreate(model.Lease{
		SID: s, Server: "srv1", Slot: 1, State: model.LeaseActive,
		Origin: model.LeaseOriginImplicit, GrantedAt: now,
	})

	// Снятие аренды.
	status, raw := doJSON(t, http.MethodPost, ts.URL+"/api/v1/leases/release",
		map[string]string{"sid": s})
	if status != http.StatusOK {
		t.Fatalf("lease release: %d %s, хочу 200", status, raw)
	}
	// Аренда RELEASED (LeaseGet ищет только PENDING/ACTIVE/RECOVERING).
	if l, err := st.LeaseGet(s); err == nil {
		t.Fatalf("аренда ещё активна: %s", l.State)
	}
	// Событие LEASE_RELEASE с причиной OPERATOR.
	evs, _ := st.EventList(0, 100)
	var found bool
	for _, e := range evs {
		if e.Kind == model.KindLeaseRelease && e.SID == s {
			var pl struct {
				Reason string `json:"reason"`
			}
			if json.Unmarshal(e.Payload, &pl) == nil && pl.Reason == model.ReleaseOperator {
				found = true
			}
		}
	}
	if !found {
		t.Error("нет события LEASE_RELEASE с причиной OPERATOR")
	}
}

func TestLeaseReleaseNotFound(t *testing.T) {
	ts, _, _ := testServer(t)
	s, _ := sid.New()
	status, _ := doJSON(t, http.MethodPost, ts.URL+"/api/v1/leases/release",
		map[string]string{"sid": s})
	if status != http.StatusNotFound {
		t.Fatalf("lease release (нет аренды): %d, хочу 404", status)
	}
}

func TestLeaseReleaseBadState(t *testing.T) {
	ts, st, _ := testServer(t)
	s, _ := sid.New()
	now := time.Now().UTC()
	_ = st.CreateSession(store.SessionRecord{
		SID: s, Name: "lease-bad", Host: "h", HostIP: "127.0.0.1",
		Profile: "qwen", State: model.SessionIdle, StateChangedAt: now,
		Class: model.ClassNormal, ConstraintKind: model.ConstraintNone, CreatedAt: now,
	})
	// Аренда уже RELEASED.
	_, _ = st.LeaseCreate(model.Lease{
		SID: s, Server: "srv1", Slot: 1, State: model.LeaseActive,
		Origin: model.LeaseOriginImplicit, GrantedAt: now,
	})
	_ = st.LeaseRelease(s, "TURN_DONE", now)

	// RELEASED-аренда не найдена LeaseGet → 404.
	status, _ := doJSON(t, http.MethodPost, ts.URL+"/api/v1/leases/release",
		map[string]string{"sid": s})
	if status != http.StatusNotFound {
		t.Fatalf("lease release (RELEASED): %d, хочу 404", status)
	}
}
