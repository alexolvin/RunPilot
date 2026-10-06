package api

// п.6: GET /api/v1/state отдаёт held_since сессии, удерживающей запрос
// (queue_entry.held_request=true + held_since задан); у прочих — нет.

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"runpilot/internal/model"
)

func TestStateHeldSince(t *testing.T) {
	ts, st, _ := testServer(t)
	_, m := doJSON(t, "POST", ts.URL+"/api/v1/run",
		map[string]any{"name": "proj", "host": "h1", "host_ip": "127.0.0.1", "profile": "qwen"})
	sid := m["sid"]
	if sid == "" {
		t.Fatalf("run без sid: %v", m)
	}
	// Запись в удержании (как создаёт holdPass): held_request=true, held_since.
	held := time.Now().UTC()
	if err := st.QueueUpsert(model.QueueEntry{
		SID: sid, Class: model.ClassHigh, EnqueuedAt: held, NotBefore: held,
		Mode: model.QueueModeSubmit, HeldRequest: true, HeldSince: held,
	}); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Get(ts.URL + "/api/v1/state")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var state struct {
		Sessions []map[string]any `json:"sessions"`
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatalf("state: %v (%s)", err, raw)
	}
	want := held.Format(time.RFC3339Nano)
	for _, s := range state.Sessions {
		sidv, _ := s["SID"].(string)
		if sidv == "" {
			sidv, _ = s["sid"].(string)
		}
		if sidv != sid {
			continue
		}
		hs, ok := s["held_since"]
		if !ok {
			t.Fatalf("сессия %s: нет held_since: %v", sid, s)
		}
		if str, _ := hs.(string); str != want {
			t.Fatalf("held_since=%q, хочу %q", str, want)
		}
		return
	}
	t.Fatal("сессия не найдена в state")
}

// Неудерживающая сессия (held_request=false) — held_since в state отсутствует.
func TestStateNoHeldSinceWhenNotHeld(t *testing.T) {
	ts, st, _ := testServer(t)
	_, m := doJSON(t, "POST", ts.URL+"/api/v1/run",
		map[string]any{"name": "proj", "host": "h1", "host_ip": "127.0.0.1", "profile": "qwen"})
	sid := m["sid"]
	if sid == "" {
		t.Fatalf("run без sid: %v", m)
	}
	enq := time.Now().UTC()
	if err := st.QueueUpsert(model.QueueEntry{
		SID: sid, Class: model.ClassHigh, EnqueuedAt: enq, NotBefore: enq,
		Mode: model.QueueModeSubmit, HeldRequest: false, HeldSince: enq,
	}); err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(ts.URL + "/api/v1/state")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var state struct {
		Sessions []map[string]any `json:"sessions"`
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatalf("state: %v (%s)", err, raw)
	}
	for _, s := range state.Sessions {
		sidv, _ := s["SID"].(string)
		if sidv == "" {
			sidv, _ = s["sid"].(string)
		}
		if sidv != sid {
			continue
		}
		if _, ok := s["held_since"]; ok {
			t.Fatalf("неудерживающая сессия: held_since не должен быть в state: %v", s)
		}
		return
	}
	t.Fatal("сессия не найдена в state")
}
