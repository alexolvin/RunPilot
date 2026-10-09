package doctor

// node_version (W12, ТЗ 14: «Автообновления aqm нет»): версия узла == версия
// координатора. Несовпадение → WARN (не FAIL): узел работает, но на старой
// версии — оператор обновляет вручную.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"runpilot/internal/config"
)

// versionServer — минимальный сервер /api/v1/node/version: отдаёт версию.
func versionServer(t *testing.T, version string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/node/version", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version":"` + version + `"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestNodeVersionMatch(t *testing.T) {
	srv := versionServer(t, "77e9550")
	cfg := config.Defaults()
	cfg.Client.Coordinator = srv.URL
	cfg.Client.TokenEnv = "RUNPILOT_NODE_TOKEN"
	t.Setenv("RUNPILOT_NODE_TOKEN", "node-tok")
	if r := checkNodeVersion(&Context{Cfg: cfg, NodeVersion: "77e9550"}); r.Level != PASS {
		t.Fatalf("версии совпадают: %s", r)
	}
}

func TestNodeVersionMismatch(t *testing.T) {
	srv := versionServer(t, "77e9550")
	cfg := config.Defaults()
	cfg.Client.Coordinator = srv.URL
	cfg.Client.TokenEnv = "RUNPILOT_NODE_TOKEN"
	t.Setenv("RUNPILOT_NODE_TOKEN", "node-tok")
	r := checkNodeVersion(&Context{Cfg: cfg, NodeVersion: "d96bf26"})
	if r.Level != WARN || !strings.Contains(r.Detail, "автообновления нет") {
		t.Fatalf("mismatch: %s", r)
	}
}
