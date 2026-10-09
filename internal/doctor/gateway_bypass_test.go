package doctor

// W13 этап 3: checkGatewayBypass — управляемый кодер (дерево процессов, вкл.
// субагенты) держит прямое ESTAB-соединение с upstream (vLLM-порт) в обход
// шлюза → FAIL с sid+PID.

import (
	"strings"
	"testing"

	"runpilot/internal/config"
)

// gatewayBypassCtx — контекст с сервером, чей upstream на порту 8004.
func gatewayBypassCtx() *Context {
	cfg := &config.Config{}
	cfg.Node.TmuxSockets = []string{"default"}
	cfg.Coordinator.GatewayURL = "http://127.0.0.1:8787"
	cfg.Servers = []config.Server{{
		Name:      "vzu5",
		Upstreams: config.Upstreams{OpenAI: config.UpstreamOpenAI{URL: "http://100.126.222.23:8004/v1"}},
	}}
	return &Context{Cfg: cfg, CfgPath: "/tmp/cfg.yaml", Cmdline: qwenCmdlineRe}
}

// installFakeBypass — подменяет tmuxListPanes + psOutput + ssTnp.
func installFakeBypass(panes, ps, ss string) func() {
	oldTmux, oldPs, oldSS := tmuxListPanes, psOutput, ssTnp
	tmuxListPanes = func(string) ([]byte, error) { return []byte(panes), nil }
	psOutput = func() ([]byte, error) { return []byte(ps), nil }
	ssTnp = func() ([]byte, error) { return []byte(ss), nil }
	return func() { tmuxListPanes, psOutput, ssTnp = oldTmux, oldPs, oldSS }
}

// Кодер живой сессии держит прямое соединение с upstream (8004) → FAIL с
// sid и PID.
func TestGatewayBypassFail(t *testing.T) {
	defer installFakeBypass(
		"0\t100\tphonebroker\tSID100",
		"100 1 bash\n101 100 qwen\n",
		"ESTAB 0 0 100.124.155.106:54321 100.126.222.23:8004 users:((\"qwen\",pid=101,fd=25))\n",
	)()
	r := checkGatewayBypass(gatewayBypassCtx())
	if r.Level != FAIL {
		t.Fatalf("уровень=%s, хочу FAIL; detail=%s", r.Level, r.Detail)
	}
	if !strings.Contains(r.Detail, "SID100") || !strings.Contains(r.Detail, "101") {
		t.Errorf("FAIL должен содержать sid и PID: %s", r.Detail)
	}
}

// Субагент (qwen-потомок кодера) держит прямое соединение с upstream → FAIL
// (проверяется поддерево, не только кодер).
func TestGatewayBypassSubagent(t *testing.T) {
	defer installFakeBypass(
		"0\t100\tphonebroker\tSID100",
		"100 1 bash\n101 100 qwen\n102 101 qwen\n",
		"ESTAB 0 0 100.124.155.106:54322 100.126.222.23:8004 users:((\"qwen\",pid=102,fd=25))\n",
	)()
	r := checkGatewayBypass(gatewayBypassCtx())
	if r.Level != FAIL {
		t.Fatalf("уровень=%s, хочу FAIL (субагент); detail=%s", r.Level, r.Detail)
	}
	if !strings.Contains(r.Detail, "SID100") || !strings.Contains(r.Detail, "102") {
		t.Errorf("FAIL должен содержать sid и PID субагента: %s", r.Detail)
	}
}

// Кодер ходит через шлюз (порт 8787) → PASS (не bypass).
func TestGatewayBypassPassViaGateway(t *testing.T) {
	defer installFakeBypass(
		"0\t100\tphonebroker\tSID100",
		"100 1 bash\n101 100 qwen\n",
		"ESTAB 0 0 127.0.0.1:54321 127.0.0.1:8787 users:((\"qwen\",pid=101,fd=25))\n",
	)()
	r := checkGatewayBypass(gatewayBypassCtx())
	if r.Level != PASS {
		t.Fatalf("уровень=%s, хочу PASS (через шлюз); detail=%s", r.Level, r.Detail)
	}
}

// Соединение с чужим (не upstream, не шлюз) портом → не bypass → PASS.
func TestGatewayBypassForeignPort(t *testing.T) {
	defer installFakeBypass(
		"0\t100\tphonebroker\tSID100",
		"100 1 bash\n101 100 qwen\n",
		"ESTAB 0 0 127.0.0.1:54321 93.184.216.34:443 users:((\"qwen\",pid=101,fd=25))\n",
	)()
	r := checkGatewayBypass(gatewayBypassCtx())
	if r.Level != PASS {
		t.Fatalf("уровень=%s, хочу PASS (чужой порт); detail=%s", r.Level, r.Detail)
	}
}

// Нет управляемых сессий → PASS.
func TestGatewayBypassNoSessions(t *testing.T) {
	defer installFakeBypass("", "100 1 qwen\n",
		"ESTAB 0 0 100.124.155.106:54321 100.126.222.23:8004 users:((\"qwen\",pid=100,fd=25))\n")()
	r := checkGatewayBypass(gatewayBypassCtx())
	if r.Level != PASS {
		t.Fatalf("уровень=%s, хочу PASS (нет сессий); detail=%s", r.Level, r.Detail)
	}
}

// parseSS — разбор peer-порта и pid.
func TestParseSS(t *testing.T) {
	out := "ESTAB 0 0 100.124.155.106:54321 100.126.222.23:8004 users:((\"qwen\",pid=101,fd=25))\n" +
		"ESTAB 0 0 127.0.0.1:54322 127.0.0.1:8787 users:((\"qwen\",pid=102,fd=26))\n" +
		"LISTEN 0 128 0.0.0.0:8787 0.0.0.0:* users:((\"runpilot\",pid=50,fd=3))\n"
	conns := parseSS([]byte(out))
	if len(conns) != 2 {
		t.Fatalf("соединений=%d, хочу 2 (только ESTAB): %+v", len(conns), conns)
	}
	if conns[0].peerPort != 8004 || conns[0].pid != 101 {
		t.Errorf("conns[0]=%+v, хочу peerPort=8004 pid=101", conns[0])
	}
	if conns[1].peerPort != 8787 || conns[1].pid != 102 {
		t.Errorf("conns[1]=%+v, хочу peerPort=8787 pid=102", conns[1])
	}
}
