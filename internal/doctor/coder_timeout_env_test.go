package doctor

import (
	"errors"
	"strings"
	"testing"

	"runpilot/internal/config"
)

func coderTimeoutEnvCtx() *Context {
	cfg := &config.Config{}
	cfg.Node.TmuxSockets = []string{"default"}
	return &Context{Cfg: cfg, CfgPath: "/tmp/cfg.yaml"}
}

// installFakePanes — подменяет tmuxListPanes + readProcEnviron, возвращает
// функцию отката.
func installFakePanes(panes string, envFor func(pid string) ([]byte, error)) func() {
	oldTmux, oldRead := tmuxListPanes, readProcEnviron
	tmuxListPanes = func(string) ([]byte, error) { return []byte(panes), nil }
	readProcEnviron = envFor
	return func() { tmuxListPanes, readProcEnviron = oldTmux, oldRead }
}

// Все управляемые сессии с переменной -> PASS.
func TestCoderTimeoutEnvAllPresent(t *testing.T) {
	defer installFakePanes("0\t123\tphonebroker\tSID123\n0\t456\tother\tSID456",
		func(string) ([]byte, error) {
			return []byte("PATH=/usr/bin\x00QWEN_CODE_API_TIMEOUT_MS=5400000\x00"), nil
		})()
	r := checkCoderTimeoutEnv(coderTimeoutEnvCtx())
	if r.Level != PASS {
		t.Fatalf("уровень=%s, хочу PASS; detail=%s", r.Level, r.Detail)
	}
}

// Управляемая сессия без переменной -> WARN с sid и именем; сессия с
// переменной в WARN не фигурирует.
func TestCoderTimeoutEnvWarnMissing(t *testing.T) {
	defer installFakePanes("0\t123\tphonebroker\tSID123\n0\t456\tother\tSID456",
		func(pid string) ([]byte, error) {
			if pid == "123" {
				return []byte("PATH=/usr/bin\x00"), nil // без переменной
			}
			return []byte("QWEN_CODE_API_TIMEOUT_MS=5400000\x00"), nil
		})()
	r := checkCoderTimeoutEnv(coderTimeoutEnvCtx())
	if r.Level != WARN {
		t.Fatalf("уровень=%s, хочу WARN (не FAIL, чтобы не блокировать откат); detail=%s", r.Level, r.Detail)
	}
	if !strings.Contains(r.Detail, "SID123") || !strings.Contains(r.Detail, "phonebroker") {
		t.Errorf("WARN должен содержать sid и имя: %s", r.Detail)
	}
	if strings.Contains(r.Detail, "SID456") {
		t.Errorf("SID456 (с переменной) не должен быть в WARN: %s", r.Detail)
	}
}

// Нет tmux-сервера (координатор без узлов) -> PASS «нет живых управляемых
// сессий», а не FAIL.
func TestCoderTimeoutEnvNoServer(t *testing.T) {
	oldTmux := tmuxListPanes
	tmuxListPanes = func(string) ([]byte, error) { return nil, errors.New("no server running") }
	defer func() { tmuxListPanes = oldTmux }()
	r := checkCoderTimeoutEnv(coderTimeoutEnvCtx())
	if r.Level != PASS {
		t.Fatalf("уровень=%s, хочу PASS (нет сервера); detail=%s", r.Level, r.Detail)
	}
}

// Нечитаемый /proc/<pid>/environ (процесс исчез) -> WARN, не падение.
func TestCoderTimeoutEnvEnvironUnreadable(t *testing.T) {
	defer installFakePanes("0\t123\tphonebroker\tSID123",
		func(string) ([]byte, error) { return nil, errors.New("no such process") })()
	r := checkCoderTimeoutEnv(coderTimeoutEnvCtx())
	if r.Level != WARN {
		t.Fatalf("уровень=%s, хочу WARN; detail=%s", r.Level, r.Detail)
	}
}

// procEnvHas — NUL-разделение.
func TestProcEnvHas(t *testing.T) {
	env := []byte("A=1\x00QWEN_CODE_API_TIMEOUT_MS=5400000\x00B=2")
	if !procEnvHas(env, "QWEN_CODE_API_TIMEOUT_MS") {
		t.Error("не найдена на месте")
	}
	if !procEnvHas(env, "A") {
		t.Error("не найдена A")
	}
	if procEnvHas(env, "QWEN_CODE_API_TIMEOUT_MSX") {
		t.Error("ложное совпадение по префиксу (X)")
	}
	if procEnvHas(env, "QWEN_CODE_API_TIMEOUT") {
		t.Error("ложное совпадение (без _MS)")
	}
}
