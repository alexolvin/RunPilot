package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"runpilot/internal/clock"
	"runpilot/internal/config"
	"runpilot/internal/store"
)

// TestNodeEnrollFileSetupUmask — создание config.yaml + secrets.env (install.sh
// узла) даёт режим 0600 НЕЗАВИСИМО от umask (требование 14: doctor file_mode).
// umask 0002 ставится ВНУТРИ дочернего bash (не трогая umask процесса тестов):
// без `chmod 600` обычный `cat >`/`echo >` дал бы 0664, с — 0600.
func TestNodeEnrollFileSetupUmask(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash не найден")
	}
	dir := t.TempDir()
	script := nodeEnrollFileSetup(dir, "https://3p14-aaa:8788", "tok123")
	cmd := exec.Command("bash", "-c", "set -euo pipefail\numask 0002\n"+script)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("file-setup shell не завершился: %v\n%s", err, out)
	}
	for _, name := range []string{"config.yaml", "secrets.env"} {
		p := filepath.Join(dir, name)
		st, err := os.Stat(p)
		if err != nil {
			t.Fatalf("нет %s: %v", name, err)
		}
		if got := st.Mode().Perm(); got != 0o600 {
			t.Errorf("%s: mode=%o, хочу 0600 (umask 0002 должен быть преодолён)", name, got)
		}
	}
	// содержимое корректно (не только права)
	cfg, err := os.ReadFile(filepath.Join(dir, "config.yaml"))
	if err != nil {
		t.Fatalf("read config.yaml: %v", err)
	}
	// host: "" — узел определяет hostname сам (os.Hostname), не из конфига.
	for _, want := range []string{"coordinator: https://3p14-aaa:8788", "host: \"\""} {
		if !strings.Contains(string(cfg), want) {
			t.Errorf("config.yaml не содержит %q", want)
		}
	}
	sec, err := os.ReadFile(filepath.Join(dir, "secrets.env"))
	if err != nil {
		t.Fatalf("read secrets.env: %v", err)
	}
	if !strings.Contains(string(sec), "RUNPILOT_NODE_TOKEN=tok123") {
		t.Errorf("secrets.env не содержит RUNPILOT_NODE_TOKEN=tok123")
	}
}

// TestNodeUpdateStaleDistRefused — W12 (ТЗ 14): бинарник в dist_dir не
// совпадает по версии с координатором (stale dist) → API-обновление отказывает
// 409 DIST_MISMATCH (иначе self-update узлов разворачивал бы устаревший dist).
func TestNodeUpdateStaleDistRefused(t *testing.T) {
	dir := t.TempDir()
	distDir := filepath.Join(dir, "dist")
	if err := os.MkdirAll(distDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// dist-бинарник отчитывается СТАРОЙ версией (stale dist).
	staleBin := filepath.Join(distDir, "runpilot")
	if err := os.WriteFile(staleBin, []byte("#!/bin/sh\necho d96bf26\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := config.Defaults()
	cfg.Coordinator.DistDir = distDir
	st, err := store.Open(filepath.Join(dir, "runpilot.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	clk := clock.NewReal()
	srv := NewServer(cfg, st, NewHub(clk), clk,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv.SetVersion("77e9550")
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() { ts.Close() })

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/nodes/vzu5-big/update", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("статус %d, хочу 409 (DIST_MISMATCH)", resp.StatusCode)
	}
}

// TestNodeUpdateDistOK — W12: бинарник в dist_dir = версии координатора →
// API-обновление НЕ отказывает по версии (202, операция запущена).
func TestNodeUpdateDistOK(t *testing.T) {
	dir := t.TempDir()
	distDir := filepath.Join(dir, "dist")
	if err := os.MkdirAll(distDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// dist-бинарник отчитывается версией координатора.
	okBin := filepath.Join(distDir, "runpilot")
	if err := os.WriteFile(okBin, []byte("#!/bin/sh\necho 77e9550\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := config.Defaults()
	cfg.Coordinator.DistDir = distDir
	st, err := store.Open(filepath.Join(dir, "runpilot.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	clk := clock.NewReal()
	srv := NewServer(cfg, st, NewHub(clk), clk,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv.SetVersion("77e9550")
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() { ts.Close() })

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/nodes/vzu5-big/update", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("статус %d, хочу 202 (обновление запущено)", resp.StatusCode)
	}
}
