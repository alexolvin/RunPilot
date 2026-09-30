package api

// Раздел 8.2 ТЗ: внешние кодеры вне tmux/runpilot — обнаружение (≤2 скана),
// source=cron, target=server:<name>, ignore_rule, GONE.

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"runpilot/internal/clock"
	"runpilot/internal/config"
	"runpilot/internal/model"
	"runpilot/internal/proto"
	"runpilot/internal/store"
)

// extTestServer — координатор с одним сервером (upstream 127.0.0.1:9000) и
// виртуальными часами (детерминированный start_time).
func extTestServer(t *testing.T) (*store.Store, *Server, *clock.Virtual) {
	t.Helper()
	cfg := config.Defaults()
	cfg.Servers = []config.Server{{
		Name:      "srv-01",
		Upstreams: config.Upstreams{OpenAI: config.UpstreamOpenAI{URL: "http://127.0.0.1:9000"}},
	}}
	cfg.Coordinator.GatewayURL = "http://127.0.0.1:8787"
	st, err := store.Open(filepath.Join(t.TempDir(), "runpilot.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	clk := clock.NewVirtual(time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC))
	srv := NewServer(cfg, st, NewHub(clk), clk, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return st, srv, clk
}

func extMsg(procs ...proto.ExternalProc) proto.Msg {
	m := proto.New(proto.KindExternal)
	m.Externals = procs
	return m
}

// TestExternalCronDetected — CONTROL 7: cron-qwen обнаружен, source=cron,
// target=server:srv-01; дублей нет; исчезновение → GONE.
func TestExternalCronDetected(t *testing.T) {
	st, srv, clk := extTestServer(t)
	notified := make(chan string, 4)
	srv.SetNotify(func(text string) { notified <- text })

	// Скан 1: процесс живёт 30 с (etimes). start_time = now − 30.
	clk.Advance(30 * time.Second)
	srv.ApplyNodeMsg("node-01", extMsg(proto.ExternalProc{
		PID: 901, PPID: 900, UID: 1000, StartSec: 30,
		Exe: "qwen", Flags: []string{"--approval-mode"}, Source: "cron",
		OpenAIBaseURL: "http://127.0.0.1:9000/v1",
	}))
	list, err := st.ExternalList()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("обнаружено %d, хочу 1: %+v", len(list), list)
	}
	if list[0].Source != "cron" {
		t.Fatalf("source=%q, хочу cron", list[0].Source)
	}
	if list[0].Target != model.ExtTargetServer+"srv-01" {
		t.Fatalf("target=%q, хочу server:srv-01", list[0].Target)
	}
	select {
	case <-notified:
	default:
		t.Fatal("первое обнаружение не прислало WARN")
	}

	// Скан 2 (1 с позже): etimes вырос на 1, start_time стабилен → без дубля.
	clk.Advance(time.Second)
	srv.ApplyNodeMsg("node-01", extMsg(proto.ExternalProc{
		PID: 901, PPID: 900, UID: 1000, StartSec: 31,
		Exe: "qwen", Flags: []string{"--approval-mode"}, Source: "cron",
		OpenAIBaseURL: "http://127.0.0.1:9000/v1",
	}))
	if list, _ := st.ExternalList(); len(list) != 1 {
		t.Fatalf("дубль: %d строк", len(list))
	}

	// Скан 3: процесс исчез → GONE.
	clk.Advance(time.Second)
	srv.ApplyNodeMsg("node-01", extMsg())
	list, _ = st.ExternalList()
	if len(list) != 1 || list[0].Status != model.ExtGone {
		t.Fatalf("после исчезновения: %+v, хочу GONE", list)
	}
}

// TestExternalTargets — классификация target: gateway:<sid>, server:<name>,
// unknown.
func TestExternalTargets(t *testing.T) {
	st, srv, clk := extTestServer(t)
	clk.Advance(10 * time.Second)
	srv.ApplyNodeMsg("node-01", extMsg(
		proto.ExternalProc{PID: 700, UID: 1, StartSec: 10, Exe: "qwen", Source: "other",
			OpenAIBaseURL: "http://127.0.0.1:8787/s/ABCDEF123456/v1"},
		proto.ExternalProc{PID: 701, UID: 1, StartSec: 10, Exe: "qwen", Source: "sshd",
			OpenAIBaseURL: "http://127.0.0.1:9000/v1"},
		proto.ExternalProc{PID: 702, UID: 1, StartSec: 10, Exe: "qwen", Source: "other",
			OpenAIBaseURL: "http://10.1.2.3:11434/v1"},
	))
	list, err := st.ExternalList()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 {
		t.Fatalf("внешних: %d, хочу 3", len(list))
	}
	by := map[int]model.ExternalProcess{}
	for _, p := range list {
		by[p.PID] = p
	}
	if by[700].Target != model.ExtTargetGateway+"ABCDEF123456" {
		t.Fatalf("target 700=%q, хочу gateway:ABCDEF123456", by[700].Target)
	}
	if by[701].Target != model.ExtTargetServer+"srv-01" {
		t.Fatalf("target 701=%q, хочу server:srv-01", by[701].Target)
	}
	if by[702].Target != model.ExtTargetUnknown {
		t.Fatalf("target 702=%q, хочу unknown", by[702].Target)
	}
}

// TestExternalIgnoreRule — ignore_rule по host/exe: статус IGNORED, без WARN.
func TestExternalIgnoreRule(t *testing.T) {
	st, srv, clk := extTestServer(t)
	notified := make(chan string, 4)
	srv.SetNotify(func(text string) { notified <- text })
	if _, err := st.IgnoreAdd("node-01", "qwen", "", clk.Now()); err != nil {
		t.Fatal(err)
	}
	clk.Advance(10 * time.Second)
	srv.ApplyNodeMsg("node-01", extMsg(proto.ExternalProc{
		PID: 800, UID: 1, StartSec: 10, Exe: "qwen", Source: "cron",
		OpenAIBaseURL: "http://127.0.0.1:9000/v1",
	}))
	list, _ := st.ExternalList()
	if len(list) != 1 || list[0].Status != model.ExtIgnored {
		t.Fatalf("статус=%+v, хочу IGNORED", list)
	}
	select {
	case <-notified:
		t.Fatal("игнорируемый кандидат прислал WARN")
	default:
	}
}

// TestExternalAPI — list / ignore / ignore-delete / kill (узел недоступен).
func TestExternalAPI(t *testing.T) {
	st, srv, clk := extTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// Внешний кодер в БД (source=cron, target=server:srv-01).
	clk.Advance(10 * time.Second)
	srv.ApplyNodeMsg("node-01", extMsg(proto.ExternalProc{
		PID: 800, UID: 1000, StartSec: 10, Exe: "qwen", Source: "cron",
		OpenAIBaseURL: "http://127.0.0.1:9000/v1",
	}))

	// List: кодер на месте.
	status, out := jobReq(t, ts, "", http.MethodGet, "/api/v1/externals", nil)
	if status != http.StatusOK {
		t.Fatalf("list: %d %v", status, out)
	}
	exts, _ := out["externals"].([]any)
	if len(exts) != 1 {
		t.Fatalf("externals: %d, хочу 1", len(exts))
	}
	e0, _ := exts[0].(map[string]any)
	if e0["target"] != model.ExtTargetServer+"srv-01" || e0["source"] != "cron" {
		t.Fatalf("внешний: %v", e0)
	}

	// Ignore: добавляем правило, получаем id.
	status, out = jobReq(t, ts, "", http.MethodPost, "/api/v1/externals/ignore",
		map[string]any{"host": "node-01", "exe": "qwen"})
	if status != http.StatusOK {
		t.Fatalf("ignore: %d %v", status, out)
	}
	id, _ := out["id"].(float64)
	if id < 1 {
		t.Fatalf("ignore id=%v", out["id"])
	}
	// Ignore: без exe → 400.
	status, _ = jobReq(t, ts, "", http.MethodPost, "/api/v1/externals/ignore",
		map[string]any{"host": "h"})
	if status != http.StatusBadRequest {
		t.Fatalf("ignore без exe: %d, хочу 400", status)
	}
	// Ignore-delete.
	status, _ = jobReq(t, ts, "", http.MethodDelete, fmt.Sprintf("/api/v1/externals/ignore/%d", int(id)), nil)
	if status != http.StatusOK {
		t.Fatalf("ignore-delete: %d", status)
	}
	if rules, _ := st.IgnoreList(); len(rules) != 0 {
		t.Fatalf("правила не удалены: %d", len(rules))
	}

	// Kill: узел не подключён → 502 NODE_UNAVAILABLE.
	status, out = jobReq(t, ts, "", http.MethodPost, "/api/v1/externals/kill",
		map[string]any{"host": "node-01", "pid": 800, "start_time": 0})
	if status != http.StatusBadGateway || out["code"] != "NODE_UNAVAILABLE" {
		t.Fatalf("kill без узла: %d %v, хочу 502 NODE_UNAVAILABLE", status, out)
	}
}
