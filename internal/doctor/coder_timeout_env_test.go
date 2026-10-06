package doctor

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	"runpilot/internal/config"
)

// qwenCmdlineRe — cmdline_regex профиля по умолчанию (см. profiles/qwen.yaml).
var qwenCmdlineRe = regexp.MustCompile(`(^|/)qwen(\s|$)`)

func coderTimeoutEnvCtx() *Context {
	cfg := &config.Config{}
	cfg.Node.TmuxSockets = []string{"default"}
	cfg.Coordinator.GatewayURL = "http://127.0.0.1:8787"
	return &Context{Cfg: cfg, CfgPath: "/tmp/cfg.yaml", Cmdline: qwenCmdlineRe}
}

// installFakePanes — подменяет tmuxListPanes + psOutput + readProcEnviron,
// возвращает функцию отката.
func installFakePanes(panes, ps string, envFor func(pid string) ([]byte, error)) func() {
	oldTmux, oldPs, oldRead := tmuxListPanes, psOutput, readProcEnviron
	tmuxListPanes = func(string) ([]byte, error) { return []byte(panes), nil }
	psOutput = func() ([]byte, error) { return []byte(ps), nil }
	readProcEnviron = envFor
	return func() { tmuxListPanes, psOutput, readProcEnviron = oldTmux, oldPs, oldRead }
}

// Все управляемые сессии с переменной (кодер = pane_pid, совпадение на корне) -> PASS.
func TestCoderTimeoutEnvAllPresent(t *testing.T) {
	defer installFakePanes("0\t123\tphonebroker\tSID123\n0\t456\tother\tSID456",
		"123 1 qwen\n456 1 qwen\n",
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
		"123 1 qwen\n456 1 qwen\n",
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

// Ключевой сценарий п.2: переменная есть у ОБВЁРТКИ (pane_pid=bash), но НЕТ у
// процесса КОДЕРА (потомок, совпадающий с cmdline_regex) -> WARN. Читаем кодера.
func TestCoderTimeoutEnvCoderChildLacks(t *testing.T) {
	defer installFakePanes("0\t100\tphonebroker\tSID100",
		"100 1 bash\n101 100 qwen\n",
		func(pid string) ([]byte, error) {
			if pid == "100" {
				return []byte("QWEN_CODE_API_TIMEOUT_MS=5400000\x00"), nil // у обёртки ЕСТЬ
			}
			return []byte("PATH=/usr/bin\x00"), nil // у кодера (101) НЕТ
		})()
	r := checkCoderTimeoutEnv(coderTimeoutEnvCtx())
	if r.Level != WARN {
		t.Fatalf("уровень=%s, хочу WARN (у кодера нет переменной); detail=%s", r.Level, r.Detail)
	}
	if !strings.Contains(r.Detail, "SID100") {
		t.Errorf("WARN должен содержать sid: %s", r.Detail)
	}
}

// Обратный случай п.2: переменная НЕТ у обёртки, но ЕСТЬ у кодера (потомка) ->
// PASS. Доказывает, что читается именно кодер, а не pane_pid.
func TestCoderTimeoutEnvCoderChildHas(t *testing.T) {
	defer installFakePanes("0\t100\tphonebroker\tSID100",
		"100 1 bash\n101 100 qwen\n",
		func(pid string) ([]byte, error) {
			if pid == "100" {
				return []byte("PATH=/usr/bin\x00"), nil // у обёртки НЕТ
			}
			return []byte("QWEN_CODE_API_TIMEOUT_MS=5400000\x00"), nil // у кодера (101) ЕСТЬ
		})()
	r := checkCoderTimeoutEnv(coderTimeoutEnvCtx())
	if r.Level != PASS {
		t.Fatalf("уровень=%s, хочу PASS (у кодера есть переменная); detail=%s", r.Level, r.Detail)
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
		"123 1 qwen\n",
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
