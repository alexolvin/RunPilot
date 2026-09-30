package node

import (
	"context"
	"errors"
	"strings"
	"testing"

	"runpilot/profiles"
)

const testPanesOutput = `sess1	0	0	%10	100	qwen	0	S123456789
sess1	0	1	%11	200	qwen	0	
sess2	0	0	%12	300	vim	0	
sess2	0	1	%13	400	qwen	1	S999999999
sess3	0	0	%14	500	node	0	S777777777
`

const testPSOutput = `  1    0    0    0 /sbin/init
100    1  1000  50 bash -lc /usr/local/bin/qwen
101  100  1000  50 /usr/local/bin/qwen --approval-mode default
200    1  1000  50 bash
201  200  1000  50 /usr/local/bin/qwen
300    1  1000  50 bash
301  300  1000  50 vim /etc/hosts
400    1  1000  50 bash
500    1  1000  50 node /tmp/fakebin/qwen
`

func testScannerExec(t *testing.T, missingSocket bool) *fakeExec {
	t.Helper()
	f := &fakeExec{}
	f.respond = func(name string, args ...string) (string, error) {
		joined := strings.Join(args, " ")
		switch {
		case name == "ps":
			return testPSOutput, nil
		case name == "tmux" && strings.Contains(joined, "list-panes"):
			if missingSocket {
				return "", errors.New("no server running")
			}
			return testPanesOutput, nil
		}
		return "", nil
	}
	return f
}

func TestScanManagedUnmanaged(t *testing.T) {
	prof, err := profiles.LoadEmbeddedQwen()
	if err != nil {
		t.Fatal(err)
	}
	f := testScannerExec(t, false)
	panes, err := Scan(context.Background(), f, []string{"default"}, prof.Cmdline())
	if err != nil {
		t.Fatal(err)
	}
	if f.callCount("ps ") != 1 {
		t.Fatalf("ps за цикл: %d, хочу 1 (раздел 7 ТЗ)", f.callCount("ps "))
	}
	byID := map[string]PaneInfo{}
	for _, p := range panes {
		byID[p.PaneID] = p
	}
	if len(byID) != 5 {
		t.Fatalf("панелей: %d, хочу 5", len(byID))
	}
	if !byID["%10"].Managed() || byID["%10"].SID != "S123456789" {
		t.Fatalf("%%10 должен быть управляемым с SID: %+v", byID["%10"])
	}
	if !byID["%11"].Unmanaged() {
		t.Fatalf("%%11 (qwen без @runpilot_sid) должен быть UNMANAGED: %+v", byID["%11"])
	}
	if byID["%12"].Match || byID["%12"].Managed() || byID["%12"].Unmanaged() {
		t.Fatalf("%%12 (vim) не кодер: %+v", byID["%12"])
	}
	if !byID["%13"].Dead {
		t.Fatalf("%%13 — dead-панель: %+v", byID["%13"])
	}
	if byID["%13"].Match {
		t.Fatalf("dead-панель %%13 не сканируется по процессам: %+v", byID["%13"])
	}
	// Production-кейс: совпадает сам процесс панели (node /…/qwen),
	// потомки совпадений не дают.
	if !byID["%14"].Managed() || byID["%14"].SID != "S777777777" {
		t.Fatalf("%%14 (совпадение в самом процессе панели) должен быть управляемым: %+v", byID["%14"])
	}
}

func TestScanMissingSocketSkipped(t *testing.T) {
	prof, err := profiles.LoadEmbeddedQwen()
	if err != nil {
		t.Fatal(err)
	}
	f := testScannerExec(t, true)
	panes, err := Scan(context.Background(), f, []string{"no-such-socket"}, prof.Cmdline())
	if err != nil {
		t.Fatalf("отсутствующий сокет не должен ронять цикл: %v", err)
	}
	if len(panes) != 0 {
		t.Fatalf("панелей: %d, хочу 0", len(panes))
	}
}
