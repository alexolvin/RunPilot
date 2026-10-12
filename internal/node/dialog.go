package node

import (
	"context"
	"fmt"
	"strings"

	"runpilot/internal/detect"
	"runpilot/internal/proto"
)

// ResDialogMismatch — выделенная строка не совпала с ожидаемой (ТЗ Relay §12.2).
const ResDialogMismatch = "DIALOG_MISMATCH"

// dialogAnswer — шаги диалога вопроса (ТЗ Relay §12.2). Клавиши — только из
// dialog_keys профиля и только в состоянии диалога. После каждого шага —
// снимок: выделенная строка совпадает с ожидаемой (dialog_selected_regex).
// Escape не отправляется: он отменяет ход.
func (c *CommandRunner) dialogAnswer(ctx context.Context, socket string, m proto.Msg) (*Reply, error) {
	info, err := c.paneInfo(ctx, socket, m.PaneID)
	if err != nil || info.SID == "" {
		return &Reply{CmdID: m.CmdID, Result: ResPaneGone}, nil
	}
	if c.profile.DialogRegex == nil {
		return &Reply{CmdID: m.CmdID, Result: ResBadKeys, Detail: "dialog_regex не задан"}, nil
	}
	for i, step := range m.DialogSteps {
		// Проверка состояния диалога перед каждым шагом.
		st, _, _, err := capturePane(ctx, c.ex, c.re, info)
		if err != nil {
			return &Reply{CmdID: m.CmdID, Result: ResPaneGone}, nil
		}
		if st != detect.WaitUI && !c.inDialog(ctx, socket, info) {
			return &Reply{CmdID: m.CmdID, Result: ResDialogMismatch, Detail: fmt.Sprintf("шаг %d: не в диалоге", i)}, nil
		}
		switch {
		case step.Key != "":
			if !inList(c.profile.DialogKeys, step.Key) {
				return &Reply{CmdID: m.CmdID, Result: ResBadKeys, Detail: "клавиша вне dialog_keys: " + step.Key}, nil
			}
			if err := c.sendLiteral(ctx, socket, m.PaneID, step.Key); err != nil {
				return nil, err
			}
		case step.Paste != "":
			if err := c.pasteStep(ctx, socket, m.PaneID, step.Paste); err != nil {
				return nil, err
			}
		default:
			return &Reply{CmdID: m.CmdID, Result: ResBadKeys, Detail: fmt.Sprintf("шаг %d: пусто", i)}, nil
		}
		// Проверка выделенной строки после шага.
		if step.Expected != "" {
			if err := c.sleepDialog(); err != nil {
				return nil, err
			}
			sel, err := c.selectedLine(ctx, socket, info)
			if err != nil {
				return &Reply{CmdID: m.CmdID, Result: ResPaneGone}, nil
			}
			if !strings.Contains(sel, step.Expected) {
				return &Reply{CmdID: m.CmdID, Result: ResDialogMismatch, Detail: fmt.Sprintf("шаг %d: %q ∌ %q", i, sel, step.Expected)}, nil
			}
		}
	}
	return &Reply{CmdID: m.CmdID, Result: ResSent, Detail: fmt.Sprintf("%d steps", len(m.DialogSteps))}, nil
}

// inDialog — на экране диалог вопроса (dialog_regex).
func (c *CommandRunner) inDialog(ctx context.Context, socket string, info PaneInfo) bool {
	out, err := c.ex.Output(ctx, "tmux", tmuxArgs(socket, "capture-pane", "-p", "-J", "-t", info.PaneID)...)
	if err != nil {
		return false
	}
	return c.profile.DialogRegex.MatchString(string(out))
}

// selectedLine — выделенная строка диалога (dialog_selected_regex).
func (c *CommandRunner) selectedLine(ctx context.Context, socket string, info PaneInfo) (string, error) {
	out, err := c.ex.Output(ctx, "tmux", tmuxArgs(socket, "capture-pane", "-p", "-J", "-t", info.PaneID)...)
	if err != nil {
		return "", err
	}
	if c.profile.DialogSelectedRegex == nil {
		return "", nil
	}
	for _, line := range strings.Split(string(out), "\n") {
		if c.profile.DialogSelectedRegex.MatchString(line) {
			return strings.TrimSpace(line), nil
		}
	}
	return "", nil
}

// pasteStep — вставка текста (bracketed paste) в диалоге.
func (c *CommandRunner) pasteStep(ctx context.Context, socket, paneID, text string) error {
	buf := "runpilot-dialog-"
	if _, err := c.ex.OutputInput(ctx, "tmux", text, tmuxArgs(socket, "load-buffer", "-b", buf, "-")...); err != nil {
		return err
	}
	_, err := c.ex.Output(ctx, "tmux", tmuxArgs(socket, "paste-buffer", "-p", "-d", "-b", buf, "-t", paneID)...)
	return err
}

// sleepDialog — короткая пауза перед переснятием (отрисовка).
func (c *CommandRunner) sleepDialog() error {
	c.sleep(dialogSettle)
	return nil
}
