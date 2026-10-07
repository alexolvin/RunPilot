package main

import "testing"

// buildQwenCmd — промпт передаётся КОДЕРУ позиционным аргументом (qwen:
// «Defaults to one-shot»), stdin = /dev/null. runpilot exec -- "Сделай X" →
// qwen "Сделай X" (задача T1: exec обязан передать промпт).
func TestBuildQwenCmdPrompt(t *testing.T) {
	cmd, err := buildQwenCmd("qwen", []string{"Сделай X"}, map[string]string{
		"OPENAI_BASE_URL": "http://127.0.0.1:8787/s/SID/v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cmd.Args) != 2 || cmd.Args[0] != "qwen" || cmd.Args[1] != "Сделай X" {
		t.Fatalf("Args=%v, хочу [qwen Сделай X]", cmd.Args)
	}
	envFound := false
	for _, kv := range cmd.Env {
		if kv == "OPENAI_BASE_URL=http://127.0.0.1:8787/s/SID/v1" {
			envFound = true
		}
	}
	if !envFound {
		t.Fatalf("ENV шлюза не проброшен в кодер: %v", cmd.Env)
	}
}

// пустые аргументы (без промпта) — кодер стартует интерактивно (не наша задача,
// но не должно ломать Args).
func TestBuildQwenCmdNoArgs(t *testing.T) {
	cmd, err := buildQwenCmd("qwen", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(cmd.Args) != 1 || cmd.Args[0] != "qwen" {
		t.Fatalf("Args=%v, хочу [qwen]", cmd.Args)
	}
}
