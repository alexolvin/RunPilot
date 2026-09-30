package gateway

// v2 (S11): key_env задан, но переменная пуста в окружении службы →
// сервер KEY_MISSING (не годится для аренд); ключ задан → UP.

import (
	"testing"
	"time"

	"runpilot/internal/config"
	"runpilot/internal/model"
)

func TestKeyMissingState(t *testing.T) {
	t.Setenv("RUNPILOT_S11_SET", "k")
	cfg := []config.Server{
		srvCfg("nokey", 10, 1, "http://127.0.0.1:1", "RUNPILOT_S11_UNSET"), // не задан
		srvCfg("haskey", 9, 1, "http://127.0.0.1:1", "RUNPILOT_S11_SET"),     // задан
	}
	srvs, err := NewServers(cfg, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if st := srvs.StateOf("nokey"); st != model.ServerKeyMissing {
		t.Fatalf("nokey: state=%s, хочу KEY_MISSING", st)
	}
	if srvs.StateOf("haskey") != model.ServerUp {
		t.Fatalf("haskey: state=%s, хочу UP", srvs.StateOf("haskey"))
	}
}
