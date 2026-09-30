package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"runpilot/internal/clock"
	"runpilot/internal/config"
	"runpilot/internal/proto"
	"runpilot/internal/store"
)

// W1 CONTROL 6: встроенный узел — узел, панели и сессии ХОСТА КООРДИНАТОРА
// видны в /api/v1/state без службы runpilot-node (v2 раздел 2.5, 15).
//
// Встроенный узел (cmd/runpilot/embedded.go) использует тот же путь данных, что и
// WS-узел: hub.Register + Server.NodeSeen + Server.ApplyNodeMsg. Тест гоняет
// ровно этот путь (без tmux и без WebSocket) и проверяет, что узел хоста, его
// панель и сессия хоста видны в срезе — то есть отдельная служба runpilot-node на
// хосте координатора не нужна.

// newEmbeddedHarness — координатор + ссылки на hub и server (путь данных узла).
func newEmbeddedHarness(t *testing.T) (*httptest.Server, *Hub, *Server) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "runpilot.db"))
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
	return ts, hub, srv
}

func TestEmbeddedNodeHostVisibleInState(t *testing.T) {
	ts, hub, srv := newEmbeddedHarness(t)
	host := "coord-host"

	// 1) Сессия хоста (runpilot run) — возвращает sid.
	status, m := doJSON(t, "POST", ts.URL+"/api/v1/run",
		map[string]any{"name": "hostjob", "host": host, "host_ip": "127.0.0.1", "profile": "qwen"})
	if status != http.StatusCreated {
		t.Fatalf("run: %d %v", status, m)
	}
	sid := m["sid"]
	if sid == "" {
		t.Fatalf("run без sid: %v", m)
	}

	// 2) Узел хоста: регистрация (как делает встроенный узел) + NodeSeen.
	hub.Register(&NodeInfo{Host: host, HostIP: "127.0.0.1", RUNPILOTVersion: "v", Conn: nil})
	srv.NodeSeen(host)

	// 3) Панель хоста: снимок через ApplyNodeMsg (отправляющий путь узла).
	srv.ApplyNodeMsg(host, proto.PaneOf(proto.Pane{PaneID: "%0", SID: sid, State: "IDLE", Hash: 1}))

	// /state: узел, панель и сессия хоста видны без службы runpilot-node.
	resp, err := http.Get(ts.URL + "/api/v1/state")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var sv StateView
	if err := json.Unmarshal(raw, &sv); err != nil {
		t.Fatalf("state: %v (%s)", err, raw)
	}

	var hostNode, hostPane, hostSession bool
	for _, n := range sv.Nodes {
		if n.Host == host {
			hostNode = true
		}
	}
	for _, p := range sv.Panes {
		if p.Pane.PaneID == "%0" && p.Host == host {
			hostPane = true
		}
	}
	for _, s := range sv.Sessions {
		if s.SID == sid && s.Host == host {
			hostSession = true
		}
	}
	if !hostNode {
		t.Fatalf("узел хоста %s не в state.nodes: %+v", host, sv.Nodes)
	}
	if !hostPane {
		t.Fatalf("панель хоста не в state.panes: %+v", sv.Panes)
	}
	if !hostSession {
		t.Fatalf("сессия хоста %s не в state.sessions: %+v", sid, sv.Sessions)
	}
}
