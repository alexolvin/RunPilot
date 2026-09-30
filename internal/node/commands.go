package node

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"runpilot/internal/detect"
	"runpilot/internal/proto"
)

// Коды ответов узла на команды (разделы 7/8 ТЗ).
const (
	ResSent        = "SENT"
	ResAlreadyBusy = "ALREADY_BUSY"
	ResPaneGone    = "PANE_GONE"
	ResPaneWaitUI  = "PANE_WAIT_UI"
	ResPaneChanged = "PANE_CHANGED"
	ResEmptyInput  = "EMPTY_INPUT"
	ResBadKeys     = "BAD_KEYS"
	ResDrain       = "DRAIN"

	// Ввод в панель (раздел 13.1 ТЗ, v2): совпадают с кодами proto.Res*.
	ResPasted         = "PASTED"
	ResPasteMismatch  = "PASTE_MISMATCH"
	ResPasteSubmitted = "PASTE_SUBMITTED"
	ResApproved       = "APPROVED"
	ResApproveNoEffect = "APPROVE_NO_EFFECT"
)

// errBadKeys — клавиша вне allowlist (раздел 8 ТЗ: узел принимает только
// submit_keys / cancel_keys профиля и литерал resume_text).
type errBadKeys struct{ msg string }

func (e errBadKeys) Error() string { return e.msg }

// CommandRunner — исполнение команд координатора на панели.
// Команды выполняются только для панелей с @runpilot_sid (раздел 13 ТЗ).
type CommandRunner struct {
	ex         Execer
	re         detect.Regexps
	profile    Profile
	resumeWait time.Duration
	sleep      func(time.Duration)
}

// ApprovalOption — вариант ответа на запрос подтверждения (раздел 13.3 ТЗ, v2):
// ID — имя в /approve, Label — подпись, Keys — клавиши (проверяются allowlist).
type ApprovalOption struct {
	ID    string
	Label string
	Keys  []string
}

// Profile — ключи агента из profiles/qwen.yaml для allowlist-проверки.
type Profile struct {
	SubmitKeys []string
	CancelKeys []string
	ResumeText string

	// Ввод и разрешения (разделы 13.1/13.3 ТЗ, v2).
	ApprovalOptions       []ApprovalOption
	CompressText          string
	QuitText              string
	PastePlaceholderRegex string
}

// NewCommandRunner создаёт исполнителя; resumeWait — пауза
// dispatch.resume_key_delay_ms между resume_text и Enter.
func NewCommandRunner(ex Execer, re detect.Regexps, p Profile, resumeWait time.Duration) *CommandRunner {
	return &CommandRunner{ex: ex, re: re, profile: p, resumeWait: resumeWait, sleep: time.Sleep}
}

// Reply — ответ на команду координатора. Cols/Rows — для KindScreen,
// Roots — project_roots узла в ответе list_dirs (поле proto.Msg.ProjectRoots
// существует с v2 — старые узлы просто не заполняют его, omitempty).
type Reply struct {
	CmdID  string
	Result string
	Detail string
	Cols   int
	Rows   int
	Roots  []string
}

func (r Reply) msg() proto.Msg {
	m := proto.New(proto.KindReply)
	m.CmdID, m.Result, m.Detail = r.CmdID, r.Result, r.Detail
	m.Cols, m.Rows = r.Cols, r.Rows
	m.ProjectRoots = r.Roots
	return m
}

// tmuxArgs — глобальный флаг сокета перед подкомандой.
func tmuxArgs(socket string, sub ...string) []string {
	args := []string{}
	if socket != "" {
		args = append(args, "-L", socket)
	}
	return append(args, sub...)
}

// HandleCommand разбирает команду координатора и возвращает ответ
// (nil — если ответа нет, например drain).
func (c *CommandRunner) HandleCommand(ctx context.Context, socket string, m proto.Msg) (*Reply, error) {
	switch m.Type {
	case proto.KindDispatch:
		return c.dispatch(ctx, socket, m)
	case proto.KindSendKeys:
		return c.sendKeys(ctx, socket, m)
	case proto.KindCapture:
		return c.captureLines(ctx, socket, m)
	case proto.KindScreen:
		return c.screen(ctx, socket, m)
	case proto.KindPaste:
		return c.paste(ctx, socket, m)
	case proto.KindApprove:
		return c.approve(ctx, socket, m)
	case proto.KindCompress:
		return c.compress(ctx, socket, m)
	case proto.KindSetStatus:
		return c.setOption(ctx, socket, m, "@runpilot_status")
	case proto.KindSetSummary:
		return c.setOption(ctx, socket, m, "@runpilot_summary")
	case proto.KindDrain:
		return nil, nil
	default:
		return nil, fmt.Errorf("node: неизвестная команда %q", m.Type)
	}
}

