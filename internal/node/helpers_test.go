package node

import (
	"context"
	"strings"
	"sync"
	"testing"

	"runpilot/internal/detect"
	"runpilot/profiles"
)

// fakeExec — подмена tmux/ps: ответы по точным аргументам.
type fakeExec struct {
	mu      sync.Mutex
	calls   [][]string
	respond func(name string, args ...string) (string, error)
}

func (f *fakeExec) Output(_ context.Context, name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	f.calls = append(f.calls, append([]string{name}, args...))
	r := f.respond
	f.mu.Unlock()
	if r == nil {
		return nil, nil
	}
	out, err := r(name, args...)
	return []byte(out), err
}

// OutputInput реализует Execer: записывает вызов (stdin-текст не попадает в
// вызов) и отвечает как Output.
func (f *fakeExec) OutputInput(_ context.Context, name, input string, args ...string) ([]byte, error) {
	_ = input
	return f.Output(context.Background(), name, args...)
}

// callCount — число вызовов команды с подстрокой в аргументах.
func (f *fakeExec) callCount(substr string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if strings.Contains(strings.Join(c, " "), substr) {
			n++
		}
	}
	return n
}

// callContains — есть ли вызов, содержащий ВСЕ подстроки одновременно.
func (f *fakeExec) callContains(substrs ...string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		joined := strings.Join(c, " ")
		ok := true
		for _, s := range substrs {
			if !strings.Contains(joined, s) {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

// testRegexps — детерминированный встроенный профиль qwen.
func testRegexps(t *testing.T) detect.Regexps {
	prof, err := profiles.LoadEmbeddedQwen()
	if err != nil {
		t.Fatal(err)
	}
	re, err := prof.Regexps()
	if err != nil {
		t.Fatal(err)
	}
	return re
}

// idleScreen — минимальный экран IDLE (хром как в фикстурах 0.24.4).
const idleScreen = `────────────────────────────────────────────────
>   Type your message or @path/to/file
────────────────────────────────────────────────
  ➜ /tmp/proj · qwen3.6-27b
`

// idleInputScreen — IDLE с непустым вводом (ZWSP-курсор, как в 0.24.4).
const idleInputScreen = `────────────────────────────────────────────────
> Однострочный тестовый промпт ` + "\u200b" + `
────────────────────────────────────────────────
  ➜ /tmp/proj · qwen3.6-27b
`

// busyScreen — BUSY (маркер «Enter to steer»).
const busyScreen = `────────────────────────────────────────────────
>   Type your message or @path/to/file
────────────────────────────────────────────────
  ➜ /tmp/proj · qwen3.6-27b
  Enter to steer · Ctrl+Q to queue · ⏸ Ask permissions (shift + tab to cycle)
`

// waitUIScreen — WAIT_UI (маркер «Initializing...»).
const waitUIScreen = `────────────────────────────────────────────────
>   Type your message or @path/to/file
────────────────────────────────────────────────
  ➜ /tmp/proj · qwen3.6-27b
  Initializing...
`

// promptScreen — PROMPT (маркер «Waiting for user confirmation», approval_regex).
const promptScreen = `────────────────────────────────────────────────
  ⠏ Waiting for user confirmation...
────────────────────────────────────────────────
  ➜ /tmp/proj · qwen3.6-27b
`
