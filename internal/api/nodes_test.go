package api

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
	script := nodeEnrollFileSetup(dir, "https://3p14-aaa:8788", "node-host", "tok123")
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
	for _, want := range []string{"coordinator: https://3p14-aaa:8788", "host: node-host"} {
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
