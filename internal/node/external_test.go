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
// 100=управляемая панель (родитель 1), 101=потомок 100 (исключается).
const testExternalPS = `  1    0    0    0 /sbin/init
900    1    0   60 /usr/sbin/cron
901  900  1000  30 /usr/local/bin/qwen --approval-mode default
100    1  1000  50 bash -lc /usr/local/bin/qwen
101  100  1000  50 /usr/local/bin/qwen --approval-mode default
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
	panePIDs := map[int]bool{100: true} // управляемая панель %10
	envs := map[int]string{901: "http://127.0.0.1:9000/v1"}
	ex := ScanExternal(table, panePIDs, re, func(pid int) string { return envs[pid] })
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
	if e.Exe != "qwen" || len(e.Flags) != 1 || e.Flags[0] != "--approval-mode" {
		t.Fatalf("exe=%q flags=%v", e.Exe, e.Flags)
	}
	if e.UID != 1000 {
		t.Fatalf("uid=%d, хочу 1000", e.UID)
	}
}

func TestScanExternalSystemdAndExclusion(t *testing.T) {
	prof, err := profiles.LoadEmbeddedQwen()
	if err != nil {
		t.Fatal(err)
	}
	re := prof.Cmdline()
	// 800=systemd, 801=qwen (systemd); 700=runpilot, 701=qwen (runpilot exec → исключается);
	// 600=управляемая панель, 601=потомок (в tmux → не внешний).
	const psOut = `  1    0    0    0 /sbin/init
800    1    0   10 /usr/lib/systemd/systemd
801  800  1000  20 /usr/local/bin/qwen
700    1  1000  10 runpilot exec --name j1
701  700  1000  10 /usr/local/bin/qwen --approval-mode default
600    1  1000  10 bash -lc /usr/local/bin/qwen
601  600  1000  10 /usr/local/bin/qwen
`
	table, err := processTable(t.Context(), psExec(psOut))
	if err != nil {
		t.Fatal(err)
	}
	panePIDs := map[int]bool{600: true}
	ex := ScanExternal(table, panePIDs, re, func(pid int) string { return "" })
	if len(ex) != 1 {
		t.Fatalf("внешних: %d, хочу 1 (только systemd-qwen): %+v", len(ex), ex)
	}
	if ex[0].PID != 801 || ex[0].Source != sourceSystemd {
		t.Fatalf("ожидаю systemd-qwen 801: %+v", ex[0])
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