// capturePane — снимок + нормализация + классификация (раздел 7 ТЗ).
func capturePane(ctx context.Context, ex Execer, re detect.Regexps, info PaneInfo) (detect.State, uint64, string, error) {
	out, err := ex.Output(ctx, "tmux", tmuxArgs(info.Socket, "capture-pane", "-p", "-J", "-t", info.PaneID)...)
	if err != nil {
		return detect.Unknown, 0, "", err
	}
	norm := detect.Normalize(string(out), re)
	st := detect.Classify(norm, re)
	return st, detect.Hash(norm), detect.ExtractInput(norm, re), nil
}

// dispatch — отправка Enter по разделу 8 ТЗ. Проверки по порядку:
// 1) панель существует и @runpilot_sid совпадает; 2) BUSY; 3) WAIT_UI;
// 4) хеш = expect_hash; 5) submit: ввод не пуст.
func (c *CommandRunner) dispatch(ctx context.Context, socket string, m proto.Msg) (*Reply, error) {
	gone := func() (*Reply, error) {
		return &Reply{CmdID: m.CmdID, Result: ResPaneGone}, nil
	}
	info, err := c.paneInfo(ctx, socket, m.PaneID)
	if err != nil {
		return gone()
	}
	if info.SID != m.SID {
		return gone()
	}
	st, hash, input, err := capturePane(ctx, c.ex, c.re, info)
	if err != nil {
		return gone()
	}
	switch {
	case st == detect.Busy:
		return &Reply{CmdID: m.CmdID, Result: ResAlreadyBusy}, nil
	case st == detect.WaitUI:
		return &Reply{CmdID: m.CmdID, Result: ResPaneWaitUI}, nil
	case hash != m.ExpectHash:
		return &Reply{CmdID: m.CmdID, Result: ResPaneChanged}, nil
	}
	switch m.Mode {
	case "submit":
		if strings.TrimSpace(input) == "" {
			return &Reply{CmdID: m.CmdID, Result: ResEmptyInput}, nil
		}
		if err := c.sendLiteral(ctx, socket, m.PaneID, c.profile.SubmitKeys[0]); err != nil {
			return nil, err
		}
	case "resume":
		// Непустой ввод оператора имеет приоритет: отправляется только Enter.
		if strings.TrimSpace(input) != "" {
			if err := c.sendLiteral(ctx, socket, m.PaneID, c.profile.SubmitKeys[0]); err != nil {
				return nil, err
			}
			break
		}
		if err := c.sendText(ctx, socket, m.PaneID, m.ResumeText); err != nil {
			return nil, err
		}
		c.sleep(c.resumeWait)
		if err := c.sendLiteral(ctx, socket, m.PaneID, c.profile.SubmitKeys[0]); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("node: неизвестный режим dispatch %q", m.Mode)
	}
	return &Reply{CmdID: m.CmdID, Result: ResSent, Detail: m.Mode}, nil
}

// sendKeys — произвольные клавиши; допустимы только submit_keys /
// cancel_keys профиля и литерал resume_text (раздел 8 ТЗ).
func (c *CommandRunner) sendKeys(ctx context.Context, socket string, m proto.Msg) (*Reply, error) {
	if len(m.Keys) == 0 {
		return &Reply{CmdID: m.CmdID, Result: ResBadKeys, Detail: "keys пуст"}, nil
	}
	info, err := c.paneInfo(ctx, socket, m.PaneID)
	if err != nil || info.SID == "" {
		return &Reply{CmdID: m.CmdID, Result: ResPaneGone}, nil
	}
	for _, k := range m.Keys {
		err := c.sendOne(ctx, socket, m.PaneID, k)
		if err != nil {
			var bke errBadKeys
			if errors.As(err, &bke) {
				return &Reply{CmdID: m.CmdID, Result: ResBadKeys, Detail: err.Error()}, nil
			}
			return nil, err
		}
	}
	return &Reply{CmdID: m.CmdID, Result: ResSent, Detail: fmt.Sprintf("%d keys", len(m.Keys))}, nil
}

