package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCaptureArgs(t *testing.T) {
	if got := captureArgs("", "mock1"); got[0] != "capture-pane" {
		t.Fatalf("без сокета первый аргумент = capture-pane, got %q", got[0])
	}
	got := captureArgs("runpilottest", "mock1")
	want := []string{"-L", "runpilottest", "capture-pane", "-p", "-J", "-t", "mock1"}
	if len(got) != len(want) {
		t.Fatalf("captureArgs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("captureArgs = %v, want %v", got, want)
		}
	}
}

func TestNextFixtureSeq(t *testing.T) {
	dir := t.TempDir()
	if got := nextFixtureSeq(dir, "idle_empty"); got != 1 {
		t.Fatalf("пустой каталог: %d, want 1", got)
	}
	for _, name := range []string{"idle_empty-01.txt", "idle_empty-03.txt", "busy_tool-07.txt", "readme.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if got := nextFixtureSeq(dir, "idle_empty"); got != 4 {
		t.Fatalf("после 01 и 03: %d, want 4", got)
	}
	if got := nextFixtureSeq(dir, "busy_tool"); got != 8 {
		t.Fatalf("после 07: %d, want 8", got)
	}
}
