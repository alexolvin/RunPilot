package node

// PTY-менеджер узла (13.4 ТЗ): реальный tmux attach в псевдотерминале —
// открытие, эхо нажатий, закрытие без утечки (нет zombie, list-clients
// возвращается к исходному, размер окна не меняется).
//
// Тест на реальном tmux (CmdExecer), изолированный сокет на PID.

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"runpilot/internal/clock"
	"runpilot/internal/config"
	"runpilot/internal/proto"
	"runpilot/profiles"
)

// tmuxOut — разовая команда tmux на сокете (для setup/проверок теста).
func tmuxOut(t *testing.T, socket string, args ...string) (string, error) {
	t.Helper()
	full := append([]string{"-L", socket}, args...)
	out, err := exec.Command("tmux", full...).CombinedOutput()
	return string(out), err
}

// newRealNode — узел с реальным exec и соктом socket.
func newRealNode(t *testing.T, socket string) *Node {
	t.Helper()
	cfg := config.Defaults()
	cfg.Node.TmuxSockets = []string{socket}
	prof, err := profiles.LoadEmbeddedQwen()
	if err != nil {
		t.Fatal(err)
	}
	n, err := New(cfg, prof, CmdExecer{}, clock.NewReal(),
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// windowSize — «ширина высота» окна сессии.
func windowSize(t *testing.T, socket, session string) (int, int) {
	t.Helper()
	out, err := tmuxOut(t, socket, "display-message", "-p", "-t", session,
		"-F", "#{window_width} #{window_height}")
	if err != nil {
		t.Fatalf("display-message: %v (%s)", err, out)
	}
	f := strings.Fields(strings.TrimSpace(out))
	if len(f) < 2 {
		t.Fatalf("размер окна: пустой ответ %q", out)
	}
	w, e1 := strconv.Atoi(f[0])
	h, e2 := strconv.Atoi(f[1])
	if e1 != nil || e2 != nil {
		t.Fatalf("размер окна: %q", out)
	}
	return w, h
}

func TestPtyOpenEchoCloseNoLeak(t *testing.T) {
	socket := fmt.Sprintf("runpilotw8t%d", os.Getpid())
	// tmux установлен? (в окружении runpilot — да; без него пропуск).
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("нет tmux")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Сессия с bash-панелью 80x24 на изолированном сокете.
	if out, err := tmuxOut(t, socket, "new-session", "-d", "-s", "w8t",
		"-x", "80", "-y", "24", "bash"); err != nil {
		t.Fatalf("new-session: %v (%s)", err, out)
	}
	t.Cleanup(func() { _, _ = tmuxOut(t, socket, "kill-server") })

	// tmux резервирует строку статуса: контент = 80x23 при -y 24. Точное
	// значение не важно — важно, что оно НЕ меняется (CONTROL W8).
	w0, h0 := windowSize(t, socket, "w8t")
	// list-clients до: 0 клиентов.
	if out, _ := tmuxOut(t, socket, "list-clients", "-t", "w8t"); strings.TrimSpace(out) != "" {
		t.Fatalf("list-clients до: не пуст %q", out)
	}

	n := newRealNode(t, socket)
	// Вывод PTY → буфер; pty_exit → флаг.
	var mu sync.Mutex
	var out []byte
	exitCh := make(chan struct{})
	var exitOnce sync.Once
	n.SetPtySender(func(c int, d []byte) error {
		mu.Lock()
		out = append(out, d...)
		mu.Unlock()
		return nil
	})
	n.sendMsg = func(m proto.Msg) error {
		if m.Type == proto.KindPtyExit {
			exitOnce.Do(func() { close(exitCh) })
		}
		return nil
	}

	reply, err := n.ptyOpen(ctx, proto.Msg{Chan: 1, TmuxSession: "w8t", Socket: socket, CmdID: "c1"})
	if err != nil {
		t.Fatalf("ptyOpen: %v", err)
	}
	if reply.Result != proto.ResPtyOpened {
		t.Fatalf("ptyOpen result=%q detail=%q, хочу PTY_OPENED", reply.Result, reply.Detail)
	}
	// Размер окна после attach (ignore-size) не меняется.
	if w1, h1 := windowSize(t, socket, "w8t"); w1 != w0 || h1 != h0 {
		t.Fatalf("размер окна после attach = %dx%d, был %dx%d", w1, h1, w0, h0)
	}
	// list-clients после attach: 1 клиент (attach-процесс).
	if out, _ := tmuxOut(t, socket, "list-clients", "-t", "w8t"); strings.TrimSpace(out) == "" {
		t.Fatalf("list-clients после attach: пусто, ожидал 1 клиент")
	}

	// Эхо: нажатие клавиш → вывод PTY.
	n.PtyWrite(1, []byte("echo w8echo_TEST_MARKER\n"))
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		has := strings.Contains(string(out), "w8echo_TEST_MARKER")
		mu.Unlock()
		if has {
			break
		}
		if time.Now().After(deadline) {
			mu.Lock()
			t.Fatalf("эхо не пришло за 5 с; вывод: %q", out)
			mu.Unlock()
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Закрытие: pty_close → SIGTERM → ptyReap реапит и снимает канал.
	if r, err := n.ptyClose(proto.Msg{Chan: 1, CmdID: "c2"}); err != nil || r.Result != ResSent {
		t.Fatalf("ptyClose: result=%q err=%v, хочу SENT", r.Result, err)
	}
	// Ожидание pty_exit (процесс attach завершился и реапнут).
	select {
	case <-exitCh:
	case <-time.After(5 * time.Second):
		t.Fatalf("pty_exit не получен за 5 с")
	}
	// Реестр пуст (ptyDrop) — процесс реапнут, нет zombie.
	deadline = time.Now().Add(5 * time.Second)
	for {
		n.ptyMu.Lock()
		left := len(n.ptySes)
		n.ptyMu.Unlock()
		if left == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ptySes не опустел за 5 с: осталось %d", left)
		}
		time.Sleep(20 * time.Millisecond)
	}
	// list-clients снова 0 (attach отстёгнут).
	deadline = time.Now().Add(5 * time.Second)
	for {
		if out, _ := tmuxOut(t, socket, "list-clients", "-t", "w8t"); strings.TrimSpace(out) == "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("list-clients после close не опустел: %q", out)
		}
		time.Sleep(50 * time.Millisecond)
	}
	// Размер окна после close не изменился.
	if w2, h2 := windowSize(t, socket, "w8t"); w2 != w0 || h2 != h0 {
		t.Fatalf("размер окна после close = %dx%d, был %dx%d", w2, h2, w0, h0)
	}
}

// TestPtyOpenWithoutTermEnv — W9 (приёмка на железе): headless-машина — у
// сервисного runpilot-node нет TERM; без явного TERM tmux attach мгновенно
// завершается («open terminal failed: terminal does not support clear») и
// веб-терминал не работает. Регрессия: открытие и эхо работают, когда TERM
// в окружении процесса узла отсутствует.
func TestPtyOpenWithoutTermEnv(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("нет tmux")
	}
	savedTerm, hadTerm := os.LookupEnv("TERM")
	os.Unsetenv("TERM")
	t.Cleanup(func() {
		if hadTerm {
			os.Setenv("TERM", savedTerm)
		}
	})

	socket := fmt.Sprintf("runpilotw9t%d", os.Getpid())
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if out, err := tmuxOut(t, socket, "new-session", "-d", "-s", "w9t",
		"-x", "80", "-y", "24", "bash"); err != nil {
		t.Fatalf("new-session: %v (%s)", err, out)
	}
	t.Cleanup(func() { _, _ = tmuxOut(t, socket, "kill-server") })

	n := newRealNode(t, socket)
	var mu sync.Mutex
	var out []byte
	n.SetPtySender(func(c int, d []byte) error {
		mu.Lock()
		out = append(out, d...)
		mu.Unlock()
		return nil
	})

	reply, err := n.ptyOpen(ctx, proto.Msg{Chan: 1, TmuxSession: "w9t", Socket: socket, CmdID: "c1"})
	if err != nil {
		t.Fatalf("ptyOpen: %v", err)
	}
	if reply.Result != proto.ResPtyOpened {
		t.Fatalf("ptyOpen result=%q detail=%q, хочу PTY_OPENED", reply.Result, reply.Detail)
	}
	// Клиент attach жив (без фикса он тут же завершался).
	if out, _ := tmuxOut(t, socket, "list-clients", "-t", "w9t"); strings.TrimSpace(out) == "" {
		t.Fatal("list-clients: пусто — клиент attach завершился (TERM?)")
	}
	// Эхо нажатий — клиент рендерит.
	n.PtyWrite(1, []byte("echo w9noTERM_MARKER\n"))
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		has := strings.Contains(string(out), "w9noTERM_MARKER")
		mu.Unlock()
		if has {
			break
		}
		if time.Now().After(deadline) {
			mu.Lock()
			t.Fatalf("эхо не пришло за 5 с; вывод: %q", out)
			mu.Unlock()
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Закрытие без утечки.
	if r, err := n.ptyClose(proto.Msg{Chan: 1, CmdID: "c2"}); err != nil || r.Result != ResSent {
		t.Fatalf("ptyClose: result=%q err=%v, хочу SENT", r.Result, err)
	}
	deadline = time.Now().Add(5 * time.Second)
	for {
		n.ptyMu.Lock()
		left := len(n.ptySes)
		n.ptyMu.Unlock()
		if left == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ptySes не опустел за 5 с: осталось %d", left)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// --- чистые пути (без реального tmux) ---

func TestPtyWriteAbsentIsNoop(t *testing.T) {
	n := newRealNode(t, fmt.Sprintf("runpilotw8x%d", os.Getpid()))
	n.PtyWrite(99, []byte("x")) // нет канала — no-op, без паники
}

func TestPtyCloseAbsentNoCrash(t *testing.T) {
	n := newRealNode(t, fmt.Sprintf("runpilotw8x%d", os.Getpid()))
	r, err := n.ptyClose(proto.Msg{Chan: 99, CmdID: "c"})
	if err != nil || r.Result != ResSent {
		t.Fatalf("ptyClose(отсутствующий) = %q err=%v, хочу SENT", r.Result, err)
	}
}

func TestPtyDropIdempotent(t *testing.T) {
	n := newRealNode(t, fmt.Sprintf("runpilotw8x%d", os.Getpid()))
	n.ptyDrop(5)
	n.ptyDrop(5) // повторно — без паники
}
