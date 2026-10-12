package node

import (
	"context"
	"regexp"
	"testing"

	"runpilot/internal/detect"
	"runpilot/internal/proto"
)

// dialogScreen — экран диалога вопроса (выделенная строка с ❯).
const dialogScreen = `  Which option do you prefer?
❯ Option 1
  Option 2
  Type something...
────────────────────────────────────────────────
  ➜ /tmp/proj · qwen3.6-27b
`

// dialogScreenOpt2 — диалог после Down (выделен Option 2).
const dialogScreenOpt2 = `  Which option do you prefer?
  Option 1
❯ Option 2
  Type something...
────────────────────────────────────────────────
  ➜ /tmp/proj · qwen3.6-27b
`

// dialogMsg — команда dialog_answer.
func dialogMsg(paneID string, steps []proto.DialogStep) proto.Msg {
	m := proto.New(proto.KindDialogAnswer)
	m.CmdID, m.PaneID, m.DialogSteps = "c9", paneID, steps
	return m
}

// dialogRunner — CommandRunner с dialog-профилем.
func dialogRunner(s *screenExec, re detect.Regexps) *CommandRunner {
	r := s.run(re)
	r.profile.DialogKeys = []string{"Up", "Down", "Enter", "Space", "Tab"}
	r.profile.DialogRegex = regexp.MustCompile(`❯|Type something`)
	r.profile.DialogSelectedRegex = regexp.MustCompile(`^❯`)
	return r
}

// TestDialogAnswerOK — шаги выполняются, выделенная строка совпадает.
func TestDialogAnswerOK(t *testing.T) {
	re := testRegexps(t)
	// screenAfter — экран после ПЕРВОГО send-keys (Down → Option 2).
	s := &screenExec{fakeExec: &fakeExec{}, screen: dialogScreen, screenAfter: dialogScreenOpt2, paneSID: "S1"}
	runner := dialogRunner(s, re)
	steps := []proto.DialogStep{
		{Key: "Down", Expected: "Option 2"},
		{Key: "Enter"},
	}
	r, err := runner.HandleCommand(context.Background(), "default", dialogMsg("%10", steps))
	if err != nil {
		t.Fatal(err)
	}
	if r.Result != ResSent {
		t.Fatalf("result = %s, хочу SENT", r.Result)
	}
	if s.callCount("send-keys") != 2 {
		t.Fatalf("send-keys: %d, хочу 2", s.callCount("send-keys"))
	}
}

// TestDialogAnswerMismatch — выделенная строка не совпала → DIALOG_MISMATCH.
func TestDialogAnswerMismatch(t *testing.T) {
	re := testRegexps(t)
	// Экран не меняется после Down → Expected "Option 3" не совпадёт.
	s := &screenExec{fakeExec: &fakeExec{}, screen: dialogScreen, paneSID: "S1"}
	runner := dialogRunner(s, re)
	steps := []proto.DialogStep{{Key: "Down", Expected: "Option 3"}}
	r, err := runner.HandleCommand(context.Background(), "default", dialogMsg("%10", steps))
	if err != nil {
		t.Fatal(err)
	}
	if r.Result != ResDialogMismatch {
		t.Fatalf("result = %s, хочу DIALOG_MISMATCH", r.Result)
	}
}

// TestDialogAnswerBadKey — клавиша вне dialog_keys → BAD_KEYS.
func TestDialogAnswerBadKey(t *testing.T) {
	re := testRegexps(t)
	s := &screenExec{fakeExec: &fakeExec{}, screen: dialogScreen, paneSID: "S1"}
	runner := dialogRunner(s, re)
	steps := []proto.DialogStep{{Key: "Escape"}}
	r, err := runner.HandleCommand(context.Background(), "default", dialogMsg("%10", steps))
	if err != nil {
		t.Fatal(err)
	}
	if r.Result != ResBadKeys {
		t.Fatalf("result = %s, хочу BAD_KEYS", r.Result)
	}
}

// TestDialogAnswerGone — панели нет → PANE_GONE.
func TestDialogAnswerGone(t *testing.T) {
	re := testRegexps(t)
	s := &screenExec{fakeExec: &fakeExec{}, screen: dialogScreen, paneSID: "S1", paneGone: true}
	runner := dialogRunner(s, re)
	r, err := runner.HandleCommand(context.Background(), "default", dialogMsg("%10", []proto.DialogStep{{Key: "Enter"}}))
	if err != nil {
		t.Fatal(err)
	}
	if r.Result != ResPaneGone {
		t.Fatalf("result = %s, хочу PANE_GONE", r.Result)
	}
}

// TestDialogAnswerNotInDialog — экран не диалог → DIALOG_MISMATCH.
func TestDialogAnswerNotInDialog(t *testing.T) {
	re := testRegexps(t)
	s := &screenExec{fakeExec: &fakeExec{}, screen: idleScreen, paneSID: "S1"}
	runner := dialogRunner(s, re)
	r, err := runner.HandleCommand(context.Background(), "default", dialogMsg("%10", []proto.DialogStep{{Key: "Enter"}}))
	if err != nil {
		t.Fatal(err)
	}
	if r.Result != ResDialogMismatch {
		t.Fatalf("result = %s, хочу DIALOG_MISMATCH", r.Result)
	}
}
