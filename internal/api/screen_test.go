package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

// TestScreenNoSession — GET /sessions/<t>/screen: сессии нет → 404.
func TestScreenNoSession(t *testing.T) {
	ts, _, _ := testServer(t)
	resp, err := http.Get(ts.URL + "/api/v1/sessions/нет-такой/screen")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("статус = %d, хочу 404", resp.StatusCode)
	}
}

// TestScreenNoPane — сессия есть, узла/панели нет → 200 пустой кадр.
func TestScreenNoPane(t *testing.T) {
	ts, _, _ := testServer(t)
	body, _ := json.Marshal(map[string]any{
		"name": "alpha", "host": "h1", "host_ip": "127.0.0.1", "profile": "qwen",
	})
	resp, err := http.Post(ts.URL+"/api/v1/run", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("run: %d", resp.StatusCode)
	}
	resp2, err := http.Get(ts.URL + "/api/v1/sessions/alpha/screen")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("screen: статус %d тело %s, хочу 200", resp2.StatusCode, raw)
	}
	var frame struct {
		TextANSI string `json:"text_ansi"`
	}
	_ = json.Unmarshal(raw, &frame)
	if frame.TextANSI != "" {
		t.Fatalf("text_ansi = %q, хочу пустой кадр", frame.TextANSI)
	}
}
