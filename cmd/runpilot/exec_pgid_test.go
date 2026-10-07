package main

// setExecPGID 500: кодер мёртв, сессия завершена (finish exit_code=1),
// слот свободен. Кодер — реальный `sleep 60` с Setpgid; фэйковый
// координатор отклоняет exec_pgid (500).

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"runpilot/internal/config"
	"runpilot/internal/node"
)

func TestRunExecSetExecPGID500(t *testing.T) {
	const sid = "t-pgid"
	var finishCode int
	finishCalled := make(chan struct{})
	pgidSeen := 0
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/jobs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"sid": sid})
	})
	mux.HandleFunc("GET /api/v1/jobs/"+sid+"/wait", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"sid": sid, "env": map[string]string{}})
	})
	mux.HandleFunc("POST /api/v1/jobs/"+sid+"/exec_pgid", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			PGID int `json:"pgid"`
		}
		_ = json.NewDecoder(r.Body).Decode(&b)
		pgidSeen = b.PGID
		w.WriteHeader(http.StatusInternalServerError)
	})
	mux.HandleFunc("POST /api/v1/jobs/"+sid+"/finish", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			ExitCode int `json:"exit_code"`
		}
		_ = json.NewDecoder(r.Body).Decode(&b)
		finishCode = b.ExitCode
		close(finishCalled)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	// Профиль с command: sleep (кодер = `sleep 60`).
	home := t.TempDir()
	profDir := filepath.Join(home, ".config", "runpilot", "profiles")
	if err := os.MkdirAll(profDir, 0o755); err != nil {
		t.Fatal(err)
	}
	qwenYAML := "name: qwen\ncommand: sleep\nclassify_lines: 1\ntab_width: 8\n"
	if err := os.WriteFile(filepath.Join(profDir, "qwen.yaml"), []byte(qwenYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("RUNPILOT_TOKEN", "tok")

	cfg = config.Defaults()
	cfg.Client.Coordinator = srv.URL

	code, err := runExec(execOpts{args: []string{"60"}})
	if code != 1 {
		t.Fatalf("code=%d err=%v, хочу 1", code, err)
	}
	select {
	case <-finishCalled:
	case <-time.After(2 * time.Second):
		t.Fatal("finish не вызван (слот не свободен)")
	}
	if finishCode != 1 {
		t.Fatalf("finish exit_code=%d, хочу 1", finishCode)
	}
	// Кодер (sleep 60) мёртв: zombie (Z) → ProcStartTime = 0.
	if pgidSeen <= 0 {
		t.Fatal("exec_pgid не вызван (pgid не передан)")
	}
	deadline := time.Now().Add(3 * time.Second)
	for node.ProcStartTime(pgidSeen) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("кодер жив после setExecPGID 500, хочу группу убитой")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