// sendOne — одна клавиша: имя ключа tmux из allowlist либо resume_text.
func (c *CommandRunner) sendOne(ctx context.Context, socket, paneID, key string) error {
	if key == c.profile.ResumeText {
		return c.sendText(ctx, socket, paneID, key)
	}
	if inList(c.profile.SubmitKeys, key) || inList(c.profile.CancelKeys, key) {
		return c.sendLiteral(ctx, socket, paneID, key)
	}
	return errBadKeys{msg: "клавиша вне submit_keys/cancel_keys: " + key}
}

func inList(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// sendLiteral — `tmux send-keys -t <pane> <ключ>`.
func (c *CommandRunner) sendLiteral(ctx context.Context, socket, paneID, key string) error {
	_, err := c.ex.Output(ctx, "tmux", tmuxArgs(socket, "send-keys", "-t", paneID, key)...)
	return err
}

// sendText — `tmux send-keys -t <pane> -l '<текст>'` (побайто).
func (c *CommandRunner) sendText(ctx context.Context, socket, paneID, text string) error {
	_, err := c.ex.Output(ctx, "tmux", tmuxArgs(socket, "send-keys", "-t", paneID, "-l", text)...)
	return err
}

// captureLines — последние N строк экрана (peek, /peek Telegram).
func (c *CommandRunner) captureLines(ctx context.Context, socket string, m proto.Msg) (*Reply, error) {
	out, err := c.ex.Output(ctx, "tmux", tmuxArgs(socket, "capture-pane", "-p", "-J", "-t", m.PaneID)...)
	if err != nil {
		return &Reply{CmdID: m.CmdID, Result: ResPaneGone}, nil
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if m.Lines > 0 && len(lines) > m.Lines {
		lines = lines[len(lines)-m.Lines:]
	}
	return &Reply{CmdID: m.CmdID, Result: ResSent, Detail: strings.Join(lines, "\n")}, nil
}

// screen — ANSI-снимок панели для живого экрана (раздел 15.4 ТЗ, v2):
// `tmux capture-pane -e -p -J -t <pane>` (с ANSI) + размеры окна.
func (c *CommandRunner) screen(ctx context.Context, socket string, m proto.Msg) (*Reply, error) {
	ansiOut, err := c.ex.Output(ctx, "tmux", tmuxArgs(socket, "capture-pane", "-e", "-p", "-J", "-t", m.PaneID)...)
	if err != nil {
		return &Reply{CmdID: m.CmdID, Result: ResPaneGone}, nil
	}
	cols, rows := 0, 0
	if dims, derr := c.ex.Output(ctx, "tmux", tmuxArgs(socket, "display-message", "-p", "-t", m.PaneID, "#{pane_width} #{pane_height}")...); derr == nil {
		parts := strings.Fields(string(dims))
		if len(parts) == dimsFields {
			if v, e := strconv.Atoi(parts[0]); e == nil {
				cols = v
			}
			if v, e := strconv.Atoi(parts[1]); e == nil {
				rows = v
			}
		}
	}
	return &Reply{CmdID: m.CmdID, Result: ResSent, Detail: string(ansiOut), Cols: cols, Rows: rows}, nil
}

// setOption — `tmux set-option -p -t <pane> <опция> <текст>`.
func (c *CommandRunner) setOption(ctx context.Context, socket string, m proto.Msg, opt string) (*Reply, error) {
	_, err := c.ex.Output(ctx, "tmux", tmuxArgs(socket, "set-option", "-p", "-t", m.PaneID, opt, m.Text)...)
	if err != nil {
		return &Reply{CmdID: m.CmdID, Result: ResPaneGone}, nil
	}
	return &Reply{CmdID: m.CmdID, Result: ResSent, Detail: opt}, nil
}

// paneInfo — одна строка list-panes для целевой панели.
func (c *CommandRunner) paneInfo(ctx context.Context, socket, paneID string) (PaneInfo, error) {
	args := tmuxArgs(socket, "list-panes", "-t", paneID, "-F", paneFormat)
	out, err := c.ex.Output(ctx, "tmux", args...)
	if err != nil {
		return PaneInfo{}, err
	}
	panes := parsePanes(socket, out, &procTable{}, nil)
	if len(panes) == 0 {
		return PaneInfo{}, fmt.Errorf("панель %s не найдена", paneID)
	}
	return panes[0], nil
}
