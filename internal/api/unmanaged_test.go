package api

// 8.3 X1 / 5.4: pane_gone дельта, сверка с БД (потерянные сессии),
// DELETE GONE-сессий и adopt открытых tmux-панелей.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"runpilot/internal/clock"
	"runpilot/internal/config"
	"runpilot/internal/model"
	"runpilot/internal/proto"
	"runpilot/internal/scheduler"
	"runpilot/internal/store"

	"github.com/gorilla/websocket"
)

// unmanagedTestServer — координатор с планировщиком (нужен для GONE-переходов).
func unmanagedTestServer(t *testing.T) (*httptest.Server, *store.Store, *Server, *Hub) {
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
	sch := scheduler.New(scheduler.Options{
		Cfg: cfg, Store: st, Clk: clk,
		Servers: &apiFakeServers{}, Panes: &apiFakePanes{},
		Dispatch: func(context.Context, string, proto.Msg) (proto.Msg, error) {
			return proto.Msg{}, errors.New("off")
		},
		ResumeText: "continue", NoAsyncDispatch: true,
	})
	srv.SetScheduler(sch)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, st, srv, hub
}

// waitForState — ожидание состояния сессии в БД.
func waitForState(t *testing.T, st *store.Store, sid string, want model.SessionState) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		rec, err := st.GetSession(sid)
		if err == nil && rec.State == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	rec, _ := st.GetSession(sid)
	t.Fatalf("сессия %s: state=%v, хочу %v", sid, rec.State, want)
}

