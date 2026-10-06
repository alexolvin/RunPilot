package node

import (
	"context"
	"strings"
	"testing"
)

// TestDispatchLaunchReplay — dispatch с «командой для запуска» (submit): заряд
// (текст + клавиши) воспроизводится по порядку: текст → paste-buffer, клавиши →
// send-keys. Ввод в панели заранее не нужен (экран IDLE с пустым вводом).
func TestDispatchLaunchReplay(t *testing.T) {
	re := testRegexps(t)
	s := &screenExec{fakeExec: &fakeExec{}, screen: idleScreen, paneSID: "S1"}
	runner := s.run(re)
	m := dispatchMsg("submit", s.normHash(re))
	m.LaunchCommand = "запусти тесты[Up][Enter]"
	r, err := runner.HandleCommand(context.Background(), "default", m)
	if err != nil {
		t.Fatal(err)
	}
	if r.Result != ResSent {
		t.Fatalf("result = %s, хочу SENT", r.Result)
	}
	// Текст — через paste-buffer (load-buffer stdin + paste-buffer -p -d),
	// буфер по панели runpilot-launch-%10.
	if n := s.callCount("load-buffer"); n != 1 {
		t.Fatalf("load-buffer: %d, хочу 1", n)
	}
	if !strings.Contains(strings.Join(lastArgs(s, "load-buffer"), " "), "runpilot-launch-%10") {
		t.Fatalf("load-buffer буфер = %q, хочу runpilot-launch-%%10", lastArgs(s, "load-buffer"))
	}
	if n := s.callCount("paste-buffer"); n != 1 ||
		!strings.Contains(strings.Join(lastArgs(s, "paste-buffer"), " "), "-p -d") {
		t.Fatalf("paste-buffer: %d, хочу 1 с -p -d", s.callCount("paste-buffer"))
	}
	// Клавиши — send-keys (без -l), по порядку: Up затем Enter.
	var keys []string
	s.fakeExec.mu.Lock()
	for _, c := range s.fakeExec.calls {
		joined := strings.Join(c, " ")
		if c[0] == "tmux" && strings.Contains(joined, "send-keys") && !strings.Contains(joined, "-l") {
			keys = append(keys, c[len(c)-1])
		}
	}
	s.fakeExec.mu.Unlock()
	if len(keys) != 2 || keys[0] != "Up" || keys[1] != "Enter" {
		t.Fatalf("клавиши = %v, хочу [Up Enter]", keys)
	}
}

// TestDispatchLaunchJustEnter — пустая «команда для запуска» = только Enter
// (пустой скрипт после EnsureTrailingEnter = "[Enter]").
func TestDispatchLaunchJustEnter(t *testing.T) {
	re := testRegexps(t)
	s := &screenExec{fakeExec: &fakeExec{}, screen: idleScreen, paneSID: "S1"}
	runner := s.run(re)
	m := dispatchMsg("submit", s.normHash(re))
	m.LaunchCommand = "[Enter]"
	r, err := runner.HandleCommand(context.Background(), "default", m)
	if err != nil {
		t.Fatal(err)
	}
	if r.Result != ResSent {
		t.Fatalf("result = %s, хочу SENT", r.Result)
	}
	// Только одна клавиша (Enter), без вставки текста.
	if n := s.callCount("load-buffer"); n != 0 {
		t.Fatalf("load-buffer: %d, хочу 0 (нет текста)", n)
	}
	last := s.lastCall()
	if !strings.Contains(last, "send-keys") || !strings.HasSuffix(last, "Enter") {
		t.Fatalf("команда = %q, хочу send-keys … Enter", last)
	}
}

// TestDispatchSubmitEmptyCommandUnchanged — без «команды для запуска» (пусто)
// работает старое поведение: пустой ввод → EMPTY_INPUT.
func TestDispatchSubmitEmptyCommandUnchanged(t *testing.T) {
	re := testRegexps(t)
	s := &screenExec{fakeExec: &fakeExec{}, screen: idleScreen, paneSID: "S1"}
	runner := s.run(re)
	m := dispatchMsg("submit", s.normHash(re))
	// LaunchCommand не задан → старая логика (ввод пуст → EMPTY_INPUT).
	r, err := runner.HandleCommand(context.Background(), "default", m)
	if err != nil {
		t.Fatal(err)
	}
	if r.Result != ResEmptyInput {
		t.Fatalf("result = %s, хочу EMPTY_INPUT", r.Result)
	}
}
