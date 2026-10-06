package main

// A.4: QWEN_CODE_API_TIMEOUT_MS — источник только координатор (client_timeout_ms),
// выставляется после env профиля и перекрывает override-профиль (с переменной)
// и окружение оператора.

import (
	"testing"

	"runpilot/internal/config"
	"runpilot/profiles"
)

func TestCoderEnvClientTimeoutOverride(t *testing.T) {
	cfg = config.Defaults()

	// (1) override-профиль БЕЗ переменной → значение из координатора (5400000).
	prof := &profiles.Qwen{Env: map[string]string{"OPENAI_MODEL": "runpilot"}}
	env, err := coderEnv(prof, "SID123", 5400000)
	if err != nil {
		t.Fatal(err)
	}
	if env["QWEN_CODE_API_TIMEOUT_MS"] != "5400000" {
		t.Fatalf("QWEN_CODE_API_TIMEOUT_MS = %q, хочу 5400000 (из координатора)", env["QWEN_CODE_API_TIMEOUT_MS"])
	}

	// (2) override-профиль С переменной (=1, имитация оператора) → всё равно из координатора.
	prof2 := &profiles.Qwen{Env: map[string]string{"QWEN_CODE_API_TIMEOUT_MS": "1"}}
	env2, err := coderEnv(prof2, "SID123", 5400000)
	if err != nil {
		t.Fatal(err)
	}
	if env2["QWEN_CODE_API_TIMEOUT_MS"] != "5400000" {
		t.Fatalf("перекрывание: %q, хочу 5400000 (из координатора, не 1)", env2["QWEN_CODE_API_TIMEOUT_MS"])
	}
}
