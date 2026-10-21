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
		func(pid int) string { return envs[pid] }, map[int]ExecRoot{},
		func(pid int) string { return cwds[pid] })
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

// TestScanExternalSystemd — 801 (qwen под systemd, нет панели/EXEC-корня) ->
// вне очереди; 601 (процесс управляемой панели sid600, верный URL) -> управляемый.
func TestScanExternalSystemd(t *testing.T) {
	prof, err := profiles.LoadEmbeddedQwen()
	if err != nil {
		t.Fatal(err)
	}
	re := prof.Cmdline()
	const psOut = `  1    0    0    0 /sbin/init
800    1    0   10 /usr/lib/systemd/systemd
801  800  1000  20 /usr/local/bin/qwen
600    1  1000  10 /usr/local/bin/qwen --approval-mode default
601  600  1000  10 sleep 30
`
	table, err := processTable(t.Context(), psExec(psOut))
	if err != nil {
		t.Fatal(err)
	}
	panePIDToSID := map[int]string{600: "sid600"}
	envs := map[int]string{600: GatewayCoderURL(testExternalGateway, "sid600")}
	ex := ScanExternal(table, panePIDToSID, re, testExternalGateway,
		func(pid int) string { return envs[pid] }, map[int]ExecRoot{},
		func(pid int) string { return "" })
	if len(ex) != 1 {
		t.Fatalf("внешних: %d, хочу 1 (systemd-qwen): %+v", len(ex), ex)
	}
	if ex[0].PID != 801 || ex[0].Source != sourceSystemd {
		t.Fatalf("ожидаю systemd-qwen 801: %+v", ex[0])
	}
}

// TestScanExternalTmuxServer — tmux-сервер несёт команду кодера как аргумент
// (tmux new-session … /path/qwen …), но сам кодером не является (exe=tmux)
// -> НЕ внешний. Раньше правило совпадало по всей cmdline и tmux-сервер
// управляемой сессии числился внешним кодером (source=tmux, target=unknown).
func TestScanExternalTmuxServer(t *testing.T) {
	prof, err := profiles.LoadEmbeddedQwen()
	if err != nil {
		t.Fatal(err)
	}
	re := prof.Cmdline()
	const psOut = `  1    0    0    0 /sbin/init
700    1  1000  10 tmux -L default new-session -d -s migration -c /srv/migration/ -e OPENAI_BASE_URL=http://127.0.0.1:8787/s/sidmig/v1 /home/botagent/.local/bin/qwen --approval-mode yolo --session-id e8632c3c
`
	table, err := processTable(t.Context(), psExec(psOut))
	if err != nil {
		t.Fatal(err)
	}
	ex := ScanExternal(table, map[int]string{}, re, testExternalGateway,
		func(pid int) string { return "" }, map[int]ExecRoot{},
		func(pid int) string { return "" })
	if len(ex) != 0 {
		t.Fatalf("tmux-сервер не кодер (exe=tmux), внешних хочу 0: %+v", ex)
	}
}

// TestScanExternalExecLive — кодер живой EXEC-сессии: qwen 501 под процессом
// 500, который в EXEC-корнях (PID+время старта совпадает) + верный URL шлюза
// -> управляемый (не внешний). cmdline 500 с глобальным флагом --config:
// правило НЕ разбирает cmdline (корень по PID) — флаг на результат не влияет
// («runpilot --config X exec …» и любые глобальные флаги -> управляемый).
func TestScanExternalExecLive(t *testing.T) {
	prof, err := profiles.LoadEmbeddedQwen()
	if err != nil {
		t.Fatal(err)
	}
	re := prof.Cmdline()
	const psOut = `  1    0    0    0 /sbin/init
500    1  1000  10 /usr/local/bin/runpilot --config /x exec --name j1
501  500  1000  10 /usr/local/bin/qwen --approval-mode default
`
	table, err := processTable(t.Context(), psExec(psOut))
	if err != nil {
		t.Fatal(err)
	}
	envs := map[int]string{501: GatewayCoderURL(testExternalGateway, "sid501")}
	starts := map[int]int64{500: 123456}
	old := procStartTimeFn
	procStartTimeFn = func(pid int) int64 { return starts[pid] }
	defer func() { procStartTimeFn = old }()
	ex := ScanExternal(table, map[int]string{}, re, testExternalGateway,
		func(pid int) string { return envs[pid] },
		map[int]ExecRoot{500: {Starttime: 123456, SID: "sid501"}},
		func(pid int) string { return "" })
	if len(ex) != 0 {
		t.Fatalf("кодер живой EXEC-сессии управляемый, внешних хочу 0: %+v", ex)
	}
}

// TestScanExternalExecPIDReused — PID корня переиспользован другим процессом:
// EXEC-корень {500, starttime=123456}, но процесс 500 — другой (starttime 999)
// -> время старта не совпадает -> не потомок живого корня -> вне очереди
// (даже с «верным» URL). Защита от переиспользования PID.
func TestScanExternalExecPIDReused(t *testing.T) {
	prof, err := profiles.LoadEmbeddedQwen()
	if err != nil {
		t.Fatal(err)
	}
	re := prof.Cmdline()
	const psOut = `  1    0    0    0 /sbin/init
500    1  1000  10 /usr/local/bin/other
501  500  1000  10 /usr/local/bin/qwen --approval-mode default
`
	table, err := processTable(t.Context(), psExec(psOut))
	if err != nil {
		t.Fatal(err)
	}
	envs := map[int]string{501: GatewayCoderURL(testExternalGateway, "sid501")}
	// PID 500 жив, но это ДРУГОЙ процесс (starttime 999 != сохранённому 123456).
	starts := map[int]int64{500: 999}
	old := procStartTimeFn
	procStartTimeFn = func(pid int) int64 { return starts[pid] }
	defer func() { procStartTimeFn = old }()
	ex := ScanExternal(table, map[int]string{}, re, testExternalGateway,
		func(pid int) string { return envs[pid] },
		map[int]ExecRoot{500: {Starttime: 123456, SID: "sid501"}},
		func(pid int) string { return "" })
	if len(ex) != 1 || ex[0].PID != 501 {
		t.Fatalf("PID переиспользован -> вне очереди: внешних=%d, хочу 1 (501): %+v", len(ex), ex)
	}
}

// TestScanExternalExecFinished — EXEC-сессия завершена: exec-процесс умер
// (нет в таблице), qwen 501 репарентирован в init (родитель 1), EXEC-корней
// нет (GONE) -> не потомок живого корня -> вне очереди.
func TestScanExternalExecFinished(t *testing.T) {
	prof, err := profiles.LoadEmbeddedQwen()
	if err != nil {
		t.Fatal(err)
	}
	re := prof.Cmdline()
	const psOut = `  1    0    0    0 /sbin/init
501    1  1000  10 /usr/local/bin/qwen --approval-mode default
`
	table, err := processTable(t.Context(), psExec(psOut))
	if err != nil {
		t.Fatal(err)
	}
	envs := map[int]string{501: GatewayCoderURL(testExternalGateway, "sid501")}
	ex := ScanExternal(table, map[int]string{}, re, testExternalGateway,
		func(pid int) string { return envs[pid] },
		map[int]ExecRoot{},
		func(pid int) string { return "" })
	if len(ex) != 1 || ex[0].PID != 501 {
		t.Fatalf("qwen завершённой EXEC-сессии вне очереди: внешних=%d, хочу 1 (501): %+v", len(ex), ex)
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
