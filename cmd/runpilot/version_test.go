package main

// buildVersion — версия из buildinfo: короткий хеш vcs.revision, суффикс
// «-dirty» при vcs.modified=true. Тест собирает реальный бинарник (go build
// ставит VCS-stamping; тестовый бинарник go test его не получает) и сверяет
// вывод `runpilot version` с vcs-данными из buildinfo ЭТОГО бинарника.

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
	// cwd = каталог пакета (cmd/runpilot) → «go build .» строит main-пакет.
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
	if len(rev) < versionShortHashLen {
		t.Fatalf("vcs.revision=%q, хочу хеш длиннее %d символов (VCS-stamping absent)", rev, versionShortHashLen)
	}
	want := rev[:versionShortHashLen]
	if modified == "true" {
		want += "-dirty"
	}

	out, err := exec.Command(bin, "version").Output()
	if err != nil {
		t.Fatalf("runpilot version: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != want {
		t.Fatalf("runpilot version=%q, хочу %q (rev=%q modified=%q)", got, want, rev, modified)
	}
}

// ldflags-переопределение имеет приоритет над buildinfo.
func TestBuildVersionLdflagsOverride(t *testing.T) {
	old := version
	version = "1.2.3"
	defer func() { version = old }()
	if got := buildVersion(); got != "1.2.3" {
		t.Fatalf("buildVersion()=%q, хочу 1.2.3 (ldflags-приоритет)", got)
	}
}
