package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// TestCommandHelp — /help и /help <cmd> (инлайн, без Executor).
func TestCommandHelp(t *testing.T) {
	ts, _, _ := testServer(t)
	srv := ts.URL

	st, out := doCommand(t, srv, "/help")
	if st != http.StatusOK {
		t.Fatalf("/help: статус %d %v", st, out)
	}
	if out["code"] != "OK" {
		t.Fatalf("/help code = %v, хочу OK", out["code"])
	}
	txt, _ := out["text"].(string)
	if !strings.Contains(txt, "/pause") {
		t.Fatalf("/help: нет /pause в %q", txt)
	}
	st, out = doCommand(t, srv, "/help pause")
	if st != http.StatusOK || !strings.Contains(outText(out), "/pause") {
		t.Fatalf("/help pause: %d %q", st, outText(out))
	}
}

// TestCommandUnknown — неизвестная команда → 404 UNKNOWN_COMMAND.
func TestCommandUnknown(t *testing.T) {
	ts, _, _ := testServer(t)
	st, out := doCommand(t, ts.URL, "/нет-такой-команды")
	if st != http.StatusNotFound {
		t.Fatalf("/нет-такой: статус %d %v", st, out)
	}
}

// TestCommandPasteNoPane — /go: сессия есть, панели нет → PASTE (PANE_NOT_FOUND).
func TestCommandPasteNoPane(t *testing.T) {
	ts, _, _ := testServer(t)
	// Сессия есть (run), но узла/панели нет → CmdPaste вернёт PANE_NOT_FOUND.
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
	st, out := doCommand(t, ts.URL, "/go alpha hello world")
	if st != http.StatusOK {
		t.Fatalf("/go: статус %d %v", st, out)
	}
	if out["code"] != "PANE_NOT_FOUND" {
		t.Fatalf("/go code = %v, хочу PANE_NOT_FOUND", out["code"])
	}
}

// TestCommandNewNotImpl — /new → NOT_IMPLEMENTED (в этом инкременте).
func TestCommandNewNotImpl(t *testing.T) {
	ts, _, _ := testServer(t)
	st, out := doCommand(t, ts.URL, "/new node1 /repo/a")
	if st != http.StatusOK {
		t.Fatalf("/new: статус %d %v", st, out)
	}
	if out["code"] != "NOT_IMPLEMENTED" {
		t.Fatalf("/new code = %v, хочу NOT_IMPLEMENTED", out["code"])
	}
}

// TestCommandEmpty — пустая строка → 400.
func TestCommandEmpty(t *testing.T) {
	ts, _, _ := testServer(t)
	st, _ := doCommand(t, ts.URL, "   ")
	if st != http.StatusBadRequest {
		t.Fatalf("пусто: статус %d, хочу 400", st)
	}
}

// TestCommandComplete — подсказки команд по префиксу.
func TestCommandComplete(t *testing.T) {
	ts, _, _ := testServer(t)
	resp, err := http.Get(ts.URL + "/api/v1/command/complete?line=" + url.QueryEscape("/pa"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var ins []commandCompletion
	if err := json.NewDecoder(resp.Body).Decode(&ins); err != nil {
		t.Fatalf("decode: %v", err)
	}
	found := false
	for _, c := range ins {
		if c.Text == "pause " {
			found = true
		}
	}
	if !found {
		t.Fatalf("complete(/pa): нет pause в %v", ins)
	}
}

type commandCompletion struct {
	Text        string `json:"text"`
	Description string `json:"description"`
}

// doCommand — POST /api/v1/command, возвращает статус + тело (map).
func doCommand(t *testing.T, base, line string) (int, map[string]any) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"line": line})
	resp, err := http.Post(base+"/api/v1/command", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	if out == nil {
		out = map[string]any{}
	}
	return resp.StatusCode, out
}

// outText — поле text из тела.
func outText(out map[string]any) string {
	s, _ := out["text"].(string)
	return s
}

// TestActionPasteNoPane — POST /sessions/{t}/paste: сессия есть, панели нет →
// PANE_NOT_FOUND (endpoint разбит, текст многострочный, без узла).
func TestActionPasteNoPane(t *testing.T) {
	ts, _, _ := testServer(t)
	body, _ := json.Marshal(map[string]any{
		"name": "beta", "host": "h1", "host_ip": "127.0.0.1", "profile": "qwen",
	})
	resp, err := http.Post(ts.URL+"/api/v1/run", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("run: %d", resp.StatusCode)
	}
	pb, _ := json.Marshal(map[string]any{"text": "строка 1\nстрока 2", "enqueue": false})
	r2, err := http.Post(ts.URL+"/api/v1/sessions/beta/paste", "application/json", bytes.NewReader(pb))
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Body.Close()
	raw, _ := io.ReadAll(r2.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	if out["code"] != "PANE_NOT_FOUND" {
		t.Fatalf("paste code = %v тело %s, хочу PANE_NOT_FOUND", out["code"], raw)
	}
}

