// Ввод в панель (раздел 13.1 ТЗ, v2): вставка задания и сверка извлечённого
// ввода. Только по запросу оператора (R6); текст никуда не сохраняется (R5).
package node

import (
	"context"
	"regexp"
	"strings"
	"unicode"

	"runpilot/internal/detect"
	"runpilot/internal/proto"
)

// paste — `tmux load-buffer -b runpilot-<cmd_id> -` (текст через stdin) затем
// `tmux paste-buffer -p -d -b runpilot-<cmd_id> -t <pane>`. Узел переснимает
// панель и сверяет извлечённый ввод с текстом: PASTED / PASTE_MISMATCH /
// PASTE_SUBMITTED (панель ушла в BUSY — критический дефект).
func (c *CommandRunner) paste(ctx context.Context, socket string, m proto.Msg) (*Reply, error) {
	gone := func() (*Reply, error) {
		return &Reply{CmdID: m.CmdID, Result: ResPaneGone}, nil
	}
	info, err := c.paneInfo(ctx, socket, m.PaneID)
	if err != nil {
		return gone()
	}
	buf := "runpilot-" + m.CmdID
	if _, err := c.ex.OutputInput(ctx, "tmux", m.Text, tmuxArgs(socket, "load-buffer", "-b", buf, "-")...); err != nil {
		return nil, err
	}
	if _, err := c.ex.Output(ctx, "tmux", tmuxArgs(socket, "paste-buffer", "-p", "-d", "-b", buf, "-t", m.PaneID)...); err != nil {
		return nil, err
	}
	if m.SubmitAfter && len(c.profile.SubmitKeys) > 0 {
		if err := c.sendLiteral(ctx, socket, m.PaneID, c.profile.SubmitKeys[0]); err != nil {
			return nil, err
		}
	}
	// Переснимать, пока вставка не отрисовалась (короткие паузы).
	for i := 0; i < pastePoll; i++ {
		c.sleep(pasteSettle)
		st, _, input, err := capturePane(ctx, c.ex, c.re, info)
		if err != nil {
			return gone()
		}
		if st == detect.Busy {
			return &Reply{CmdID: m.CmdID, Result: ResPasteSubmitted}, nil
		}
		if pasteMatches(c.profile.PastePlaceholderRegex, input, m.Text) {
			return &Reply{CmdID: m.CmdID, Result: ResPasted}, nil
		}
	}
	// Не совпало за бюджет — вернуть извлечённый ввод для PASTE_MISMATCH.
	st, _, input, err := capturePane(ctx, c.ex, c.re, info)
	if err != nil {
		return gone()
	}
	if st == detect.Busy {
		return &Reply{CmdID: m.CmdID, Result: ResPasteSubmitted}, nil
	}
	return &Reply{CmdID: m.CmdID, Result: ResPasteMismatch, Detail: previewRunes(input)}, nil
}

// pasteMatches — сверка извлечённого ввода с вставленным текстом (нормализация
// раздела 7): краткий — совпадение по trim; перенос в узком терминале —
// сравнение без пробельных символов (Qwen рендерит continuation отдельной
// строкой с отступом, capture-pane -J жёсткий перенос не склеивает); длинный
// сворачивается в плейсхолдер (сравнение по числу строк ведёт координатор;
// узел принимает плейсхолдер).
func pasteMatches(placeholderRe, input, text string) bool {
	in := strings.TrimSpace(input)
	want := strings.TrimSpace(text)
	if in == want {
		return true
	}
	if noSpace(in) == noSpace(want) {
		return true
	}
	if placeholderRe != "" {
		if re, err := regexp.Compile(placeholderRe); err == nil && re.MatchString(in) {
			return true
		}
	}
	return false
}

