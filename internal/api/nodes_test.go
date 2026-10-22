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

// TestNodeEnrollFileSetupGPU — W13 этап 5: --gpu-server в install.sh записывает
// node.gpu + node.gpu_server в секцию node (GPU_SERVER задан); без него строк
// gpu в конфиге нет.
func TestNodeEnrollFileSetupGPU(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash не найден")
	}
	run := func(t *testing.T, gpu, gpuServer string) string {
		dir := t.TempDir()
		script := nodeEnrollFileSetup(dir, "https://3p14-aaa:8788", "tok123")
		cmd := exec.Command("bash", "-c",
			"set -euo pipefail\numask 0002\nGPU="+gpu+"\nGPU_SERVER="+gpuServer+"\n"+script)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("file-setup shell не завершился: %v\n%s", err, out)
		}
		cfg, err := os.ReadFile(filepath.Join(dir, "config.yaml"))
		if err != nil {
			t.Fatalf("read config.yaml: %v", err)
		}
		return string(cfg)
	}
	t.Run("with_gpu_server", func(t *testing.T) {
		cfg := run(t, "nvidia", "vzu5")
		for _, want := range []string{"gpu: nvidia", "gpu_server: vzu5"} {
			if !strings.Contains(cfg, want) {
				t.Errorf("config.yaml не содержит %q:\n%s", want, cfg)
			}
		}
	})
	t.Run("without_gpu_server", func(t *testing.T) {
		cfg := run(t, "", "")
		if strings.Contains(cfg, "gpu:") || strings.Contains(cfg, "gpu_server:") {
			t.Errorf("config.yaml содержит gpu-строки без --gpu-server:\n%s", cfg)
		}
	})
}

// TestNodeEnrollFileSetupPreservesConfig — повторный auto_install не затирает
// существующий config.yaml: project_roots_default оператора переживает
// повторную установку (баг: сброс /srv/botboss при ре-автоустановке).
func TestNodeEnrollFileSetupPreservesConfig(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash не найден")
	}
	dir := t.TempDir()
	// Первый auto_install: создаёт config.yaml.
	script1 := nodeEnrollFileSetup(dir, "https://3p14-aaa:8788", "tok123")
	cmd1 := exec.Command("bash", "-c", "set -euo pipefail\n"+script1)
	if out, err := cmd1.CombinedOutput(); err != nil {
		t.Fatalf("первый install: %v\n%s", err, out)
	}
	// Оператор добавляет project_roots_default (секция node).
	cfgPath := filepath.Join(dir, "config.yaml")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath,
		[]byte(string(data)+"  project_roots_default:\n    - /srv/botboss\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Второй auto_install (повтор) — не должен затереть config.yaml.
	script2 := nodeEnrollFileSetup(dir, "https://3p14-aaa:8788", "tok456")
	cmd2 := exec.Command("bash", "-c", "set -euo pipefail\n"+script2)
	if out, err := cmd2.CombinedOutput(); err != nil {
		t.Fatalf("второй install: %v\n%s", err, out)
	}
	cfg, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"project_roots_default:", "/srv/botboss"} {
		if !strings.Contains(string(cfg), want) {
			t.Errorf("config.yaml не содержит %q после повторного install:\n%s", want, cfg)
		}
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
	// dist-бинарник отчитывается версией координатора (две строки: версия +
	// коммит, как git-сборка) — сверка идёт по первой строке.
	okBin := filepath.Join(distDir, "runpilot")
	if err := os.WriteFile(okBin, []byte("#!/bin/sh\necho 77e9550\necho abc1234\n"), 0o755); err != nil {
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

// TestParseTailnetHost — резолвинг tailnet-hostname в IPv4 tailscale-IP из JSON
// `tailscale status --json` (MagicDNS выключен / хост не в /etc/hosts → ssh не
// резолвит имя; координатор подставляет IP).
func TestParseTailnetHost(t *testing.T) {
	const statusJSON = `{
		"Peer": {
			"nodekey:aaa": {"HostName": "vzu5-big", "TailscaleIPs": ["100.126.222.23", "fd7a::1"]},
			"nodekey:bbb": {"HostName": "vzu5-claw", "TailscaleIPs": ["100.95.195.40"]}
		}
	}`
	t.Run("found", func(t *testing.T) {
		ip, ok := parseTailnetHost([]byte(statusJSON), "vzu5-big")
		if !ok || ip != "100.126.222.23" {
			t.Fatalf("получил %q (ok=%v), хочу 100.126.222.23", ip, ok)
		}
	})
	t.Run("not_a_peer", func(t *testing.T) {
		if ip, ok := parseTailnetHost([]byte(statusJSON), "no-such-host"); ok {
			t.Fatalf("не ожидал success, получил %q", ip)
		}
	})
	t.Run("invalid_json", func(t *testing.T) {
		if ip, ok := parseTailnetHost([]byte("not-json"), "vzu5-big"); ok {
			t.Fatalf("не ожидал success, получил %q", ip)
		}
	})
}
