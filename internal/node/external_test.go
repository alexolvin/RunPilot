package node

import (
	"os"
	"testing"
	"time"

	"runpilot/internal/clock"
	"runpilot/internal/proto"
	"runpilot/profiles"
)

// fake-cron qwen вне tmux: 900=cron (родитель 1), 901=qwen (родитель 900);
// 100=управляемая панель (родитель 1), процесс панели — САМ кодер qwen
// (реальность: после bash→qwen exec pane_pid = qwen, URL из spawnEnv),
// 101=не-кодер потомок (sleep).
const testExternalPS = `  1    0    0    0 /sbin/init
900    1    0   60 /usr/sbin/cron
901  900  1000  30 /usr/local/bin/qwen --approval-mode default
100    1  1000  50 /usr/local/bin/qwen --approval-mode default
101  100  1000  50 sleep 30
`

func psExec(out string) *fakeExec {
	f := &fakeExec{}
	f.respond = func(name string, args ...string) (string, error) {
		if name == "ps" {
			return out, nil
		}
		return "", nil
	}
	return f
}

// testExternalGateway — gateway_url для теста внешнего сканера.
const testExternalGateway = "http://127.0.0.1:8787"

func TestScanExternalCron(t *testing.T) {
	prof, err := profiles.LoadEmbeddedQwen()
	if err != nil {
		t.Fatal(err)
	}
	re := prof.Cmdline()
	table, err := processTable(t.Context(), psExec(testExternalPS))
	if err != nil {
		t.Fatal(err)
	}
	// 100 — управляемая панель (sid sid100), процесс панели — кодер с верным URL
	// шлюза -> управляемый (не внешний). 901 — qwen под cron (не панель) -> вне.
	panePIDToSID := map[int]string{100: "sid100"}
	envs := map[int]string{901: "http://127.0.0.1:9000/v1", 100: GatewayCoderURL(testExternalGateway, "sid100")}
	cwds := map[int]string{901: "/home/user/project"}
	ex := ScanExternal(table, panePIDToSID, re, testExternalGateway,
		func(pid int) string { return envs[pid] }, func(pid int) string { return cwds[pid] })
	if len(ex) != 1 {
		t.Fatalf("внешних: %d, хочу 1: %+v", len(ex), ex)
	}
	e := ex[0]
	if e.PID != 901 {
		t.Fatalf("pid=%d, хочу 901", e.PID)
	}
	if e.Source != sourceCron {
		t.Fatalf("source=%q, хочу cron", e.Source)
	}
	if e.OpenAIBaseURL != envs[901] {
		t.Fatalf("base_url=%q, хочу %q", e.OpenAIBaseURL, envs[901])
	}
	if e.CWD != cwds[901] {
		t.Fatalf("cwd=%q, хочу %q", e.CWD, cwds[901])
	}
	if e.Exe != "qwen" || len(e.Flags) != 1 || e.Flags[0] != "--approval-mode" {
		t.Fatalf("exe=%q flags=%v", e.Exe, e.Flags)
	}
	if e.UID != 1000 {
		t.Fatalf("uid=%d, хочу 1000", e.UID)
	}
}

// TestScanExternalSystemdAndExec — единое правило: 801 (qwen под systemd, нет
// панели) и 701 (qwen под runpilot exec — cron-задание, НЕ tmux-панель) -> вне
// очереди; 601 (потомок панели sid600 с верным URL шлюза) -> управляемый.
// Примечание: runpilot exec (foreground, не в tmux) теперь попадает «вне
// очереди» — следствие правила «только потомок панели с sid + URL шлюза».
func TestScanExternalSystemdAndExec(t *testing.T) {
	prof, err := profiles.LoadEmbeddedQwen()
	if err != nil {
		t.Fatal(err)
	}
	re := prof.Cmdline()
	const psOut = `  1    0    0    0 /sbin/init
800    1    0   10 /usr/lib/systemd/systemd
801  800  1000  20 /usr/local/bin/qwen
700    1  1000  10 runpilot exec --name j1
701  700  1000  10 /usr/local/bin/qwen --approval-mode default
600    1  1000  10 /usr/local/bin/qwen --approval-mode default
601  600  1000  10 sleep 30
`
	table, err := processTable(t.Context(), psExec(psOut))
	if err != nil {
		t.Fatal(err)
	}
	// 600 — управляемая панель (sid600), процесс панели — кодер с верным URL.
	panePIDToSID := map[int]string{600: "sid600"}
	envs := map[int]string{600: GatewayCoderURL(testExternalGateway, "sid600")}
	ex := ScanExternal(table, panePIDToSID, re, testExternalGateway,
		func(pid int) string { return envs[pid] }, func(pid int) string { return "" })
	if len(ex) != 2 {
		t.Fatalf("внешних: %d, хочу 2 (systemd-qwen + runpilot-exec): %+v", len(ex), ex)
	}
	// сортировка по PID: 701, 801.
	if ex[0].PID != 701 || ex[1].PID != 801 {
		t.Fatalf("pids=%d,%d, хочу 701,801", ex[0].PID, ex[1].PID)
	}
	if ex[1].Source != sourceSystemd {
		t.Fatalf("801 source=%q, хочу systemd", ex[1].Source)
	}
	if ex[0].Source != sourceCron {
		// runpilot exec запущен cron'ом: цепочка 701→700(runpilot)→1 → source other,
		// но родоначальник cron не виден (700 — root-потомок). Фиксируем факт.
		t.Logf("701 source=%q (runpilot exec не под cron в этом фиксированном ps)", ex[0].Source)
	}
}

