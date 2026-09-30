package node

// 8.3 X1: adopt (принять открытую tmux-панель под runpilot), дельта pane_gone
// и полный список unmanaged в цикле.

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"runpilot/internal/proto"
)

// paneLine — строка list-panes (10 tab-полей).
func paneLine(sess, id string, pid int, cmd string, dead int, sid, exit, dir string) string {
	return fmt.Sprintf("%s\t0\t0\t%s\t%d\t%s\t%d\t%s\t%s\t%s",
		sess, id, pid, cmd, dead, sid, exit, dir)
}

// scanRespond — responder fakeExec для скана/снимка: ps → psOut,
// capture-pane → screenOut, list-panes (любая форма) → paneOut,
// остальные tmux (send-keys/respawn/set-option) → пустой ответ OK.
func scanRespond(psOut, paneOut, screenOut string) func(string, ...string) (string, error) {
	return func(name string, args ...string) (string, error) {
		joined := strings.Join(args, " ")
		switch {
		case name == "ps":
			return psOut, nil
		case strings.Contains(joined, "capture-pane"):
			return screenOut, nil
		case strings.Contains(joined, "list-panes"):
			return paneOut, nil
		default:
			return "", nil
		}
	}
}

// TestAdopt — полный путь: UNMANAGED + IDLE → quit_text → respawn-pane -k →
// @runpilot_sid → ADOPTED.
func TestAdopt(t *testing.T) {
	const paneID = "%30"
	// Кода не запущен (bash в панели) — «выход» мгновенный; панель жива.
	psOut := "1\t0\t0\t999999\t/sbin/init\n4242\t1\t1000\t50\tbash\n"
	paneOut := paneLine("manproj", paneID, 4242, "bash", 0, "", "", "~/proj")
	ex := &fakeExec{respond: scanRespond(psOut, paneOut, idleScreen)}
	n := newTestNode(t, ex)
	seedUnmanaged(t, n, PaneInfo{PaneID: paneID})

	m := proto.New(proto.KindAdopt)
	m.CmdID = "c1"
	m.PaneID = paneID
	m.SID = "NEWSID"
	m.Env = map[string]string{"OPENAI_API_KEY": "runpilot"}
	rep, err := n.Handle(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Result != proto.ResAdopted {
		t.Fatalf("Result = %q, хочу ADOPTED", rep.Result)
	}
	if !ex.callContains("send-keys", "-l", n.prof.QuitText) {
		t.Error("не отправлен quit_text (send-keys -l)")
	}
	if !ex.callContains("send-keys", n.prof.SubmitKeys) {
		t.Error("не отправлена submit-клавиша")
	}
	if !ex.callContains("respawn-pane", "-k", paneID) {
		t.Error("нет respawn-pane -k")
	}
	if !ex.callContains("set-option", "@runpilot_sid", "NEWSID") {
		t.Error("не поставлен @runpilot_sid")
	}
}

// TestAdoptNotIdle — кодер в панели BUSY → NOT_IDLE (ТЗ: только IDLE).
func TestAdoptNotIdle(t *testing.T) {
	const paneID = "%31"
	psOut := "1\t0\t0\t999999\t/sbin/init\n5001\t1\t1000\t50\tqwen\n"
	paneOut := paneLine("busy", paneID, 5001, "qwen", 0, "", "", "/p")
	ex := &fakeExec{respond: scanRespond(psOut, paneOut, busyScreen)}
	n := newTestNode(t, ex)
	seedUnmanaged(t, n, PaneInfo{PaneID: paneID})

	m := proto.New(proto.KindAdopt)
	m.CmdID = "c1"
	m.PaneID = paneID
	m.SID = "NEWSID"
	rep, err := n.Handle(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Result != proto.ResNotIdle {
		t.Fatalf("Result = %q, хочу NOT_IDLE", rep.Result)
	}
	if ex.callContains("respawn-pane") {
		t.Error("respawn при BUSY запрещён")
	}
}

// TestAdoptManaged — панель уже с @runpilot_sid → MANAGED.
func TestAdoptManaged(t *testing.T) {
	const paneID = "%32"
	psOut := "1\t0\t0\t999999\t/sbin/init\n5002\t1\t1000\t50\tqwen\n"
	paneOut := paneLine("old", paneID, 5002, "qwen", 0, "OLDSID", "", "/p")
	ex := &fakeExec{respond: scanRespond(psOut, paneOut, idleScreen)}
	n := newTestNode(t, ex)
	seedPane(t, n, paneID, "default")

	m := proto.New(proto.KindAdopt)
	m.CmdID = "c1"
	m.PaneID = paneID
	m.SID = "NEWSID"
	rep, err := n.Handle(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Result != proto.ResManaged || rep.Detail != "OLDSID" {
		t.Fatalf("Result = %q/%q, хочу MANAGED/OLDSID", rep.Result, rep.Detail)
	}
}

// TestAdoptPaneGone — панели нет ни в снапшоте, ни в реестре unmanaged →
// PANE_GONE (socketFor не нашёл сокет — исходный сценарий «панель отсутствует»).
func TestAdoptPaneGone(t *testing.T) {
	const paneID = "%33"
	ex := &fakeExec{respond: scanRespond("", "", "")}
	n := newTestNode(t, ex)

	m := proto.New(proto.KindAdopt)
	m.CmdID = "c1"
	m.PaneID = paneID
	m.SID = "NEWSID"
	rep, err := n.Handle(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Result != ResPaneGone {
		t.Fatalf("Result = %q, хочу PANE_GONE", rep.Result)
	}
}

// TestCyclePaneGoneAndUnmanaged — цикл: управляемая панель исчезла →
// KindPaneGone дельта; UNMANAGED-панель → полный список unmanaged.
func TestCyclePaneGoneAndUnmanaged(t *testing.T) {
	psOut := "1\t0\t0\t999999\t/sbin/init\n" +
		"5001\t1\t1000\t50\tqwen\n" +
		"5002\t1\t1000\t50\tqwen\n"
	managed := paneLine("s1", "%31", 5001, "qwen", 0, "S1", "", "/p1")
	unmanaged := paneLine("s2", "%32", 5002, "qwen", 0, "", "", "/p2")
	ex := &fakeExec{respond: scanRespond(psOut, managed+"\n"+unmanaged, idleScreen)}
	n := newTestNode(t, ex)

	send := func(m proto.Msg) error { return nil }
	if err := n.cycle(context.Background(), send); err != nil {
		t.Fatal(err)
	}
	// Второй цикл: %31 исчез, %32 осталась.
	ex.respond = scanRespond(psOut, unmanaged, idleScreen)
	var msgs []proto.Msg
	if err := n.cycle(context.Background(), func(m proto.Msg) error {
		msgs = append(msgs, m)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	gone, unman := false, -1
	for _, m := range msgs {
		if m.Type == proto.KindPaneGone {
			gone = m.PaneID == "%31" && m.SID == "S1"
		}
		if m.Type == proto.KindUnmanaged {
			unman = len(m.Unmanaged)
		}
	}
	if !gone {
		t.Error("нет дельты pane_gone для %31/S1")
	}
	if unman != 1 {
		t.Errorf("unmanaged = %d, хочу 1 (%%32)", unman)
	}
	// Реестр unmanaged (8.3 X1): после цикла содержит только живую %32.
	if _, ok := n.unmanaged["%32"]; !ok {
		t.Error("реестр unmanaged не содержит %32 после цикла")
	}
	if _, ok := n.unmanaged["%31"]; ok {
		t.Error("реестр unmanaged содержит исчезнувшую %31")
	}
}
