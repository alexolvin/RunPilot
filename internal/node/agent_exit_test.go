package node

// C1 (7.3): узел читает @runpilot_exit из панели и шлёт KindAgentExit координатору
// (edge-trigger: по одному разу на переход, пока опция не снята respawn).

import (
	"context"
	"strings"
	"testing"

	"runpilot/internal/proto"
	"runpilot/profiles"
)

// TestScanParsesExit — @runpilot_exit из list-panes попадает в PaneInfo.Exit;
// пустая опция (живой кодер) — пусто.
func TestScanParsesExit(t *testing.T) {
	prof, err := profiles.LoadEmbeddedQwen()
	if err != nil {
		t.Fatal(err)
	}
	out := "sess1\t0\t0\t%10\t100\tqwen\t0\tS123456789\t137\n" +
		"sess1\t0\t1\t%11\t200\tqwen\t0\t\t\n"
	f := &fakeExec{}
	f.respond = func(name string, args ...string) (string, error) {
		switch {
		case name == "ps":
			return testPSOutput, nil
		case strings.Contains(strings.Join(args, " "), "list-panes"):
			return out, nil
		}
		return "", nil
	}
	panes, err := Scan(context.Background(), f, []string{"default"}, prof.Cmdline())
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]PaneInfo{}
	for _, p := range panes {
		byID[p.PaneID] = p
	}
	if got := byID["%10"].Exit; got != "137" {
		t.Fatalf("%%10 @runpilot_exit=%q, хочу 137", got)
	}
	if got := byID["%11"].Exit; got != "" {
		t.Fatalf("%%11 @runpilot_exit=%q, хочу пусто (живой кодер)", got)
	}
}

// TestReportAgentExit — edge-trigger: первый раз шлём, повтор — нет; после
// снятия опции (respawn) следующий выход снова шлём.
func TestReportAgentExit(t *testing.T) {
	n := &Node{sentExit: map[string]bool{}}
	var sent []proto.Msg
	send := func(m proto.Msg) error {
		sent = append(sent, m)
		return nil
	}

	n.reportAgentExit(PaneInfo{PaneID: "p1", SID: "ss1", Exit: "137"}, send)
	if len(sent) != 1 || sent[0].Type != proto.KindAgentExit || sent[0].SID != "ss1" || sent[0].ExitCode != 137 {
		t.Fatalf("первый exit не отправлен: %+v", sent)
	}
	n.reportAgentExit(PaneInfo{PaneID: "p1", SID: "ss1", Exit: "137"}, send)
	if len(sent) != 1 {
		t.Fatalf("повтор шлём повторно (len=%d), должно быть 1", len(sent))
	}
	// опция снята (respawn) → сброс; новый выход снова шлём.
	n.reportAgentExit(PaneInfo{PaneID: "p1", SID: "ss1", Exit: ""}, send)
	n.reportAgentExit(PaneInfo{PaneID: "p1", SID: "ss1", Exit: "1"}, send)
	if len(sent) != 2 || sent[1].ExitCode != 1 {
		t.Fatalf("после respawn: %+v", sent)
	}
}
