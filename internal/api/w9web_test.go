package api

// W9 (доп): замечания оператора по веб-интерфейсу — регрессии backend-части.
// 1) Журнал «Аудит» — реальные строки (paste/term_open/term_close), не
//    заглушка «появится в W5».
// 2) Журнал «Ходы» — имя сессии (не только служебный sid).
// 3) Probe с пустым HealthURL — health_ok=false (не «сервер отвечает»).

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"runpilot/internal/model"
)

// TestJournalAuditRows — «Аудит»: только действия оператора, payload на месте.
func TestJournalAuditRows(t *testing.T) {
	ts, st, _ := testServer(t)
	createSession(t, st, "S-audit", "task-audit", "h1")

	// Два аудита + одно обычное событие (в «Аудит» попасть не должно).
	for _, kind := range []string{"paste", "term_open", model.KindSessionState} {
		payload, _ := json.Marshal(map[string]any{"operator": "op1"})
		_ = st.EventRecord(model.Event{
			TS: time.Now(), Kind: kind, SID: "S-audit", Payload: payload,
		})
	}

	resp, err := http.Get(ts.URL + "/api/v1/journal?kind=audit&limit=50")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var page struct {
		Rows []struct {
			Kind    string          `json:"kind"`
			SID     string          `json:"sid"`
			Payload json.RawMessage `json:"payload"`
		} `json:"rows"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatalf("audit: %v (тело %s)", err, raw)
	}
	if len(page.Rows) != 2 {
		t.Fatalf("аудит: %d строк, хочу 2 (paste, term_open): %s", len(page.Rows), raw)
	}
	seen := map[string]bool{}
	for _, r := range page.Rows {
		seen[r.Kind] = true
		if r.SID != "S-audit" || len(r.Payload) == 0 {
			t.Errorf("аудит строка без sid/payload: %+v", r)
		}
	}
	if !seen["paste"] || !seen["term_open"] {
		t.Errorf("аудит: не все виды на месте: %v", seen)
	}
}

// TestJournalTurnsSessionName — «Ходы»: человекочитаемое имя сессии к sid.
func TestJournalTurnsSessionName(t *testing.T) {
	ts, st, _ := testServer(t)
	createSession(t, st, "S-turn", "task-web", "h1")

	id, err := st.TurnStart("S-turn", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.TurnClose(id, time.Now().Add(time.Second), model.TurnOK, 3, "srv-01"); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Get(ts.URL + "/api/v1/journal?kind=turns&limit=10")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var page struct {
		Rows []struct {
			SID     string `json:"sid"`
			Session string `json:"session"`
		} `json:"rows"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatalf("turns: %v (тело %s)", err, raw)
	}
	found := false
	for _, r := range page.Rows {
		if r.SID == "S-turn" {
			found = true
			if r.Session != "task-web" {
				t.Errorf("turns: session=%q, хочу %q", r.Session, "task-web")
			}
		}
	}
	if !found {
		t.Fatalf("turns: строка S-turn не найдена: %s", raw)
	}
}

// TestJournalTurnsNameAfterSessionDeleted — имя хода — снимок при старте:
// после удаления сессии «Ходы» всё равно показывают имя, а не голый sid.
func TestJournalTurnsNameAfterSessionDeleted(t *testing.T) {
	ts, st, _ := testServer(t)
	createSession(t, st, "S-del", "task-del", "h1")

	id, err := st.TurnStart("S-del", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.TurnClose(id, time.Now().Add(time.Second), model.TurnOK, 1, "srv-01"); err != nil {
		t.Fatal(err)
	}
	if err := st.DoWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(`DELETE FROM session WHERE sid = ?`, "S-del")
		return err
	}); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Get(ts.URL + "/api/v1/journal?kind=turns&limit=10")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var page struct {
		Rows []struct {
			SID     string `json:"sid"`
			Session string `json:"session"`
		} `json:"rows"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatalf("turns: %v (тело %s)", err, raw)
	}
	found := false
	for _, r := range page.Rows {
		if r.SID == "S-del" {
			found = true
			if r.Session != "task-del" {
				t.Errorf("session после удаления сессии = %q, хочу %q", r.Session, "task-del")
			}
		}
	}
	if !found {
		t.Fatalf("turns: строка S-del не найдена: %s", raw)
	}
}

// TestServerPatchUpstream — PATCH меняет апстрим (редактирование сервера
// в вебе: те же поля, что и в мастере создания).
func TestServerPatchUpstream(t *testing.T) {
	ts, st, _ := testServer(t)
	_, _ = doJSON(t, http.MethodPost, ts.URL+"/api/v1/servers", map[string]any{
		"name": "srv-edit", "slots": 1})

	code, _ := doJSON(t, http.MethodPatch, ts.URL+"/api/v1/servers/srv-edit",
		map[string]any{
			"priority": 77,
			"upstreams": map[string]any{"openai": map[string]any{
				"url": "http://127.0.0.1:9999", "model": "m1", "key_env": "K",
			}},
		})
	if code != http.StatusOK {
		t.Fatalf("PATCH = %d, хочу 200", code)
	}
	c, err := st.GetServer("srv-edit")
	if err != nil {
		t.Fatal(err)
	}
	if c.Priority != 77 {
		t.Errorf("priority = %d, хочу 77", c.Priority)
	}
	u := c.Upstreams.OpenAI
	if u.URL != "http://127.0.0.1:9999" || u.Model != "m1" || u.KeyEnv != "K" {
		t.Errorf("upstream после PATCH: %+v", u)
	}
}

// TestProbeEmptyHealthURL — пустой HealthURL ≠ «сервер отвечает».
func TestProbeEmptyHealthURL(t *testing.T) {
	ts, _, _ := testServer(t)
	resp, err := httpPostJSON(t, ts.URL+"/api/v1/servers/probe", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("probe: %d (тело %s)", resp.StatusCode, raw)
	}
	var out struct {
		HealthOK *bool `json:"health_ok"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("probe: %v (тело %s)", err, raw)
	}
	if out.HealthOK == nil || *out.HealthOK {
		t.Errorf("probe с пустым HealthURL: health_ok=%v, хочу false", out.HealthOK)
	}
}
