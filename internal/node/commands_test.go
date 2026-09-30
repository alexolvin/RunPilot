package node

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"runpilot/internal/detect"
	"runpilot/internal/proto"
)

// screenExec — fakeExec для команд: list-panes + capture + запись send-keys.
// screenAfter (если задан) — экран после ПЕРВОГО send-keys (имитирует выход
// панели из PROMPT при approve).
type screenExec struct {
	*fakeExec
	screen      string
	screenAfter string
	afterSent   bool
	paneSID     string
	paneGone    bool
	sleeps      []time.Duration
}

func (s *screenExec) run(re detect.Regexps) *CommandRunner {
	s.fakeExec.respond = func(name string, args ...string) (string, error) {
		joined := strings.Join(args, " ")
		switch {
		case strings.Contains(joined, "list-panes"):
			if s.paneGone {
				return "", errors.New("can't find pane")
			}
			return "sess1\t0\t0\t%10\t100\tqwen\t0\t" + s.paneSID, nil
		case strings.Contains(joined, "capture-pane"):
			if s.paneGone {
				return "", errors.New("can't find pane")
			}
			return s.screen, nil
		case strings.Contains(joined, "send-keys"):
			if s.screenAfter != "" && !s.afterSent {
				s.afterSent = true
				s.screen = s.screenAfter
			}
			return "", nil
		case strings.Contains(joined, "set-option"):
			return "", nil
		case strings.Contains(joined, "display-message"):
			return "80 24", nil
		}
		return "", nil
	}
	runner := NewCommandRunner(s.fakeExec, re, Profile{
		SubmitKeys:       []string{"Enter"},
		CancelKeys:       []string{"Escape"},
		ResumeText:       "continue",
		CompressText:     "/compact",
		ApprovalOptions: []ApprovalOption{
			{ID: "allow", Keys: []string{"y"}},
			{ID: "deny", Keys: []string{"n"}},
		},
	}, time.Second)
	runner.sleep = func(d time.Duration) { s.sleeps = append(s.sleeps, d) }
	return runner
}

// normHash — хеш экрана тем же путём, что capturePane.
func (s *screenExec) normHash(re detect.Regexps) uint64 {
	return detect.Hash(detect.Normalize(s.screen, re))
}

func dispatchMsg(mode string, expect uint64) proto.Msg {
	m := proto.New(proto.KindDispatch)
	m.CmdID, m.SID, m.PaneID, m.Mode, m.ExpectHash = "c1", "S1", "%10", mode, expect
	return m
}

func TestDispatchOrder(t *testing.T) {
	re := testRegexps(t)
	cases := []struct {
		name     string
		screen   string
		mode     string
		paneGone bool
		sid      string
		expect   int64 // 0 — несовпадение (кроме PANE_GONE/BUSY/WAIT_UI, где не до хеша)
		want     string
	}{
		{"PANE_GONE_нет_панели", idleScreen, "submit", true, "S1", 0, ResPaneGone},
		{"PANE_GONE_чужой_sid", idleScreen, "submit", false, "OTHER", 0, ResPaneGone},
		{"ALREADY_BUSY", busyScreen, "submit", false, "S1", 0, ResAlreadyBusy},
		{"PANE_WAIT_UI", waitUIScreen, "submit", false, "S1", 0, ResPaneWaitUI},
		{"PANE_CHANGED", idleScreen, "submit", false, "S1", 1, ResPaneChanged},
		{"EMPTY_INPUT", idleScreen, "submit", false, "S1", -1, ResEmptyInput}, // -1: реальный хеш
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := &screenExec{fakeExec: &fakeExec{}, screen: c.screen, paneSID: "S1", paneGone: c.paneGone}
			runner := s.run(re)
			expect := uint64(c.expect)
			if c.expect < 0 {
				expect = s.normHash(re)
			}
			m := dispatchMsg(c.mode, expect)
			if c.sid != "S1" {
				m.SID = c.sid
			}
			r, err := runner.HandleCommand(context.Background(), "default", m)
			if err != nil {
				t.Fatal(err)
			}
			if r.Result != c.want {
				t.Fatalf("result = %s, хочу %s", r.Result, c.want)
			}
			if c.want == ResAlreadyBusy || c.want == ResPaneWaitUI {
				if n := s.callCount("send-keys"); n != 0 {
					t.Fatalf("клавиши отправлены при %s: %d", c.want, n)
				}
			}
		})
	}
}

func TestDispatchSubmitSendsEnter(t *testing.T) {
	re := testRegexps(t)
	s := &screenExec{fakeExec: &fakeExec{}, screen: idleInputScreen, paneSID: "S1"}
	runner := s.run(re)
	m := dispatchMsg("submit", s.normHash(re))
	r, err := runner.HandleCommand(context.Background(), "default", m)
	if err != nil {
		t.Fatal(err)
	}
	if r.Result != ResSent {
		t.Fatalf("result = %s, хочу SENT", r.Result)
	}
	if n := s.callCount("send-keys"); n != 1 {
		t.Fatalf("send-keys: %d, хочу 1", n)
	}
	last := s.lastCall()
	if !strings.Contains(last, "send-keys") || !strings.HasSuffix(last, "Enter") {
		t.Fatalf("команда = %q, хочу send-keys … Enter", last)
	}
}