func seedSession(t *testing.T, st *store.Store, sid, name, host string, state model.SessionState) {
	t.Helper()
	now := time.Now().UTC()
	if err := st.CreateSession(store.SessionRecord{
		SID: sid, Name: name, Host: host, HostIP: "127.0.0.1",
		Profile: "qwen", State: state, StateChangedAt: now,
		Class: model.ClassNormal, ConstraintKind: model.ConstraintNone,
		CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
}

// TestOnPaneGone — дельта узла: снимок снят, сессия → GONE.
func TestOnPaneGone(t *testing.T) {
	_, st, srv, hub := unmanagedTestServer(t)
	seedSession(t, st, "SG1", "gone1", "h1", model.SessionIdle)
	hub.SetPane("h1", proto.Pane{PaneID: "%42", SID: "SG1", State: "IDLE"})
	srv.onPaneGone("%42", "")
	if _, ok := hub.Pane("%42"); ok {
		t.Fatal("снимок панели остался после pane_gone")
	}
	waitForState(t, st, "SG1", model.SessionGone)
}

// TestApplyPanesReconciliation — «потерянная сессия» (ocNextLife): координатор
// перезапустился, полный список пришёл без панели, hub её не знал — БД-сверка
// ведёт запись в GONE.
func TestApplyPanesReconciliation(t *testing.T) {
	_, st, srv, _ := unmanagedTestServer(t)
	seedSession(t, st, "LOST", "lost", "h1", model.SessionIdle)
	// Полный список узла: панель сессии отсутствует (hub пуст).
	srv.applyPanes("h1", nil)
	waitForState(t, st, "LOST", model.SessionGone)
}

// TestApplyPanesKeepsLive — панель есть в полном списке → сессия не тронута.
func TestApplyPanesKeepsLive(t *testing.T) {
	_, st, srv, _ := unmanagedTestServer(t)
	seedSession(t, st, "LIVE", "live", "h1", model.SessionIdle)
	srv.applyPanes("h1", []proto.Pane{{PaneID: "%50", SID: "LIVE", State: "IDLE"}})
	rec, err := st.GetSession("LIVE")
	if err != nil {
		t.Fatal(err)
	}
	if rec.State != model.SessionIdle {
		t.Fatalf("state=%v, хочу IDLE (панель в списке)", rec.State)
	}
}

// TestSessionDelete — DELETE только для GONE.
func TestSessionDelete(t *testing.T) {
	ts, st, _, _ := unmanagedTestServer(t)
	seedSession(t, st, "DL1", "del1", "h1", model.SessionIdle)
	// Не-GONE → 409.
	status, m := doJSON(t, "DELETE", ts.URL+"/api/v1/sessions/DL1", nil)
	if status != http.StatusConflict || m["code"] != "NOT_GONE" {
		t.Fatalf("не-GONE: %d %v, хочу 409 NOT_GONE", status, m)
	}
	_ = st.SetSessionState("DL1", model.SessionGone, "", time.Now().UTC())
	status, _ = doJSON(t, "DELETE", ts.URL+"/api/v1/sessions/DL1", nil)
	if status != http.StatusOK {
		t.Fatalf("GONE: %d, хочу 200", status)
	}
	if _, err := st.GetSession("DL1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("запись осталась: %v", err)
	}
	// Повторный DELETE → 404.
	status, _ = doJSON(t, "DELETE", ts.URL+"/api/v1/sessions/DL1", nil)
	if status != http.StatusNotFound {
		t.Fatalf("повтор: %d, хочу 404", status)
	}
}

// waitNode — дождаться регистрации узла в hub (hello асинхронно).
func waitNode(t *testing.T, hub *Hub, host string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := hub.Node(host); ok {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("узел %s не зарегистрирован", host)
}

// fakeNodeAdopt — WS-узел, отвечающий на команды (KindAdopt → result).
func fakeNodeAdopt(t *testing.T, ts *httptest.Server, host, result string) {
	t.Helper()
	dialer := websocket.Dialer{}
	conn, _, err := dialer.Dial(wsURL(ts.URL), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	hello := proto.New(proto.KindHello)
	hello.Host, hello.HostIP = host, "127.0.0.1"
	if err := conn.WriteJSON(hello); err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			var m proto.Msg
			if err := conn.ReadJSON(&m); err != nil {
				return
			}
			if m.Type != proto.KindAdopt {
				continue
			}
			reply := proto.New(proto.KindReply)
			reply.CmdID, reply.Result = m.CmdID, result
			if err := conn.WriteJSON(reply); err != nil {
				return
			}
		}
	}()
}

// TestAdoptEndpoint — adopt UNMANAGED-панели: сессия создана, 200 + sid.
func TestAdoptEndpoint(t *testing.T) {
	ts, st, _, hub := unmanagedTestServer(t)
	fakeNodeAdopt(t, ts, "h1", proto.ResAdopted)
	waitNode(t, hub, "h1")
	hub.SetUnmanaged("h1", []proto.UnmanagedPane{
		{PaneID: "%30", Session: "manproj", Dir: "~/proj", Socket: "default"},
	})
	body := bytes.NewReader(nil)
	req, err := http.NewRequest(http.MethodPost,
		ts.URL+"/api/v1/unmanaged/h1/"+url.PathEscape("%30")+"/adopt", body)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("adopt: %d %s", resp.StatusCode, raw)
	}
	var out struct {
		SID  string `json:"sid"`
		Name string `json:"name"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if out.SID == "" || out.Name != "manproj" {
		t.Fatalf("ответ: %+v (name должен быть manproj)", out)
	}
	rec, err := st.GetSession(out.SID)
	if err != nil {
		t.Fatal(err)
	}
	if rec.State != model.SessionIdle || rec.Host != "h1" || rec.Name != "manproj" {
		t.Fatalf("запись: %+v", rec)
	}
}

// TestAdoptEndpointNotIdle — кодер в панели не в простое → 409 NOT_IDLE.
func TestAdoptEndpointNotIdle(t *testing.T) {
	ts, _, _, hub := unmanagedTestServer(t)
	fakeNodeAdopt(t, ts, "h1", proto.ResNotIdle)
	waitNode(t, hub, "h1")
	hub.SetUnmanaged("h1", []proto.UnmanagedPane{{PaneID: "%30", Session: "m"}})
	resp, err := http.Post(ts.URL+"/api/v1/unmanaged/h1/"+url.PathEscape("%30")+"/adopt",
		"application/json", bytes.NewReader(nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("NOT_IDLE: %d %s, хочу 409", resp.StatusCode, raw)
	}
}

// TestAdoptEndpointUnknownPane — панели нет в живом списке → 404.
func TestAdoptEndpointUnknownPane(t *testing.T) {
	ts, _, _, hub := unmanagedTestServer(t)
	fakeNodeAdopt(t, ts, "h1", proto.ResAdopted)
	waitNode(t, hub, "h1")
	resp, err := http.Post(ts.URL+"/api/v1/unmanaged/h1/"+url.PathEscape("%99")+"/adopt",
		"application/json", bytes.NewReader(nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("неизвестная панель: %d, хочу 404", resp.StatusCode)
	}
}

// TestCmdCloseLostSession — «Закрыть» для потерянной сессии (панели в hub
// нет) → GONE без операции узла.
func TestCmdCloseLostSession(t *testing.T) {
	_, st, srv, _ := unmanagedTestServer(t)
	seedSession(t, st, "CL1", "close1", "h1", model.SessionIdle)
	out, err := srv.newExecutor().Close(context.Background(), "CL1")
	if err != nil {
		t.Fatal(err)
	}
	if out.Code != "OK" {
		t.Fatalf("Close: %+v", out)
	}
	waitForState(t, st, "CL1", model.SessionGone)
}

// TestCmdCloseOwned — «Закрыть» owned-сессию: kill_session на узле → GONE.
func TestCmdCloseOwned(t *testing.T) {
	ts, st, srv, hub := unmanagedTestServer(t)
	// Узел отвечает KILLED на любую панельную команду.
	dialer := websocket.Dialer{}
	conn, _, err := dialer.Dial(wsURL(ts.URL), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	hello := proto.New(proto.KindHello)
	hello.Host, hello.HostIP = "h1", "127.0.0.1"
	if err := conn.WriteJSON(hello); err != nil {
		t.Fatal(err)
	}
	seedSession(t, st, "CL2", "close2", "h1", model.SessionIdle)
	hub.SetPane("h1", proto.Pane{PaneID: "%60", SID: "CL2", State: "IDLE"})
	go func() {
		for {
			var m proto.Msg
			if err := conn.ReadJSON(&m); err != nil {
				return
			}
			if m.Type != proto.KindKillSession {
				continue
			}
			reply := proto.New(proto.KindReply)
			reply.CmdID, reply.Result = m.CmdID, proto.ResKilled
			if err := conn.WriteJSON(reply); err != nil {
				return
			}
		}
	}()
	out, err := srv.newExecutor().Close(context.Background(), "CL2")
	if err != nil {
		t.Fatal(err)
	}
	if out.Code != "OK" {
		t.Fatalf("Close: %+v", out)
	}
	waitForState(t, st, "CL2", model.SessionGone)
	if _, ok := hub.PaneBySID("CL2"); ok {
		t.Fatal("снимок панели остался после close")
	}
}

// fakeNodeReplyKind — WS-узел, отвечающий на конкретный kind командой→result.
func fakeNodeReplyKind(t *testing.T, ts *httptest.Server, host string, kind proto.Kind, result, detail string) {
	t.Helper()
	dialer := websocket.Dialer{}
	conn, _, err := dialer.Dial(wsURL(ts.URL), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	hello := proto.New(proto.KindHello)
	hello.Host, hello.HostIP = host, "127.0.0.1"
	if err := conn.WriteJSON(hello); err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			var m proto.Msg
			if err := conn.ReadJSON(&m); err != nil {
				return
			}
			if m.Type != kind {
				continue
			}
			reply := proto.New(proto.KindReply)
			reply.CmdID, reply.Result, reply.Detail = m.CmdID, result, detail
			if err := conn.WriteJSON(reply); err != nil {
				return
			}
		}
	}()
}

// TestNodeQwenSettingsEndpoint — POST /nodes/{host}/qwen-settings (доп-3i).
func TestNodeQwenSettingsEndpoint(t *testing.T) {
	ts, _, _, hub := unmanagedTestServer(t)
	fakeNodeReplyKind(t, ts, "h1", proto.KindQwenSettings,
		proto.ResQwenSettingsChanged, "security.auth.selectedType: qwen → openai")
	waitNode(t, hub, "h1")
	resp, err := http.Post(ts.URL+"/api/v1/nodes/h1/qwen-settings", "application/json", bytes.NewReader(nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("qwen-settings: %d %s", resp.StatusCode, raw)
	}
	var out struct {
		Status  string `json:"status"`
		Message string `json:"message"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if out.Status != "changed" || out.Message == "" {
		t.Fatalf("ответ: %+v (хочу changed + message)", out)
	}
}

// TestNodeQwenSettingsNoNode — узел офлайн → 503.
func TestNodeQwenSettingsNoNode(t *testing.T) {
	ts, _, _, _ := unmanagedTestServer(t)
	resp, err := http.Post(ts.URL+"/api/v1/nodes/ghost/qwen-settings", "application/json", bytes.NewReader(nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("нет узла: %d, хочу 503", resp.StatusCode)
	}
}

// TestUnmanagedInState — /state несёт секцию unmanaged.
func TestUnmanagedInState(t *testing.T) {
	ts, _, _, hub := unmanagedTestServer(t)
	hub.SetUnmanaged("h1", []proto.UnmanagedPane{{PaneID: "%30", Session: "m"}})
	resp, err := http.Get(ts.URL + "/api/v1/state")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var st StateView
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		t.Fatal(err)
	}
	if len(st.Unmanaged) != 1 || st.Unmanaged[0].PaneID != "%30" ||
		st.Unmanaged[0].Host != "h1" {
		t.Fatalf("state.unmanaged = %+v", st.Unmanaged)
	}
}
