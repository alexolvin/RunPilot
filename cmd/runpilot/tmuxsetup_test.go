package main

import (
	"strings"
	"testing"
)

func TestReplaceBlockInsertsWhenAbsent(t *testing.T) {
	out := replaceBlock("set -sg status-left \"old\"\n", tmuxBlock)
	if !strings.Contains(out, tmuxBlockStart) || !strings.Contains(out, tmuxBlockEnd) {
		t.Fatalf("блок не вставлен: %q", out)
	}
	if !strings.Contains(out, "set -sg status-left") {
		t.Fatalf("старый конфиг потерялся: %q", out)
	}
}

func TestReplaceBlockReplacesBetweenMarkers(t *testing.T) {
	old := "set -sg status-left \"old\"\n" + tmuxBlockStart + "\nустаревшие строки\n" + tmuxBlockEnd + "\nset -g other 1\n"
	out := replaceBlock(old, tmuxBlock)
	if strings.Contains(out, "устаревшие строки") {
		t.Fatalf("старый блок не заменён: %q", out)
	}
	if !strings.Contains(out, "set -g other 1") || !strings.Contains(out, "set -sg status-left") {
		t.Fatalf("окружающий конфиг повреждён: %q", out)
	}
	n := strings.Count(out, tmuxBlockStart)
	if n != 1 {
		t.Fatalf("маркеров start: %d, хочу 1", n)
	}
}

func TestReplaceBlockIdempotent(t *testing.T) {
	once := replaceBlock("", tmuxBlock)
	twice := replaceBlock(once, tmuxBlock)
	if once != twice {
		t.Fatalf("повторный прогон меняет конфиг:\n%q\nvs\n%q", once, twice)
	}
}
