package api

// W13 этап 2: GET /api/v1/state отдаёт сервер текущего слота (server) и
// правило выбора (rule) для сессии с действующей арендой («В работе»).

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"runpilot/internal/model"
)

// testServerRule — сервер + сессия + действующая аренда с правилом.
func TestStateServerRule(t *testing.T) {
	ts, st, _ := testServer(t)
	_, m := doJSON(t, "POST", ts.URL+"/api/v1/run",
		map[string]any{"name": "proj", "host": "h1", "host_ip": "127.0.0.1", "profile": "qwen"})
	sid := m["sid"]
	if sid == "" {
		t.Fatalf("run без sid: %v", m)
	}
	// Действующая аренда с правилом выбора.
	if _, err := st.LeaseCreate(model.Lease{
		SID: sid, Server: "srv1", Slot: 1,
		State: model.LeaseActive, Origin: model.LeaseOriginDispatch,
		GrantedAt: time.Now().UTC(), Rule: "warm_cache",
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
		if sidv != sid {
			continue
		}
		if srv, _ := s["server"].(string); srv != "srv1" {
			t.Fatalf("server=%q, хочу srv1: %v", srv, s)
		}
		if rule, _ := s["rule"].(string); rule != "warm_cache" {
			t.Fatalf("rule=%q, хочу warm_cache: %v", rule, s)
		}
		return
	}
	t.Fatal("сессия не найдена в state")
}

// Сессия без действующей аренды — server/rule в state отсутствуют.
func TestStateNoServerRuleWithoutLease(t *testing.T) {
	ts, _, _ := testServer(t)
	_, m := doJSON(t, "POST", ts.URL+"/api/v1/run",
		map[string]any{"name": "proj", "host": "h1", "host_ip": "127.0.0.1", "profile": "qwen"})
	sid := m["sid"]
	if sid == "" {
		t.Fatalf("run без sid: %v", m)
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
		if sidv != sid {
			continue
		}
		if _, ok := s["server"]; ok {
			t.Fatalf("без аренды server не должен быть в state: %v", s)
		}
		if _, ok := s["rule"]; ok {
			t.Fatalf("без аренды rule не должен быть в state: %v", s)
		}
		return
	}
	t.Fatal("сессия не найдена в state")
}

// LeaseCreate с правилом — правило сохраняется и читается (LeaseGetByID).
func TestLeaseRulePersisted(t *testing.T) {
	_, st, _ := testServer(t)
	id, err := st.LeaseCreate(model.Lease{
		SID: "s1", Server: "srv1", Slot: 1,
		State: model.LeaseActive, Origin: model.LeaseOriginDispatch,
		GrantedAt: time.Now().UTC(), Rule: "priority",
	})
	if err != nil {
		t.Fatal(err)
	}
	l, err := st.LeaseGetByID(id)
	if err != nil {
		t.Fatal(err)
	}
	if l.Rule != "priority" {
		t.Fatalf("rule=%q, хочу priority", l.Rule)
	}
}
