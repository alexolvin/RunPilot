package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"runpilot/internal/clock"
	"runpilot/internal/config"
	"runpilot/internal/model"
	"runpilot/internal/proto"
	"runpilot/internal/store"

	"github.com/gorilla/websocket"
)

// testServer — координатор на tmp-БД без токена.
func testServer(t *testing.T) (*httptest.Server, *store.Store, *Hub) {
	t.Helper()
	dbDir := t.TempDir()
	st, err := store.Open(filepath.Join(dbDir, "runpilot.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	cfg := config.Defaults()
	clk := clock.NewReal()
	hub := NewHub(clk)
	srv := NewServer(cfg, st, hub, clk, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, st, hub
}

// doJSON — POST/PATCH JSON; возвращает статус и code/detail/sid из тела.
func doJSON(t *testing.T, method, url string, body any) (int, map[string]string) {
	t.Helper()
	data, _ := json.Marshal(body)
	resp, err := http.NewRequest(method, url, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	out, err := http.DefaultClient.Do(resp)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Body.Close()
	raw, _ := io.ReadAll(out.Body)
	var bodyOut struct {
		SID    string `json:"sid"`
		Code   string `json:"code"`
		Detail string `json:"detail"`
	}
	json.Unmarshal(raw, &bodyOut)
	m := map[string]string{"code": bodyOut.Code, "detail": bodyOut.Detail}
	if bodyOut.SID != "" {
		m["sid"] = bodyOut.SID
	}
	return out.StatusCode, m
}

func TestRunNameInUseLive(t *testing.T) {
	ts, _, _ := testServer(t)
	body := map[string]any{"name": "proj", "host": "h1", "host_ip": "127.0.0.1", "profile": "qwen"}
	status, m := doJSON(t, "POST", ts.URL+"/api/v1/run", body)
	if status != http.StatusCreated {
		t.Fatalf("первый run: %d %v", status, m)
	}
	status, m = doJSON(t, "POST", ts.URL+"/api/v1/run", body)
	if status != http.StatusConflict || m["code"] != "NAME_IN_USE" {
		t.Fatalf("повторный run: %d %v, хочу 409 NAME_IN_USE", status, m)
	}
}

func TestRunNameInUseGoneWithinRetention(t *testing.T) {
	ts, st, _ := testServer(t)
	body := map[string]any{"name": "proj", "host": "h1", "host_ip": "127.0.0.1", "profile": "qwen"}
	status, m := doJSON(t, "POST", ts.URL+"/api/v1/run", body)
	if status != http.StatusCreated {
		t.Fatalf("первый run: %d %v", status, m)
	}
	first := m["sid"]
	// GONE «сейчас» — имя занято до retention.
	if err := st.SetSessionState(first, model.SessionGone, "", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	status, m = doJSON(t, "POST", ts.URL+"/api/v1/run", body)
	if status != http.StatusConflict || m["code"] != "NAME_IN_USE" {
		t.Fatalf("GONE в retention: %d %v, хочу 409 NAME_IN_USE", status, m)
	}
	if !strings.Contains(m["detail"], first) {
		t.Fatalf("в сообщении должен быть sid старой записи %s: %q", first, m["detail"])
	}
	// GONE старше retention (7 дней по умолчанию) → имя свободно.
	old := time.Now().UTC().AddDate(0, 0, -8)
	if err := st.SetSessionState(first, model.SessionGone, "", old); err != nil {
		t.Fatal(err)
	}
	status, m = doJSON(t, "POST", ts.URL+"/api/v1/run", body)
	if status != http.StatusCreated {
		t.Fatalf("GONE старше retention: %d %v, хочу 201", status, m)
	}
}

func TestRunBadAgent(t *testing.T) {
	ts, _, _ := testServer(t)
	body := map[string]any{"name": "p", "host": "h", "host_ip": "127.0.0.1", "profile": "claude"}
	status, m := doJSON(t, "POST", ts.URL+"/api/v1/run", body)
	if status != http.StatusBadRequest || m["code"] != "BAD_AGENT" {
		t.Fatalf("%d %v, хочу 400 BAD_AGENT", status, m)
	}
}

// wsURL — ws-адрес координатора из http-адреса.
func wsURL(httpURL string) string {
	return "ws" + strings.TrimPrefix(httpURL, "http") + "/api/v1/node"
}

// expectClosed — соединение закрыто с кодом протокольной ошибки.
func expectClosed(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	_, data, err := conn.ReadMessage()
	var cerr *websocket.CloseError
	if errors.As(err, &cerr) {
		if cerr.Code != websocket.CloseProtocolError {
			t.Fatalf("код закрытия = %d, хочу %d", cerr.Code, websocket.CloseProtocolError)
		}
		return
	}
	if err != nil {
		t.Fatalf("ожидал close: %v", err)
	}
	if len(data) >= 2 {
		code := int(data[0])<<8 | int(data[1])
		if code != websocket.CloseProtocolError {
			t.Fatalf("код закрытия = %d, хочу %d", code, websocket.CloseProtocolError)
		}
		return
	}
	t.Fatalf("close без кода: %v", data)
}

// W9 доп-3c: hello-ответ — config с model_alias/gateway_url (эндпоинт
// qwen code); узел по нему приводит ~/.qwen/settings.json в порядок.
func TestNodeHelloSendsConfigWithModelAlias(t *testing.T) {
	ts, _, _ := testServer(t)
	dialer := websocket.Dialer{}
	conn, _, err := dialer.Dial(wsURL(ts.URL), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	m := proto.New(proto.KindHello)
	m.Host, m.HostIP = "h1", "127.0.0.1"
	if err := conn.WriteJSON(m); err != nil {
		t.Fatal(err)
	}
	// Первый ответ координатора — config.
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var got proto.Msg
	if err := conn.ReadJSON(&got); err != nil {
		t.Fatalf("ожидал config после hello: %v", err)
	}
	if got.Type != proto.KindConfig {
		t.Fatalf("первый ответ = %s, хочу config", got.Type)
	}
	if got.ModelAlias != "qwen3.6-27b" {
		t.Fatalf("model_alias=%q, хочу qwen3.6-27b", got.ModelAlias)
	}
	if got.GatewayURL == "" {
		t.Fatal("gateway_url пуст, а должен быть из конфига")
	}
}

func TestNodeWSUnknownProtoClosed(t *testing.T) {
	ts, _, _ := testServer(t)
	dialer := websocket.Dialer{}
	conn, _, err := dialer.Dial(wsURL(ts.URL), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	m := proto.New(proto.KindHello)
	m.Proto = 999
	m.Host, m.HostIP = "h1", "127.0.0.1"
	if err := conn.WriteJSON(m); err != nil {
		t.Fatal(err)
	}
	expectClosed(t, conn)
}

func TestNodeWSHostIPMismatchClosed(t *testing.T) {
	ts, _, _ := testServer(t)
	dialer := websocket.Dialer{}
	conn, _, err := dialer.Dial(wsURL(ts.URL), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	m := proto.New(proto.KindHello)
	m.Host, m.HostIP = "h1", "10.9.9.9" // не совпадает с удалённым адресом
	if err := conn.WriteJSON(m); err != nil {
		t.Fatal(err)
	}
	expectClosed(t, conn)
}

func TestNodePaneFlow(t *testing.T) {
	ts, _, hub := testServer(t)
	dialer := websocket.Dialer{}
	conn, _, err := dialer.Dial(wsURL(ts.URL), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	m := proto.New(proto.KindHello)
	m.Host, m.HostIP, m.RUNPILOTVersion = "h1", "127.0.0.1", "0.2.0"
	if err := conn.WriteJSON(m); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if _, ok := hub.Node("h1"); !ok {
		t.Fatal("узел не зарегистрирован после hello")
	}

	// pane → hub с received_at координатора.
	before := time.Now()
	pm := proto.PaneOf(proto.Pane{PaneID: "%42", SID: "S1", State: "IDLE", Hash: 7})
	if err := conn.WriteJSON(pm); err != nil {
		t.Fatal(err)
	}
	var info *PaneInfo
	ok := false
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if info, ok = hub.Pane("%42"); ok {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !ok {
		t.Fatal("pane не пришёл в hub")
	}
	if info.ReceivedAt.Before(before.Add(-time.Second)) || info.ReceivedAt.After(time.Now().Add(time.Second)) {
		t.Fatalf("received_at = %s вне разумного окна", info.ReceivedAt)
	}

	// state содержит снимок.
	resp, err := http.Get(ts.URL + "/api/v1/state")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var st State
	json.NewDecoder(resp.Body).Decode(&st)
	if len(st.Panes) != 1 || st.Panes[0].Pane.PaneID != "%42" {
		t.Fatalf("state.panes = %+v", st.Panes)
	}
}

// TestHubDispatchRoundTrip — координатор → узел (WS) → reply, полный путь:
// /api/v1/internal/dispatch → hub.Dispatch → WS → reply → ответ HTTP.
func TestHubDispatchRoundTrip(t *testing.T) {
	ts, _, hub := testServer(t)
	dialer := websocket.Dialer{}
	conn, _, err := dialer.Dial(wsURL(ts.URL), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	hello := proto.New(proto.KindHello)
	hello.Host, hello.HostIP = "h1", "127.0.0.1"
	if err := conn.WriteJSON(hello); err != nil {
		t.Fatal(err)
	}
	pane := proto.PaneOf(proto.Pane{PaneID: "%7", SID: "S7", State: "IDLE", Hash: 1})
	if err := conn.WriteJSON(pane); err != nil {
		t.Fatal(err)
	}

	// Фейковый узел: dispatch → reply SENT.
	go func() {
		for {
			var m proto.Msg
			if err := conn.ReadJSON(&m); err != nil {
				return
			}
			if m.Type != proto.KindDispatch {
				continue
			}
			reply := proto.New(proto.KindReply)
			reply.CmdID, reply.Result = m.CmdID, "SENT"
			if err := conn.WriteJSON(reply); err != nil {
				return
			}
		}
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := hub.PaneBySID("S7"); ok {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	body := map[string]any{"sid": "S7", "mode": "submit"}
	data, _ := json.Marshal(body)
	resp, err := http.Post(ts.URL+"/api/v1/internal/dispatch",
		"application/json", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("dispatch: %d %s", resp.StatusCode, raw)
	}
	var out struct {
		Result string `json:"result"`
		Detail string `json:"detail"`
		RTTMS  int64  `json:"rtt_ms"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if out.Result != "SENT" {
		t.Fatalf("result = %s, хочу SENT", out.Result)
	}
	if out.RTTMS < 0 || out.RTTMS > 5000 {
		t.Fatalf("rtt_ms = %d, вне разумного", out.RTTMS)
	}
}
