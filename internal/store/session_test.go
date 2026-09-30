package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"runpilot/internal/model"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "runpilot.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestSessionCRUD(t *testing.T) {
	st := openTestStore(t)
	now := time.Now().UTC()
	rec := SessionRecord{
		SID: "ABCDEF1234", Name: "proj", Host: "h1", HostIP: "127.0.0.1",
		Profile: "qwen", State: model.SessionIdle,
		StateChangedAt: now, Class: model.ClassNormal,
		ConstraintKind: model.ConstraintNone, AutoEnqueue: true,
		CreatedAt: now,
	}
	if err := st.CreateSession(rec); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetSession("ABCDEF1234")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "proj" || got.State != model.SessionIdle || !got.AutoEnqueue {
		t.Fatalf("rec = %+v", got)
	}
	if _, err := st.GetSession("NOPE"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("нет sid: %v, хочу ErrNotFound", err)
	}

	// Панель.
	if err := st.UpdateSessionPane("ABCDEF1234", "sess1", "%5", "0.24.4", 0); err != nil {
		t.Fatal(err)
	}
	got, _ = st.GetSession("ABCDEF1234")
	if got.PaneID != "%5" || got.TmuxSession != "sess1" || got.AgentVersion != "0.24.4" {
		t.Fatalf("панель не сохранена: %+v", got)
	}

	// Переход состояния.
	if err := st.SetSessionState("ABCDEF1234", model.SessionHold, model.HoldEmptyInput, now); err != nil {
		t.Fatal(err)
	}
	got, _ = st.GetSession("ABCDEF1234")
	if got.State != model.SessionHold || got.HoldReason != model.HoldEmptyInput {
		t.Fatalf("переход: %+v", got)
	}
}

func TestSessionNameUniqueLive(t *testing.T) {
	st := openTestStore(t)
	now := time.Now().UTC()
	base := SessionRecord{
		Host: "h1", HostIP: "127.0.0.1", Profile: "qwen",
		State: model.SessionIdle, StateChangedAt: now, CreatedAt: now,
	}
	base.SID, base.Name = "AAAABBBBCC", "proj"
	if err := st.CreateSession(base); err != nil {
		t.Fatal(err)
	}
	// Живое имя занято.
	dup := base
	dup.SID = "DDDEEEEFFF"
	if err := st.CreateSession(dup); err == nil {
		t.Fatal("дубль живого имени принят")
	}
	// GONE имя освобождает слот (уникальность только среди не-GONE).
	if err := st.SetSessionState(base.SID, model.SessionGone, "", now); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateSession(dup); err != nil {
		t.Fatalf("имя GONE-сессии должно освободиться: %v", err)
	}
}
