package node

import (
	"context"
	"testing"

	"runpilot/internal/proto"
)

func screenMsg(id string) proto.Msg {
	m := proto.New(proto.KindScreen)
	m.CmdID, m.PaneID, m.SID = id, "%10", "S1"
	return m
}

// TestScreenANSI — снимок: ANSI-текст панели + размеры окна.
func TestScreenANSI(t *testing.T) {
	re := testRegexps(t)
	s := &screenExec{fakeExec: &fakeExec{}, screen: "\x1b[31merr\x1b[0m\n", paneSID: "S1"}
	runner := s.run(re)
	r, err := runner.HandleCommand(context.Background(), "default", screenMsg("s1"))
	if err != nil || r == nil {
		t.Fatalf("err=%v rep=%v", err, r)
	}
	if r.Result != ResSent {
		t.Fatalf("result = %s, хочу SENT", r.Result)
	}
	if r.Detail != "\x1b[31merr\x1b[0m\n" {
		t.Errorf("ansi = %q", r.Detail)
	}
	if r.Cols != 80 || r.Rows != 24 {
		t.Errorf("dims = %dx%d, хочу 80x24", r.Cols, r.Rows)
	}
}

// TestScreenGone — панели нет.
func TestScreenGone(t *testing.T) {
	re := testRegexps(t)
	s := &screenExec{fakeExec: &fakeExec{}, screen: idleScreen, paneSID: "S1", paneGone: true}
	runner := s.run(re)
	r, err := runner.HandleCommand(context.Background(), "default", screenMsg("s1"))
	if err != nil {
		t.Fatal(err)
	}
	if r.Result != ResPaneGone {
		t.Fatalf("result = %s, хочу PANE_GONE", r.Result)
	}
}
