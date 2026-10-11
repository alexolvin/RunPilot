package main

// buildVersionInfo — версия из VERSION файла (embed, формат YYYY.N.R) +
// vcs.revision (коммит). Тест собирает реальный бинарник (go build ставит
// VCS-stamping) и сверяет вывод `runpilot version`.

import (
	"debug/buildinfo"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildVersionBinary(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "runpilot")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}

	// Version — из VERSION файла (embed в бинарник при сборке).
	wantVersion := "dev"
	if data, err := os.ReadFile("VERSION"); err == nil {
		if v := strings.TrimSpace(string(data)); v != "" {
			wantVersion = v
		}
	}

	// Commit — vcs.revision.
	bi, err := buildinfo.ReadFile(bin)
	if err != nil {
		t.Fatalf("buildinfo.ReadFile: %v", err)
	}
	var rev string
	for _, s := range bi.Settings {
		if s.Key == "vcs.revision" {
			rev = s.Value
		}
	}

	out, err := exec.Command(bin, "version").Output()
	if err != nil {
		t.Fatalf("runpilot version: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 1 {
		t.Fatalf("runpilot version: пустой вывод")
	}
	if lines[0] != wantVersion {
		t.Fatalf("runpilot version=%q, хочу %q", lines[0], wantVersion)
	}
	if len(lines) >= 2 && rev != "" {
		if lines[1] != rev {
			t.Fatalf("runpilot version commit=%q, хочу %q", lines[1], rev)
		}
	}
}
