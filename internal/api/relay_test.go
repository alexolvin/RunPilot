package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"runpilot/internal/model"
	"runpilot/internal/store"
)

// patchSession — PATCH /api/v1/sessions/{t} {orch_url, relay_enabled}.
func patchSession(t *testing.T, ts *httptest.Server, tName string, body map[string]any) (int, []byte) {
	t.Helper()
	data, _ := json.Marshal(body)
	return doRaw(t, "PATCH", ts.URL+"/api/v1/sessions/"+tName, data)
}

// TestRelaySessionPatchLink — PATCH связывает сессию с чатом (fakeorch).
func TestRelaySessionPatchLink(t *testing.T) {
	ts, st, _ := testServer(t)
	seedSession(t, st, "SR1", "relay1", "h1", model.SessionIdle)

	status, raw := patchSession(t, ts, "SR1", map[string]any{
		"orch_url":      "http://orch.test/chat/abc",
		"relay_enabled": true,
	})
	if status != http.StatusOK {
		t.Fatalf("PATCH: %d %s", status, raw)
	}
	var stt relayStatus
	if err := json.Unmarshal(raw, &stt); err != nil {
		t.Fatalf("не JSON: %v", err)
	}
	if !stt.Linked || !stt.Enabled || stt.Site != "fakeorch" || stt.ChatKey != "abc" {
		t.Fatalf("статус = %+v", stt)
	}
	if stt.State != "ARMED" {
		t.Fatalf("state = %q, хочу ARMED", stt.State)
	}
}

// TestRelaySessionPatchUnknownSite — ORCH_SITE_UNKNOWN (422).
func TestRelaySessionPatchUnknownSite(t *testing.T) {
	ts, st, _ := testServer(t)
	seedSession(t, st, "SR2", "relay2", "h1", model.SessionIdle)

	status, raw := patchSession(t, ts, "SR2", map[string]any{
		"orch_url":      "https://unknown.example.com/chat/abc",
		"relay_enabled": true,
	})
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("статус = %d, хочу 422: %s", status, raw)
	}
	var e struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(raw, &e)
	if e.Code != "ORCH_SITE_UNKNOWN" {
		t.Fatalf("code = %q, хочу ORCH_SITE_UNKNOWN", e.Code)
	}
}

// TestRelaySessionPatchChatInUse — ORCH_CHAT_IN_USE (409).
func TestRelaySessionPatchChatInUse(t *testing.T) {
	ts, st, _ := testServer(t)
	seedSession(t, st, "SR3", "relay3", "h1", model.SessionIdle)
	seedSession(t, st, "SR4", "relay4", "h1", model.SessionIdle)

	if status, raw := patchSession(t, ts, "SR3", map[string]any{
		"orch_url": "http://orch.test/chat/shared",
	}); status != http.StatusOK {
		t.Fatalf("первая связь: %d %s", status, raw)
	}
	status, raw := patchSession(t, ts, "SR4", map[string]any{
		"orch_url": "http://orch.test/chat/shared",
	})
	if status != http.StatusConflict {
		t.Fatalf("статус = %d, хочу 409: %s", status, raw)
	}
	var e struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(raw, &e)
	if e.Code != "ORCH_CHAT_IN_USE" {
		t.Fatalf("code = %q, хочу ORCH_CHAT_IN_USE", e.Code)
	}
}

// TestRelaySessionPatchUnlink — выключение связи.
func TestRelaySessionPatchUnlink(t *testing.T) {
	ts, st, _ := testServer(t)
	seedSession(t, st, "SR5", "relay5", "h1", model.SessionIdle)
	if status, raw := patchSession(t, ts, "SR5", map[string]any{
		"orch_url": "http://orch.test/chat/unl",
	}); status != http.StatusOK {
		t.Fatalf("связь: %d %s", status, raw)
	}
	status, raw := patchSession(t, ts, "SR5", map[string]any{
		"orch_url":      "",
		"relay_enabled": false,
	})
	if status != http.StatusOK {
		t.Fatalf("выключение: %d %s", status, raw)
	}
	var stt relayStatus
	_ = json.Unmarshal(raw, &stt)
	if stt.Linked {
		t.Fatalf("связь не снята: %+v", stt)
	}
}

// TestRelayGet — GET /sessions/{t}/relay.
func TestRelayGet(t *testing.T) {
	ts, st, _ := testServer(t)
	seedSession(t, st, "SR6", "relay6", "h1", model.SessionIdle)
	status, raw := doRaw(t, "GET", ts.URL+"/api/v1/sessions/SR6/relay", nil)
	if status != http.StatusOK {
		t.Fatalf("GET relay: %d %s", status, raw)
	}
	var stt relayStatus
	_ = json.Unmarshal(raw, &stt)
	if stt.Linked {
		t.Fatalf("связи нет, но linked=true: %+v", stt)
	}
}

// TestRelayAction — POST /sessions/{t}/relay/{action}.
func TestRelayAction(t *testing.T) {
	ts, st, _ := testServer(t)
	seedSession(t, st, "SR7", "relay7", "h1", model.SessionIdle)
	// Без связи → 409.
	status, raw := doRaw(t, "POST", ts.URL+"/api/v1/sessions/SR7/relay/send", nil)
	if status != http.StatusConflict {
		t.Fatalf("статус = %d, хочу 409: %s", status, raw)
	}
	// Со связью → 200.
	if status, raw := patchSession(t, ts, "SR7", map[string]any{
		"orch_url": "http://orch.test/chat/act",
	}); status != http.StatusOK {
		t.Fatalf("связь: %d %s", status, raw)
	}
	status, raw = doRaw(t, "POST", ts.URL+"/api/v1/sessions/SR7/relay/send", nil)
	if status != http.StatusOK {
		t.Fatalf("action: %d %s", status, raw)
	}
}

// TestRelayCheck — POST /relay/check {url}.
func TestRelayCheck(t *testing.T) {
	ts, _, _ := testServer(t)
	// Известный сайт.
	data, _ := json.Marshal(map[string]any{"url": "http://orch.test/chat/chk"})
	status, raw := doRaw(t, "POST", ts.URL+"/api/v1/relay/check", data)
	if status != http.StatusOK {
		t.Fatalf("check: %d %s", status, raw)
	}
	var res relayCheckResult
	_ = json.Unmarshal(raw, &res)
	if res.Site != "fakeorch" || res.ChatKey != "chk" {
		t.Fatalf("res = %+v", res)
	}
	// Неизвестный сайт → 422.
	data, _ = json.Marshal(map[string]any{"url": "https://nope.example.com/x"})
	status, raw = doRaw(t, "POST", ts.URL+"/api/v1/relay/check", data)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("check unknown: %d, хочу 422: %s", status, raw)
	}
}

// TestBrowserGet — GET /browser (без браузера).
func TestBrowserGet(t *testing.T) {
	ts, _, _ := testServer(t)
	status, raw := doRaw(t, "GET", ts.URL+"/api/v1/browser", nil)
	if status != http.StatusOK {
		t.Fatalf("GET browser: %d %s", status, raw)
	}
	var res struct {
		Running bool `json:"running"`
	}
	_ = json.Unmarshal(raw, &res)
	if res.Running {
		t.Fatalf("running = true, хочу false (браузера нет)")
	}
}

var _ = store.RelayLink{}
