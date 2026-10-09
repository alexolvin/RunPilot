package main

import "testing"

// buildQwenCmd — промпт передаётся КОДЕРУ позиционным аргументом (qwen:
// «Defaults to one-shot»), stdin = /dev/null. runpilot exec -- "Сделай X" →
// qwen --model <alias> --openai-base-url <gateway> "Сделай X" (явные
// аргументы гарантируют, что запросы идут через шлюз, даже если
// settings.model.name совпадает с id modelProviders).
func TestBuildQwenCmdPrompt(t *testing.T) {
	cmd, err := buildQwenCmd("qwen", []string{"Сделай X"}, map[string]string{
		"OPENAI_BASE_URL": "http://127.0.0.1:8787/s/SID/v1",
		"OPENAI_MODEL":    "qwen3.8-27b",
	})
	if err != nil {
		t.Fatal(err)
	}
	// [qwen --auth-type openai --model qwen3.8-27b --openai-base-url http://127.0.0.1:8787/s/SID/v1 Сделай X]
	want := []string{"qwen", "--auth-type", "openai", "--model", "qwen3.8-27b", "--openai-base-url", "http://127.0.0.1:8787/s/SID/v1", "Сделай X"}
	if len(cmd.Args) != len(want) {
		t.Fatalf("Args=%v (len %d), хочу %v (len %d)", cmd.Args, len(cmd.Args), want, len(want))
	}
	for i, w := range want {
		if cmd.Args[i] != w {
			t.Fatalf("Args[%d]=%q, хочу %q (полный %v)", i, cmd.Args[i], w, cmd.Args)
		}
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
