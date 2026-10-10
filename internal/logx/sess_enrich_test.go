package logx

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"runpilot/internal/model"
	"runpilot/internal/store"
)

// openStore — тестовая БД с одной сессией.
func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "runpilot.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	now := time.Now().UTC()
	rec := store.SessionRecord{
		SID: "S-a", Name: "task-a", Host: "3p14-aaa", HostIP: "127.0.0.1",
		Profile: "qwen", State: model.SessionIdle, StateChangedAt: now, CreatedAt: now,
	}
	if err := st.CreateSession(rec); err != nil {
		t.Fatal(err)
	}
	return st
}

// logOne — одна запись через обёртку в буфер; возвращает разобранный JSON.
func logOne(t *testing.T, h *SessEnrich, msg string, attrs ...any) map[string]any {
	t.Helper()
	var buf bytes.Buffer
	inner := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	h.inner = inner // пересобираем под буфер
	log := slog.New(h)
	log.Info(msg, attrs...)
	var out map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("разбор лога %q: %v", buf.String(), err)
	}
	return out
}

func TestSessEnrichAddsSession(t *testing.T) {
	st := openStore(t)
	h := NewSessEnrich(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	h.SetStore(st)

	out := logOne(t, h, "hold started", "sid", "S-a")
	if got, ok := out["session"].(string); !ok || got != "3p14-aaa/task-a" {
		t.Fatalf("session = %v, want 3p14-aaa/task-a", out["session"])
	}
	if out["sid"] != "S-a" {
		t.Fatalf("sid = %v, want S-a (должен сохраниться)", out["sid"])
	}
}

// Неизвестный sid — без "session" (поиск не нашёл).
func TestSessEnrichUnknownSID(t *testing.T) {
	st := openStore(t)
	h := NewSessEnrich(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	h.SetStore(st)

	out := logOne(t, h, "msg", "sid", "S-unknown")
	if _, ok := out["session"]; ok {
		t.Fatalf("session должен отсутствовать для неизвестного sid: %v", out)
	}
}

// Без sid — обёртка прозрачна.
func TestSessEnrichNoSID(t *testing.T) {
	st := openStore(t)
	h := NewSessEnrich(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	h.SetStore(st)

	out := logOne(t, h, "msg", "server", "3p14-aaa")
	if _, ok := out["session"]; ok {
		t.Fatalf("session должен отсутствовать без sid: %v", out)
	}
}

// Без store — обёртка прозрачна (до SetStore).
func TestSessEnrichNoStore(t *testing.T) {
	h := NewSessEnrich(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	out := logOne(t, h, "msg", "sid", "S-a")
	if _, ok := out["session"]; ok {
		t.Fatalf("session должен отсутствовать без store: %v", out)
	}
}
