// Package pty — PTY-подключение терминала к tmux-сессии (раздел 13.4 ТЗ).
//
// Узел запускает tmux attach-session в псевдотерминале размером «окно tmux +
// строка статуса». Флаг ignore-size не меняет размер окна, поэтому экран
// детектора не перерисовывается. Закрытие — завершение процесса attach
// (SIGTERM = штатный detach); tmux-сессия не затрагивается.
package pty

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"github.com/creack/pty"
)

// Execer — выполнение tmux без оболочки (тот же интерфейс, что в узле).
type Execer interface {
	Output(ctx context.Context, name string, args ...string) ([]byte, error)
}

// Session — один PTY-канал: мастер псевдотерминала (поток терминала) и
// процесс tmux attach.
type Session struct {
	Master io.ReadWriteCloser
	Cmd    *exec.Cmd
}

// Open — tmux attach к сессии в PTY размером «окно + строка статуса».
// Возвращает PTY-мастер (чтение/запись = данные терминала) и процесс attach.
func Open(ctx context.Context, ex Execer, socket, session string) (*Session, error) {
	cols, rows, err := windowSize(ctx, ex, socket, session)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command("tmux", tmuxArgs(socket,
		"attach-session", "-f", "ignore-size,active-pane", "-t", session)...)
	// W9 (приёмка на железе): headless-машина — у сервисного процесса runpilot-node
	// нет TERM (systemd user, Environment=PATH=…). Без TERM tmux attach
	// мгновенно завершается: «open terminal failed: terminal does not
	// support clear». Терминал клиента attach фиксируем явно.
	cmd.Env = envWithTerm(os.Environ())
	master, err := pty.StartWithSize(cmd, &pty.Winsize{
		Rows: uint16(rows + 1), Cols: uint16(cols),
	})
	if err != nil {
		return nil, fmt.Errorf("pty: tmux attach: %w", err)
	}
	return &Session{Master: master, Cmd: cmd}, nil
}

// Close — завершить tmux attach: SIGTERM (штатный detach) + закрытие мастера;
// tmux-сессия не затрагивается (раздел 13.4 ТЗ). Реап (Wait) и SIGKILL-
// страховку ведёт владелец (PTY-менеджер узла) — здесь только сигнал.
func (s *Session) Close() {
	if s.Cmd != nil && s.Cmd.Process != nil {
		_ = s.Cmd.Process.Signal(syscall.SIGTERM)
	}
	if s.Master != nil {
		_ = s.Master.Close()
	}
}

// windowSize — размер окна tmux (width × height) через display-message.
func windowSize(ctx context.Context, ex Execer, socket, session string) (int, int, error) {
	ctx, cancel := context.WithTimeout(ctx, sizeTimeout)
	defer cancel()
	out, err := ex.Output(ctx, "tmux", tmuxArgs(socket,
		"display-message", "-p", "-t", session, "-F", "#{window_width} #{window_height}")...)
	if err != nil {
		return 0, 0, fmt.Errorf("pty: размер окна: %w", err)
	}
	f := strings.Fields(string(out))
	if len(f) < sizeFields {
		return 0, 0, fmt.Errorf("pty: размер окна: пустой ответ %q", strings.TrimSpace(string(out)))
	}
	cols, err1 := strconv.Atoi(f[0])
	rows, err2 := strconv.Atoi(f[1])
	if err1 != nil || err2 != nil {
		return 0, 0, fmt.Errorf("pty: размер окна: %q", strings.TrimSpace(string(out)))
	}
	return cols, rows, nil
}

// tmuxArgs — аргументы tmux с сокетом (-L <socket>), если сокет задан.
func tmuxArgs(socket string, args ...string) []string {
	if socket != "" {
		return append([]string{"-L", socket}, args...)
	}
	return args
}

// envWithTerm — окружение с TERM, зафиксированным в screen-256color.
// Наследованное TERM (если есть) заменяется, чтобы поведение не зависело от
// того, запущен ли узел из логина (TERM есть) или из systemd (TERM нет).
// screen-256color — терминал tmux-клиента с 256 цветами (поддерживает clear,
// который требует tmux; базовый screen — только 8 цветов).
func envWithTerm(env []string) []string {
	for i, kv := range env {
		if strings.HasPrefix(kv, "TERM=") {
			env[i] = "TERM=screen-256color"
			return env
		}
	}
	return append(env, "TERM=screen-256color")
}