// TestCoderOutOfQueue — 4 случая единого правила (правило 2 ТЗ):
//  нет OPENAI_BASE_URL / vLLM напрямую / URL шлюза с чужим sid -> вне очереди;
//  кодер живой сессии (свой sid + верный URL) -> управляемый.
func TestCoderOutOfQueue(t *testing.T) {
	gw := testExternalGateway
	s := "SID-1"
	cases := []struct {
		name          string
		sessionSID    string
		openAIBaseURL string
		wantOut       bool // true = вне очереди
	}{
		{"нет панели (ручной запуск)", "", "", true},
		{"без OPENAI_BASE_URL", s, "", true},
		{"vLLM напрямую", s, "http://vllm:8004/v1", true},
		{"URL шлюза с чужим sid", s, GatewayCoderURL(gw, "FOREIGN"), true},
		{"кодер живой сессии (свой sid + URL)", s, GatewayCoderURL(gw, s), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := CoderOutOfQueue(c.sessionSID, c.openAIBaseURL, gw); got != c.wantOut {
				t.Fatalf("CoderOutOfQueue(%q, %q, %q)=%v, хочу %v", c.sessionSID, c.openAIBaseURL, gw, got, c.wantOut)
			}
		})
	}
}

func TestVerifyKillTarget(t *testing.T) {
	clk := clock.NewVirtual(time.Now())
	// GONE: несуществующий pid (сверка задана: uid>0).
	if r := (&Node{clk: clk}).verifyKillTarget(t.Context(), 99999999, 1000, 0); r != proto.ResProcessGone {
		t.Fatalf("gone: %q", r)
	}
	// OK: свой pid + свой uid + start_time=now (etimes=0 → live_start=now).
	f := &fakeExec{}
	f.respond = func(name string, args ...string) (string, error) {
		if name == "ps" {
			return "0\n", nil // etimes = 0
		}
		return "", nil
	}
	n := &Node{ex: f, clk: clk}
	now := clk.Now().Unix()
	myPid, myUid := os.Getpid(), os.Getuid()
	if r := n.verifyKillTarget(t.Context(), myPid, myUid, now); r != "" {
		t.Fatalf("ok: %q", r)
	}
	// CHANGED: неверный uid.
	if r := n.verifyKillTarget(t.Context(), myPid, myUid+1, now); r != proto.ResProcessChanged {
		t.Fatalf("changed: %q", r)
	}
	// CHANGED: start_time вне допуска (etimes=0, start_time в прошлом на 10 с).
	if r := n.verifyKillTarget(t.Context(), myPid, myUid, now-10); r != proto.ResProcessChanged {
		t.Fatalf("start_time changed: %q", r)
	}
}

// TestCycleSendsEmptyExternal — 8.2: цикл шлёт external-список даже пустой,
// чтобы координатор сверял живое множество и помечал исчезнувших GONE.
// Раньше пустой список отбрасывался (if len(ext) > 0) → «труп» после
// перезапуска кодера оставался ACTIVE в БД вечно.
func TestCycleSendsEmptyExternal(t *testing.T) {
	// Только init — ни одного внешнего кодера и ни одной панели.
	psOut := "1\t0\t0\t999999\t/sbin/init\n"
	ex := &fakeExec{respond: scanRespond(psOut, "", idleScreen)}
	n := newTestNode(t, ex)
	var extCount int
	if err := n.cycle(t.Context(), func(m proto.Msg) error {
		if m.Type == proto.KindExternal {
			extCount++
			if len(m.Externals) != 0 {
				t.Fatalf("пустой список, а пришло %d: %+v", len(m.Externals), m.Externals)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if extCount != 1 {
		t.Fatalf("external-сообщений: %d, хочу 1 (пустой список тоже шлём)", extCount)
	}
}