// noSpace — текст без пробельных символов: сверка вставки нечувствительна к
// переносам строк и отступам терминального рендера.
func noSpace(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if !unicode.IsSpace(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// previewRunes — первые N символов (для PASTE_MISMATCH, раздел 13.1 ТЗ).
func previewRunes(s string) string {
	r := []rune(s)
	if len(r) > inputPreviewLen {
		return string(r[:inputPreviewLen])
	}
	return s
}

// approve — ответ на запрос подтверждения (раздел 13.3 ТЗ, v2): только в
// PROMPT, клавиши варианта из профиля (доверенные, из фикстуры). Панель
// должна выйти из PROMPT за бюджет, иначе APPROVE_NO_EFFECT.
func (c *CommandRunner) approve(ctx context.Context, socket string, m proto.Msg) (*Reply, error) {
	gone := func() (*Reply, error) {
		return &Reply{CmdID: m.CmdID, Result: ResPaneGone}, nil
	}
	info, err := c.paneInfo(ctx, socket, m.PaneID)
	if err != nil {
		return gone()
	}
	st, _, _, err := capturePane(ctx, c.ex, c.re, info)
	if err != nil {
		return gone()
	}
	if st != detect.Prompt {
		return &Reply{CmdID: m.CmdID, Result: ResApproveNoEffect, Detail: "панель не в PROMPT"}, nil
	}
	var keys []string
	for _, o := range c.profile.ApprovalOptions {
		if o.ID == m.Text {
			keys = o.Keys
			break
		}
	}
	if keys == nil {
		return &Reply{CmdID: m.CmdID, Result: ResApproveNoEffect, Detail: "неизвестный вариант"}, nil
	}
	for _, k := range keys {
		if err := c.sendLiteral(ctx, socket, m.PaneID, k); err != nil {
			return nil, err
		}
	}
	for i := 0; i < pastePoll; i++ {
		c.sleep(pasteSettle)
		st, _, _, err := capturePane(ctx, c.ex, c.re, info)
		if err != nil {
			return gone()
		}
		if st != detect.Prompt {
			return &Reply{CmdID: m.CmdID, Result: ResApproved}, nil
		}
	}
	return &Reply{CmdID: m.CmdID, Result: ResApproveNoEffect}, nil
}

// compress — сжатие контекста (13.1): узел вставляет compress_text из профиля
// + submit. compress_text доверенный (из фикстуры), ввод обязан быть пуст.
func (c *CommandRunner) compress(ctx context.Context, socket string, m proto.Msg) (*Reply, error) {
	gone := func() (*Reply, error) {
		return &Reply{CmdID: m.CmdID, Result: ResPaneGone}, nil
	}
	if c.profile.CompressText == "" {
		return &Reply{CmdID: m.CmdID, Result: ResApproveNoEffect, Detail: "нет compress_text в профиле"}, nil
	}
	info, err := c.paneInfo(ctx, socket, m.PaneID)
	if err != nil {
		return gone()
	}
	st, _, input, err := capturePane(ctx, c.ex, c.re, info)
	if err != nil {
		return gone()
	}
	if st != detect.Idle || strings.TrimSpace(input) != "" {
		return &Reply{CmdID: m.CmdID, Result: ResApproveNoEffect, Detail: "панель не IDLE или ввод не пуст"}, nil
	}
	buf := "runpilot-" + m.CmdID
	if _, err := c.ex.OutputInput(ctx, "tmux", c.profile.CompressText, tmuxArgs(socket, "load-buffer", "-b", buf, "-")...); err != nil {
		return nil, err
	}
	if _, err := c.ex.Output(ctx, "tmux", tmuxArgs(socket, "paste-buffer", "-p", "-d", "-b", buf, "-t", m.PaneID)...); err != nil {
		return nil, err
	}
	if len(c.profile.SubmitKeys) > 0 {
		if err := c.sendLiteral(ctx, socket, m.PaneID, c.profile.SubmitKeys[0]); err != nil {
			return nil, err
		}
	}
	return &Reply{CmdID: m.CmdID, Result: ResSent, Detail: "compress"}, nil
}
