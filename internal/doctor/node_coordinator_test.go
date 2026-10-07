package doctor

// node_coordinator — handshake узла (WS-dial /api/v1/node с Bearer-токеном
// узла). Тесты: валидный токен → PASS; неверный → FAIL «токен отклонён»;
// токен пуст → FAIL «токен не задан: <var>»; роль node без токена клиента →
// без FAIL (node_coordinator берёт токен узла, не клиента).

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"runpilot/internal/config"

	"github.com/gorilla/websocket"
)

// wsNodeServer — минимальный WS-сервер /api/v1/node: принимает handshake,
// если Bearer-токен == want, иначе 401.
func wsNodeServer(t *testing.T, want string) *httptest.Server {
	t.Helper()
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/node", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+want {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		// Держим соединение, пока клиент не закроет (doctor закрывает сразу).
		for {
			if _, _, rerr := conn.ReadMessage(); rerr != nil {
				return
			}
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestNodeCoordinatorPass(t *testing.T) {
	srv := wsNodeServer(t, "valid-node-token")
	cfg := config.Defaults()
	cfg.Client.Coordinator = srv.URL
	cfg.Client.TokenEnv = "RUNPILOT_NODE_TOKEN"
	t.Setenv("RUNPILOT_NODE_TOKEN", "valid-node-token")
	if r := checkNodeCoordinator(&Context{Cfg: cfg}); r.Level != PASS {
		t.Fatalf("валидный токен: %s", r)
	}
}

func TestNodeCoordinatorRejected(t *testing.T) {
	srv := wsNodeServer(t, "valid-node-token")
	cfg := config.Defaults()
	cfg.Client.Coordinator = srv.URL
	cfg.Client.TokenEnv = "RUNPILOT_NODE_TOKEN"
	t.Setenv("RUNPILOT_NODE_TOKEN", "wrong-token")
	r := checkNodeCoordinator(&Context{Cfg: cfg})
	if r.Level != FAIL || !strings.Contains(r.Detail, "токен отклонён") {
		t.Fatalf("неверный токен: %s", r)
	}
}

func TestNodeCoordinatorNoToken(t *testing.T) {
	cfg := config.Defaults()
	cfg.Client.Coordinator = "http://127.0.0.1:1"
	cfg.Client.TokenEnv = "RUNPILOT_NODE_TOKEN"
	t.Setenv("RUNPILOT_NODE_TOKEN", "")
	withHome(t, t.TempDir()) // изолируем home: secrets.env/env не найдут токен
	r := checkNodeCoordinator(&Context{Cfg: cfg})
	if r.Level != FAIL || !strings.Contains(r.Detail, "токен не задан: RUNPILOT_NODE_TOKEN") {
		t.Fatalf("токен пуст: %s", r)
	}
}

// Роль node без токена клиента → без FAIL: node_coordinator берёт токен УЗЛА
// (cfg.Client.TokenEnv), не клиента; client_coordinator в роли node нет.
func TestNodeRoleNoClientToken(t *testing.T) {
	srv := wsNodeServer(t, "valid-node-token")
	cfg := config.Defaults()
	cfg.Client.Coordinator = srv.URL
	cfg.Client.TokenEnv = "RUNPILOT_NODE_TOKEN" // конфиг узла
	t.Setenv("RUNPILOT_NODE_TOKEN", "valid-node-token")
	t.Setenv("RUNPILOT_TOKEN", "") // токен клиента отсутствует

	for _, c := range RoleChecks(RoleNode) {
		if c.Name == "client_coordinator" {
			t.Fatal("роль node содержит client_coordinator — потребовал бы токен клиента")
		}
	}
	if r := checkNodeCoordinator(&Context{Cfg: cfg}); r.Level != PASS {
		t.Fatalf("node_coordinator с токеном узла: %s", r)
	}
}