func TestDispatchResumeSendsTextThenEnter(t *testing.T) {
	re := testRegexps(t)
	s := &screenExec{fakeExec: &fakeExec{}, screen: idleScreen, paneSID: "S1"}
	runner := s.run(re)
	m := dispatchMsg("resume", s.normHash(re))
	m.ResumeText = "continue"
	r, err := runner.HandleCommand(context.Background(), "default", m)
	if err != nil {
		t.Fatal(err)
	}
	if r.Result != ResSent {
		t.Fatalf("result = %s, хочу SENT", r.Result)
	}
	if len(s.sleeps) != 1 {
		t.Fatalf("пауза resume_key_delay: %d раз, хочу 1", len(s.sleeps))
	}
	s.fakeExec.mu.Lock()
	var sendKeys [][]string
	for _, c := range s.fakeExec.calls {
		if c[0] == "tmux" && strings.Contains(strings.Join(c, " "), "send-keys") {
			sendKeys = append(sendKeys, c)
		}
	}
	s.fakeExec.mu.Unlock()
	if len(sendKeys) != 2 {
		t.Fatalf("send-keys вызовов: %d, хочу 2 (текст + Enter)", len(sendKeys))
	}
	if !strings.Contains(strings.Join(sendKeys[0], " "), "-l") ||
		!strings.HasSuffix(strings.Join(sendKeys[0], " "), "continue") {
		t.Fatalf("первый вызов = %q, хочу send-keys -l … continue", sendKeys[0])
	}
	if !strings.HasSuffix(strings.Join(sendKeys[1], " "), "Enter") {
		t.Fatalf("второй вызов = %q, хочу … Enter", sendKeys[1])
	}
}

func TestDispatchResumeOperatorInputWins(t *testing.T) {
	re := testRegexps(t)
	s := &screenExec{fakeExec: &fakeExec{}, screen: idleInputScreen, paneSID: "S1"}
	runner := s.run(re)
	m := dispatchMsg("resume", s.normHash(re))
	m.ResumeText = "continue"
	r, err := runner.HandleCommand(context.Background(), "default", m)
	if err != nil {
		t.Fatal(err)
	}
	if r.Result != ResSent {
		t.Fatalf("result = %s, хочу SENT", r.Result)
	}
	if len(s.sleeps) != 0 {
		t.Fatal("непустой ввод оператора: resume_text не отправляется (раздел 8 ТЗ)")
	}
	if n := s.callCount("send-keys"); n != 1 {
		t.Fatalf("send-keys: %d, хочу 1 (только Enter)", n)
	}
}

func TestSendKeysAllowlist(t *testing.T) {
	re := testRegexps(t)
	s := &screenExec{fakeExec: &fakeExec{}, screen: idleScreen, paneSID: "S1"}
	runner := s.run(re)

	ok := proto.New(proto.KindSendKeys)
	ok.CmdID, ok.PaneID, ok.Keys = "c2", "%10", []string{"Escape"}
	r, err := runner.HandleCommand(context.Background(), "default", ok)
	if err != nil || r.Result != ResSent {
		t.Fatalf("Escape (cancel_keys) должен пройти: %+v %v", r, err)
	}

	bad := proto.New(proto.KindSendKeys)
	bad.CmdID, bad.PaneID, bad.Keys = "c3", "%10", []string{"Ctrl+C"}
	r, err = runner.HandleCommand(context.Background(), "default", bad)
	if err != nil {
		t.Fatal(err)
	}
	if r.Result != ResBadKeys {
		t.Fatalf("Ctrl+C вне allowlist: result = %s, хочу BAD_KEYS", r.Result)
	}
	if n := s.callCount("send-keys"); n != 1 {
		t.Fatalf("запрещённая клавиша не должна уходить в tmux: %d вызовов", n)
	}

	empty := proto.New(proto.KindSendKeys)
	empty.CmdID, empty.PaneID = "c4", "%10"
	r, _ = runner.HandleCommand(context.Background(), "default", empty)
	if r.Result != ResBadKeys {
		t.Fatalf("пустые keys: result = %s, хочу BAD_KEYS", r.Result)
	}
}

func TestCaptureLines(t *testing.T) {
	re := testRegexps(t)
	screen := ""
	for i := 0; i < 50; i++ {
		screen += fmt.Sprintf("L%03d\n", i)
	}
	s := &screenExec{fakeExec: &fakeExec{}, screen: screen, paneSID: "S1"}
	runner := s.run(re)
	m := proto.New(proto.KindCapture)
	m.CmdID, m.PaneID, m.Lines = "c5", "%10", 5
	r, err := runner.HandleCommand(context.Background(), "default", m)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(r.Detail, "\n")
	if len(lines) != 5 {
		t.Fatalf("строк: %d, хочу 5", len(lines))
	}
	if lines[0] != "L045" || lines[4] != "L049" {
		t.Fatalf("последние 5 = %v, хочу L045..L049", lines)
	}
}

func TestSetStatusOption(t *testing.T) {
	re := testRegexps(t)
	s := &screenExec{fakeExec: &fakeExec{}, screen: idleScreen, paneSID: "S1"}
	runner := s.run(re)
	m := proto.New(proto.KindSetStatus)
	m.CmdID, m.PaneID, m.Text = "c6", "%10", "runpilot·idle"
	r, err := runner.HandleCommand(context.Background(), "default", m)
	if err != nil {
		t.Fatal(err)
	}
	if r.Result != ResSent {
		t.Fatalf("result = %s", r.Result)
	}
	last := s.lastCall()
	if !strings.Contains(last, "set-option") || !strings.Contains(last, "@runpilot_status") ||
		!strings.HasSuffix(last, "runpilot·idle") {
		t.Fatalf("команда = %q", last)
	}
}

// lastCall — последний tmux-вызов (строкой).
func (s *screenExec) lastCall() string {
	s.fakeExec.mu.Lock()
	defer s.fakeExec.mu.Unlock()
	if len(s.fakeExec.calls) == 0 {
		return ""
	}
	return strings.Join(s.fakeExec.calls[len(s.fakeExec.calls)-1], " ")
}
