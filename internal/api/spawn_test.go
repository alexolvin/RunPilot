package api

import (
	"testing"

	"runpilot/internal/config"
	"runpilot/profiles"
)

// TestSpawnEnvClientTimeout — spawn/adopt (управляемые сессии узла) ставят
// QWEN_CODE_API_TIMEOUT_MS = client_timeout_sec×1000 из координатора, как
// runpilot run (coderEnv). До фикса spawnEnv эту переменную НЕ ставил —
// управляемая сессия (напр. phonebroker на vzu5-claw) не переживала silent-hold.
func TestSpawnEnvClientTimeout(t *testing.T) {
	cfg := &config.Config{}
	cfg.Profiles.Qwen.ClientTimeoutSec = 5400

	// 1) без профиля (s.prof == nil): таймаут — значение координатора, ставится.
	s := &Server{cfg: cfg}
	env, err := s.spawnEnv("TESTSID")
	if err != nil {
		t.Fatalf("spawnEnv (без профиля): %v", err)
	}
	if got := env["QWEN_CODE_API_TIMEOUT_MS"]; got != "5400000" {
		t.Fatalf("без профиля: QWEN_CODE_API_TIMEOUT_MS=%q, хочу 5400000", got)
	}

	// 2) с профилем: таймаут ПЕРЕКРЫВАЕТ операторский override в env профиля,
	//    шаблонные переменные подставляются.
	s.prof = &profiles.Qwen{Env: map[string]string{
		"QWEN_CODE_API_TIMEOUT_MS": "1",   // операторский override — должен быть перекрыт
		"MY_TEST_VAR":              "{{.SID}}",
	}}
	env, err = s.spawnEnv("TESTSID")
	if err != nil {
		t.Fatalf("spawnEnv (с профилем): %v", err)
	}
	if got := env["QWEN_CODE_API_TIMEOUT_MS"]; got != "5400000" {
		t.Errorf("с профилем: QWEN_CODE_API_TIMEOUT_MS=%q, хочу 5400000 (перекрывает override=1)", got)
	}
	if env["MY_TEST_VAR"] != "TESTSID" {
		t.Errorf("шаблон env не подставлен: MY_TEST_VAR=%q, хочу TESTSID", env["MY_TEST_VAR"])
	}
}
