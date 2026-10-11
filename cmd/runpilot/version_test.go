package main

// buildVersionInfo — версия из debug.ReadBuildInfo().Main.Version (git-тег)
// + vcs.revision (коммит). Тест собирает реальный бинарник (go build ставит
// VCS-stamping) и сверяет вывод `runpilot version`.

import (
	"debug/buildinfo"
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

	bi, err := buildinfo.ReadFile(bin)
	if err != nil {
		t.Fatalf("buildinfo.ReadFile: %v", err)
	}
	var rev, modified string
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			modified = s.Value
		}
	}
	_ = modified // не используется в новой логике

	wantVersion := bi.Main.Version
	if wantVersion == "" || wantVersion == "(devel)" {
		wantVersion = "dev"
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
		t.Fatalf("runpilot version=%q, хочу %q (Main.Version=%q)", lines[0], wantVersion, bi.Main.Version)
	}
	if len(lines) >= 2 && rev != "" {
		if lines[1] != rev {
			t.Fatalf("runpilot version commit=%q, хочу %q", lines[1], rev)
		}
	}
}
