package doctor

import (
	"strings"
	"testing"
)

// testOOQGateway — gateway_url из coderTimeoutEnvCtx() (URL кодера сессии).
const testOOQGateway = "http://127.0.0.1:8787"

// installFakeOOQ — фальсифицирует tmuxListPanes + psOutput + readProcEnviron +
// procCwd (для checkCoderOutOfQueue), возвращает функцию отката.
// panes — вывод tmux list-panes ("" — нет управляемых панелей).
func installFakeOOQ(panes, ps string, envFor func(pid string) ([]byte, error), cwdFor func(pid string) string) func() {
	oldTmux, oldPs, oldRead, oldCwd := tmuxListPanes, psOutput, readProcEnviron, procCwd
	tmuxListPanes = func(string) ([]byte, error) { return []byte(panes), nil }
	psOutput = func() ([]byte, error) { return []byte(ps), nil }
	readProcEnviron = envFor
	procCwd = cwdFor
	return func() { tmuxListPanes, psOutput, readProcEnviron, procCwd = oldTmux, oldPs, oldRead, oldCwd }
}

// Ручной запуск qwen (нет управляемой панели, нет OPENAI_BASE_URL) -> WARN
// с PID и cwd.
func TestCoderOutOfQueueManual(t *testing.T) {
	defer installFakeOOQ(
		"",
		"1 0 init\n101 1 qwen --approval-mode default\n",
		func(string) ([]byte, error) { return []byte("PATH=/usr/bin\x00"), nil },
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

// Кодер живой сессии: потомок панели с sid, OPENAI_BASE_URL =
// gateway_url/s/<sid>/v1 -> управляемый -> PASS.
func TestCoderOutOfQueueManagedPass(t *testing.T) {
	defer installFakeOOQ(
		"0\t100\tphonebroker\tSID100",
		"1 0 init\n100 1 bash\n101 100 qwen\n",
		func(pid string) ([]byte, error) {
			if pid == "101" {
				return []byte("OPENAI_BASE_URL=" + testOOQGateway + "/s/SID100/v1\x00"), nil
			}
			return []byte("PATH=/usr/bin\x00"), nil
		},
		func(string) string { return "/home/user/project" },
	)()
	r := checkCoderOutOfQueue(coderTimeoutEnvCtx())
	if r.Level != PASS {
		t.Fatalf("уровень=%s, хочу PASS (кодер живой сессии); detail=%s", r.Level, r.Detail)
	}
}

// Кодер под панелью SID100, но OPENAI_BASE_URL шлюза с ЧУЖИМ sid -> вне
// очереди -> WARN (условие (б) правила не выполняется).
func TestCoderOutOfQueueForeignSID(t *testing.T) {
	defer installFakeOOQ(
		"0\t100\tphonebroker\tSID100",
		"1 0 init\n100 1 bash\n101 100 qwen\n",
		func(pid string) ([]byte, error) {
			if pid == "101" {
				return []byte("OPENAI_BASE_URL=" + testOOQGateway + "/s/FOREIGN/v1\x00"), nil
			}
			return []byte("PATH=/usr/bin\x00"), nil
		},
		func(string) string { return "/home/user/project" },
	)()
	r := checkCoderOutOfQueue(coderTimeoutEnvCtx())
	if r.Level != WARN {
		t.Fatalf("уровень=%s, хочу WARN (чужой sid); detail=%s", r.Level, r.Detail)
	}
	if !strings.Contains(r.Detail, "101") {
		t.Errorf("WARN должен содержать PID 101: %s", r.Detail)
	}
}

// Кодер под панелью, но OPENAI_BASE_URL прямо на vLLM -> вне очереди -> WARN.
func TestCoderOutOfQueueVLLMDirect(t *testing.T) {
	defer installFakeOOQ(
		"0\t100\tphonebroker\tSID100",
		"1 0 init\n100 1 bash\n101 100 qwen\n",
		func(pid string) ([]byte, error) {
			if pid == "101" {
				return []byte("OPENAI_BASE_URL=http://vllm:8004/v1\x00"), nil
			}
			return []byte("PATH=/usr/bin\x00"), nil
		},
		func(string) string { return "/home/user/project" },
	)()
	r := checkCoderOutOfQueue(coderTimeoutEnvCtx())
	if r.Level != WARN {
		t.Fatalf("уровень=%s, хочу WARN (vLLM напрямую); detail=%s", r.Level, r.Detail)
	}
	if !strings.Contains(r.Detail, "101") {
		t.Errorf("WARN должен содержать PID 101: %s", r.Detail)
	}
}

// Нет qwen-процессов -> PASS «кодеров вне очереди нет».
func TestCoderOutOfQueueNone(t *testing.T) {
	defer installFakeOOQ(
		"",
		"1 0 init\n2 1 runpilot doctor\n",
		func(string) ([]byte, error) { return []byte("PATH=/usr/bin\x00"), nil },
		func(string) string { return "/" },
	)()
	r := checkCoderOutOfQueue(coderTimeoutEnvCtx())
	if r.Level != PASS {
		t.Fatalf("уровень=%s, хочу PASS (нет кодеров); detail=%s", r.Level, r.Detail)
	}
}
