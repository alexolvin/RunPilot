package scheduler

// Самовосстановление: аренды PENDING/ACTIVE/RECOVERING у GONE-сессий
// снимаются с причиной INCONSISTENT.

import (
	"testing"

	"runpilot/internal/model"
	"runpilot/internal/store"
)

func TestGoneLeasesSelfHeal(t *testing.T) {
	h := newHarn(t, []ServerView{srv("s", 10, 1)})
	now := h.clk.Now()

	// Сессия GONE с активной арендой (артефакт: аренда не снята при GONE).
	_ = h.st.CreateSession(store.SessionRecord{
		SID: "s1", Name: "gone", Host: "h", HostIP: "127.0.0.1",
		Profile: "qwen", State: model.SessionGone, StateChangedAt: now,
		Class: model.ClassNormal, ConstraintKind: "none", CreatedAt: now,
	})
	_, _ = h.st.LeaseCreate(model.Lease{
		SID: "s1", Server: "s", Slot: 1, State: model.LeaseActive,
		Origin: model.LeaseOriginImplicit, GrantedAt: now,
	})

	// Тик: аренда должна быть снята.
	h.tick()

	// Аренда больше не активна.
	if _, ok := h.leaseOf("s1"); ok {
		t.Fatal("GONE: аренда не снята после тика")
	}
}

func TestGoneLeasesNotAffectedIfNotGone(t *testing.T) {
	h := newHarn(t, []ServerView{srv("s", 10, 1)})
	now := h.clk.Now()

	// Сессия RUNNING с активной арендой — не должна быть затронута.
	_ = h.st.CreateSession(store.SessionRecord{
		SID: "s1", Name: "run", Host: "h", HostIP: "127.0.0.1",
		Profile: "qwen", State: model.SessionRunning, StateChangedAt: now,
		Class: model.ClassNormal, ConstraintKind: "none", CreatedAt: now,
	})
	_, _ = h.st.LeaseCreate(model.Lease{
		SID: "s1", Server: "s", Slot: 1, State: model.LeaseActive,
		Origin: model.LeaseOriginImplicit, GrantedAt: now,
	})

	h.tick()

	// Аренда на месте.
	if _, ok := h.leaseOf("s1"); !ok {
		t.Fatal("RUNNING: аренда снята ошибочно")
	}
}
