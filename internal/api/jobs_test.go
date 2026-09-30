package api

// Раздел 8.1 ТЗ: /api/v1/jobs/* (runpilot exec) — создание, long-poll, пульс,
// finish + аутентификация (операторский/узелский токен).

import (
	"bytes"
	"context"
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
	"runpilot/internal/scheduler"
	"runpilot/internal/store"
)

type apiFakeServers struct{ list []scheduler.ServerView }

func (f *apiFakeServers) List() []scheduler.ServerView { return f.list }

type apiFakePanes struct{}

func (f *apiFakePanes) PaneBySID(string) (scheduler.PaneSnap, bool)      { return scheduler.PaneSnap{}, false }
func (f *apiFakePanes) PaneByPaneID(string) (scheduler.PaneSnap, bool)   { return scheduler.PaneSnap{}, false }

// jobTestServer — координатор с планировщиком (для /jobs). token — пустой
// (локальный режим) или операторский токен (через RUNPILOT_TOKEN).
func jobTestServer(t *testing.T, token string) (*httptest.Server, *store.Store, *Server) {
	t.Helper()
	if token != "" {
		t.Setenv("RUNPILOT_TOKEN", token)
	}
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
	return ts, st, srv
}

// jobReq — запрос к /jobs с Bearer-токеном (token "" — без заголовка).
func jobReq(t *testing.T, ts *httptest.Server, token, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		rd = bytes.NewReader(data)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, ts.URL+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err == nil {
		for k, v := range m {
			out[k] = v
		}
	}
	out["__raw"] = string(raw)
	return resp.StatusCode, out
}

// TestJobCreate — POST /jobs: сессия JOB + запись в очереди (без панели).
func TestJobCreate(t *testing.T) {
	ts, st, _ := jobTestServer(t, "")
	status, out := jobReq(t, ts, "", http.MethodPost, "/api/v1/jobs",
		map[string]any{"name": "night-build", "prio": "normal"})
	if status != http.StatusCreated {
		t.Fatalf("create: %d %v", status, out)
	}
	sid, _ := out["sid"].(string)
	if sid == "" {
		t.Fatalf("create: нет sid: %v", out)
	}
	rec, err := st.GetSession(sid)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Kind != model.KindJob {
		t.Fatalf("kind=%q, хочу JOB", rec.Kind)
	}
	if rec.State != model.SessionQueued {
		t.Fatalf("state=%s, хочу QUEUED", rec.State)
	}
	if e, err := st.QueueGet(sid); err != nil || e.SID != sid {
		t.Fatalf("запись в очереди: %v %v", e, err)
	}
}

// TestJobCreateBadPrio — неизвестный класс → 400.
func TestJobCreateBadPrio(t *testing.T) {
	ts, _, _ := jobTestServer(t, "")
	status, out := jobReq(t, ts, "", http.MethodPost, "/api/v1/jobs",
		map[string]any{"name": "x", "prio": "bogus"})
	if status != http.StatusBadRequest || out["code"] != "BAD_PRIO" {
		t.Fatalf("%d %v, хочу 400 BAD_PRIO", status, out)
	}
}

// TestJobAuth — раздел 15.3: операторский токен ИЛИ токен узла; неверный → 403.
func TestJobAuth(t *testing.T) {
	ts, st, _ := jobTestServer(t, "op-token")
	// Неверный токен → 403 (→ код выхода 77).
	status, _ := jobReq(t, ts, "wrong", http.MethodPost, "/api/v1/jobs",
		map[string]any{"name": "a"})
	if status != http.StatusForbidden {
		t.Fatalf("неверный токен: %d, хочу 403", status)
	}
	// Операторский токен → 201.
	status, _ = jobReq(t, ts, "op-token", http.MethodPost, "/api/v1/jobs",
		map[string]any{"name": "b"})
	if status != http.StatusCreated {
		t.Fatalf("операторский токен: %d, хочу 201", status)
	}
	// Токен узла (валидный) → 201 (узел не в hub — IP-проверка пропускается).
	tok := store.NewNodeToken("node-01", time.Now())
	if err := st.CreateNodeToken(tok); err != nil {
		t.Fatal(err)
	}
	status, _ = jobReq(t, ts, tok.Token, http.MethodPost, "/api/v1/jobs",
		map[string]any{"name": "c"})
	if status != http.StatusCreated {
		t.Fatalf("токен узла: %d, хочу 201", status)
	}
}

