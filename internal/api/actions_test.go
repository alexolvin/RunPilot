package api

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

// W1 CONTROL: схема ответа каждой сущности содержит actions (раздел 15.2).

// requireActions — в каждом объекте сущности есть непустой массив actions.
func requireActions(t *testing.T, label string, objs []map[string]any) {
	t.Helper()
	for _, o := range objs {
		raw, ok := o["actions"]
		if !ok {
			t.Fatalf("%s: нет поля actions: %v", label, o)
		}
		arr, ok := raw.([]any)
		if !ok || len(arr) == 0 {
			t.Fatalf("%s: actions пуст: %v", label, raw)
		}
	}
}

func TestStateCarriesActions(t *testing.T) {
	ts, _, hub := testServer(t)
	// Сессия + узел в срезе.
	_, m := doJSON(t, "POST", ts.URL+"/api/v1/run",
		map[string]any{"name": "proj", "host": "h1", "host_ip": "127.0.0.1", "profile": "qwen"})
	if m["sid"] == "" {
		t.Fatalf("run без sid: %v", m)
	}
	hub.Register(&NodeInfo{Host: "h1", HostIP: "127.0.0.1", RUNPILOTVersion: "v", Conn: nil})

	resp, err := http.Get(ts.URL + "/api/v1/state")
	if err != nil {
		t.Fatal(err)
	}
	var state struct {
		Sessions []map[string]any `json:"sessions"`
		Nodes    []map[string]any `json:"nodes"`
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatalf("state: %v (%s)", err, raw)
	}
	requireActions(t, "state.sessions", state.Sessions)
	requireActions(t, "state.nodes", state.Nodes)
	if len(state.Sessions) == 0 {
		t.Fatalf("нет сессий в state")
	}
	if len(state.Nodes) == 0 {
		t.Fatalf("нет узлов в state")
	}
}

func TestSessionsListCarriesActions(t *testing.T) {
	ts, _, _ := testServer(t)
	doJSON(t, "POST", ts.URL+"/api/v1/run",
		map[string]any{"name": "proj", "host": "h1", "host_ip": "127.0.0.1", "profile": "qwen"})
	resp, err := http.Get(ts.URL + "/api/v1/sessions")
	if err != nil {
		t.Fatal(err)
	}
	var sessions []map[string]any
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err := json.Unmarshal(raw, &sessions); err != nil {
		t.Fatalf("sessions: %v (%s)", err, raw)
	}
	if len(sessions) == 0 {
		t.Fatalf("нет сессий в /sessions")
	}
	requireActions(t, "sessions", sessions)
}
