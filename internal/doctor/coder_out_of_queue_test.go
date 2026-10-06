package doctor

import (
	"strings"
	"testing"
)

// installFakeOOQ — фальсифицирует psOutput + readProcEnviron + procCwd
// (для checkCoderOutOfQueue), возвращает функцию отката.
func installFakeOOQ(ps string, envFor func(pid string) ([]byte, error), cwdFor func(pid string) string) func() {
	oldPs, oldRead, oldCwd := psOutput, readProcEnviron, procCwd
	psOutput = func() ([]byte, error) { return []byte(ps), nil }
	readProcEnviron = envFor
	procCwd = cwdFor
	return func() { psOutput, readProcEnviron, procCwd = oldPs, oldRead, oldCwd }
}

// qwen-процесс БЕЗ OPENAI_BASE_URL (ручной запуск) -> WARN «кодер вне очереди»
// с PID и cwd.
func TestCoderOutOfQueueWarn(t *testing.T) {
	defer installFakeOOQ(
		"1 0 init\n101 1 qwen --approval-mode default\n",
		func(string) ([]byte, error) { return []byte("PATH=/usr/bin\x00"), nil }, // без OPENAI_BASE_URL
		func(string) string { return "/home/user/project" },
	)()
	r := checkCoderOutOfQueue(coderTimeoutEnvCtx())
	if r.Level != WARN {
		t.Fatalf("уровень=%s, хочу WARN; detail=%s", r.Level, r.Detail)
	}
	if !strings.Contains(r.Detail, "101") || !strings.Contains(r.Detail, "cwd=/home/user/project") {
		t.Errorf("WARN должен содержать PID и cwd: %s", r.Detail)
	}
}

// qwen-процесс С OPENAI_BASE_URL (идёт через шлюз — управляемая сессия) ->
// не «вне очереди» -> PASS.
func TestCoderOutOfQueueManagedPass(t *testing.T) {
	defer installFakeOOQ(
		"1 0 init\n101 1 qwen\n",
		func(string) ([]byte, error) { return []byte("OPENAI_BASE_URL=http://gw/s/SID/v1\x00"), nil },
		func(string) string { return "/home/user/project" },
	)()
	r := checkCoderOutOfQueue(coderTimeoutEnvCtx())
	if r.Level != PASS {
		t.Fatalf("уровень=%s, хочу PASS (идёт через шлюз); detail=%s", r.Level, r.Detail)
	}
}

// Нет qwen-процессов -> PASS «кодеров вне очереди нет».
func TestCoderOutOfQueueNone(t *testing.T) {
	defer installFakeOOQ(
		"1 0 init\n2 1 runpilot doctor\n",
		func(string) ([]byte, error) { return []byte("PATH=/usr/bin\x00"), nil },
		func(string) string { return "/" },
	)()
	r := checkCoderOutOfQueue(coderTimeoutEnvCtx())
	if r.Level != PASS {
		t.Fatalf("уровень=%s, хочу PASS (нет кодеров); detail=%s", r.Level, r.Detail)
	}
}