// TestJobWaitReturnsEnv — при активной аренде /wait возвращает окружение шлюза.
func TestJobWaitReturnsEnv(t *testing.T) {
	ts, st, _ := jobTestServer(t, "")
	_, out := jobReq(t, ts, "", http.MethodPost, "/api/v1/jobs",
		map[string]any{"name": "w"})
	sid, _ := out["sid"].(string)
	// Аренда выдана (имитация выдачи планировщиком).
	if _, err := st.LeaseCreateIfAbsent(model.Lease{
		SID: sid, Server: "srv1", Slot: 1, State: model.LeaseActive,
		Origin: model.LeaseOriginDispatch, GrantedAt: time.Now(),
	}, false); err != nil {
		t.Fatal(err)
	}
	status, out := jobReq(t, ts, "", http.MethodGet, "/api/v1/jobs/"+sid+"/wait?wait_sec=1", nil)
	if status != http.StatusOK {
		t.Fatalf("wait: %d %v", status, out)
	}
	env, _ := out["env"].(map[string]any)
	if env == nil {
		t.Fatalf("wait: нет env: %v", out)
	}
	base, _ := env["OPENAI_BASE_URL"].(string)
	if !strings.Contains(base, "/s/"+sid+"/v1") {
		t.Fatalf("OPENAI_BASE_URL=%q, хочу .../s/%s/v1", base, sid)
	}
	if env["OPENAI_API_KEY"] != "runpilot" {
		t.Fatalf("OPENAI_API_KEY=%v, хочу runpilot", env["OPENAI_API_KEY"])
	}
	if env["RUNPILOT_SID"] != sid {
		t.Fatalf("RUNPILOT_SID=%v, хочу %s", env["RUNPILOT_SID"], sid)
	}
}

// TestJobWaitNotYet — аренды нет → 204 (ещё ждём).
func TestJobWaitNotYet(t *testing.T) {
	ts, _, _ := jobTestServer(t, "")
	_, out := jobReq(t, ts, "", http.MethodPost, "/api/v1/jobs",
		map[string]any{"name": "w2"})
	sid, _ := out["sid"].(string)
	status, _ := jobReq(t, ts, "", http.MethodGet, "/api/v1/jobs/"+sid+"/wait?wait_sec=1", nil)
	if status != http.StatusNoContent {
		t.Fatalf("wait без аренды: %d, хочу 204", status)
	}
}

// TestJobHeartbeatCancelOnEmergency — аварийная остановка → cancel=true.
func TestJobHeartbeatCancelOnEmergency(t *testing.T) {
	ts, st, _ := jobTestServer(t, "")
	_, out := jobReq(t, ts, "", http.MethodPost, "/api/v1/jobs",
		map[string]any{"name": "hb"})
	sid, _ := out["sid"].(string)
	// До аварийной остановки — cancel=false.
	status, out := jobReq(t, ts, "", http.MethodPost, "/api/v1/jobs/"+sid+"/heartbeat", nil)
	if status != http.StatusOK || out["cancel"] != false {
		t.Fatalf("пульс до emergency: %d %v, хочу cancel=false", status, out)
	}
	if err := st.SetMode(string(model.ModeEmergency)); err != nil {
		t.Fatal(err)
	}
	status, out = jobReq(t, ts, "", http.MethodPost, "/api/v1/jobs/"+sid+"/heartbeat", nil)
	if status != http.StatusOK || out["cancel"] != true {
		t.Fatalf("пульс в emergency: %d %v, хочу cancel=true", status, out)
	}
}

// TestJobFinish — finish снимает аренду TURN_DONE, сессия GONE.
func TestJobFinish(t *testing.T) {
	ts, st, _ := jobTestServer(t, "")
	_, out := jobReq(t, ts, "", http.MethodPost, "/api/v1/jobs",
		map[string]any{"name": "f"})
	sid, _ := out["sid"].(string)
	if _, err := st.LeaseCreateIfAbsent(model.Lease{
		SID: sid, Server: "srv1", Slot: 1, State: model.LeaseActive,
		Origin: model.LeaseOriginDispatch, GrantedAt: time.Now(),
	}, false); err != nil {
		t.Fatal(err)
	}
	status, out := jobReq(t, ts, "", http.MethodPost, "/api/v1/jobs/"+sid+"/finish",
		map[string]any{"exit_code": 0, "signal": ""})
	if status != http.StatusOK || out["outcome"] != "OK" {
		t.Fatalf("finish(0): %d %v, хочу outcome=OK", status, out)
	}
	if _, err := st.LeaseGet(sid); err == nil {
		t.Fatalf("finish: аренда не снята")
	}
	if rec, err := st.GetSession(sid); err != nil || rec.State != model.SessionGone {
		t.Fatalf("finish: сессия=%+v err=%v, хочу GONE", rec, err)
	}
}
