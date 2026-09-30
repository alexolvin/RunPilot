package command

import (
	"context"
	"fmt"
	"strings"

	"runpilot/internal/model"
)

// Outcome — результат выполнения команды (один и тот же для веба и Telegram).
// Code — "OK" или код ошибки (для текстов internal/texts); Text —
// человекочитаемый ответ (под строкой у веба, сообщением в Telegram).
type Outcome struct {
	Code    string         `json:"code"`
	Text    string         `json:"text,omitempty"`
	Details map[string]any `json:"details,omitempty"`
}

func ok(text string) Outcome        { return Outcome{Code: "OK", Text: text} }
func err(code, text string) Outcome { return Outcome{Code: code, Text: text} }

// SpawnRequest — /new (POST /api/v1/sessions/spawn, раздел 13.2).
type SpawnRequest struct {
	Host       string
	Dir        string
	Name       string
	Class      model.QueueClass
	HasClass   bool
	Constraint model.Constraint
	Auto       bool
}

// Executor — операции оператора, которые выполняет команда. Реализует
// координатор (web + Telegram — один и тот же Executor). Ввод в панель
// (Paste/Approve/Compress) — только сюда (R6).
type Executor interface {
	// Address — <t>: имя | sid | узел:tmux_session → sid + имя.
	Address(ctx context.Context, t string) (sid, name string, e error)

	// Очередь.
	Enqueue(ctx context.Context, sid string, c model.QueueClass, cons model.Constraint, after string, front bool) (Outcome, error)
	Dequeue(ctx context.Context, sid string) (Outcome, error)
	Requeue(ctx context.Context, sid string, back bool) (Outcome, error)
	RequeueAllHold(ctx context.Context) (Outcome, error)
	ClearQueue(ctx context.Context) (Outcome, error)
	Prio(ctx context.Context, sid string, c model.QueueClass) (Outcome, error)
	SetConstraint(ctx context.Context, sid string, cons model.Constraint) (Outcome, error)
	Hold(ctx context.Context, sid string) (Outcome, error)
	Unhold(ctx context.Context, sid string) (Outcome, error)
	Cancel(ctx context.Context, sid string) (Outcome, error)
	Auto(ctx context.Context, sid string, on bool) (Outcome, error)
	Why(ctx context.Context, sid string) (Outcome, error)

	// Ввод (R6: только по запросу оператора).
	Paste(ctx context.Context, sid, text string, enqueue bool) (Outcome, error)
	Approve(ctx context.Context, sid, option string) (Outcome, error)
	Compress(ctx context.Context, sid string) (Outcome, error)

	// Сессии.
	Spawn(ctx context.Context, req SpawnRequest) (Outcome, error)
	RestartAgent(ctx context.Context, sid string) (Outcome, error)
	KillAgent(ctx context.Context, sid string) (Outcome, error)
	Close(ctx context.Context, sid string) (Outcome, error)
	Restore(ctx context.Context, sid string) (Outcome, error)

	// Навигация (веб — URL карточки/терминала; Telegram — ссылка на панель).
	Open(ctx context.Context, sid string) (Outcome, error)
	Term(ctx context.Context, sid string) (Outcome, error)

	// Управление.
	ServerOp(ctx context.Context, op, name string) (Outcome, error)
	NodeOp(ctx context.Context, op, host string) (Outcome, error)
	Pause(ctx context.Context, on bool) (Outcome, error)
	Panic(ctx context.Context, on bool) (Outcome, error)
}

// target — разбор первого аргумента <t> в sid.
func target(ctx context.Context, ex Executor, args []string) (string, error) {
	if len(args) < 1 || args[0] == "" {
		return "", fmt.Errorf("нужен аргумент <t>")
	}
	sid, _, e := ex.Address(ctx, args[0])
	return sid, e
}

// Run — разобрать и выполнить команду через Executor (веб + Telegram общий
// разбор, раздел 12). Команды с подтверждением (requeue-hold, clear-queue,
// panic) вызывающий вызывает после подтверждения; Run просто выполняет.
func Run(ctx context.Context, p *Parsed, ex Executor) (Outcome, error) {
	a := p.Args
	switch p.Cmd.Name {
	case "help":
		return ok(helpText(a)), nil
	case "pause":
		return ex.Pause(ctx, true)
	case "resume":
		return ex.Pause(ctx, false)
	case "panic":
		return ex.Panic(ctx, true)
	case "unpanic":
		return ex.Panic(ctx, false)
	case "requeue-hold":
		return ex.RequeueAllHold(ctx)
	case "clear-queue":
		return ex.ClearQueue(ctx)

	case "go":
		sid, e := target(ctx, ex, a)
		if e != nil {
			return err("BAD_TARGET", e.Error()), nil
		}
		return ex.Paste(ctx, sid, p.Text, true)
	case "send":
		sid, e := target(ctx, ex, a)
		if e != nil {
			return err("BAD_TARGET", e.Error()), nil
		}
		return ex.Paste(ctx, sid, p.Text, false)

	case "new":
		if len(a) <= 1 {
			return err("ARG", "нужны <узел> и <каталог>"), nil
		}
		req := SpawnRequest{Host: a[0], Dir: a[1]}
		for _, x := range a[two:] {
			if n, found := strings.CutPrefix(x, "name:"); found {
				req.Name = n
				continue
			}
			if c, isCls := model.QueueClassParse(x); isCls {
				req.Class, req.HasClass = c, true
				continue
			}
			if c, isCons := model.ConstraintParse(x); isCons {
				req.Constraint = c
				continue
			}
			if x == "auto" {
				req.Auto = true
			}
		}
		return ex.Spawn(ctx, req)

	case "enqueue":
		sid, e := target(ctx, ex, a)
		if e != nil {
			return err("BAD_TARGET", e.Error()), nil
		}
		var cls model.QueueClass
		cons := model.NoConstraint
		after := ""
		front := false
		for _, x := range a[1:] {
			if c, ok := model.QueueClassParse(x); ok {
				cls = c
				continue
			}
			if r, ok := strings.CutPrefix(x, "after:"); ok {
				after = r
				continue
			}
			if x == "front" {
				front = true
				continue
			}
			if c, ok := model.ConstraintParse(x); ok {
				cons = c
			}
		}
		return ex.Enqueue(ctx, sid, cls, cons, after, front)
	case "dequeue":
		sid, e := target(ctx, ex, a)
		if e != nil {
			return err("BAD_TARGET", e.Error()), nil
		}
		return ex.Dequeue(ctx, sid)
	case "requeue":
		sid, e := target(ctx, ex, a)
		if e != nil {
			return err("BAD_TARGET", e.Error()), nil
		}
		return ex.Requeue(ctx, sid, len(a) > 1 && a[1] == "back")
	case "prio":
		sid, e := target(ctx, ex, a)
		if e != nil {
			return err("BAD_TARGET", e.Error()), nil
		}
		if len(a) <= 1 {
			return err("ARG", "нужен <класс>"), nil
		}
		c, ok := model.QueueClassParse(a[1])
		if !ok {
			return err("BAD_CLASS", "неизвестный класс: "+a[1]), nil
		}
		return ex.Prio(ctx, sid, c)
	case "pin":
		sid, e := target(ctx, ex, a)
		if e != nil {
			return err("BAD_TARGET", e.Error()), nil
		}
		return ex.SetConstraint(ctx, sid, constraintFromKind(model.ConstraintPin, a))
	case "prefer":
		sid, e := target(ctx, ex, a)
		if e != nil {
			return err("BAD_TARGET", e.Error()), nil
		}
		return ex.SetConstraint(ctx, sid, constraintFromKind(model.ConstraintPrefer, a))
	case "unpin":
		sid, e := target(ctx, ex, a)
		if e != nil {
			return err("BAD_TARGET", e.Error()), nil
		}
		return ex.SetConstraint(ctx, sid, model.NoConstraint)
	case "hold":
		sid, e := target(ctx, ex, a)
		if e != nil {
			return err("BAD_TARGET", e.Error()), nil
		}
		return ex.Hold(ctx, sid)
	case "unhold":
		sid, e := target(ctx, ex, a)
		if e != nil {
			return err("BAD_TARGET", e.Error()), nil
		}
		return ex.Unhold(ctx, sid)
	case "cancel":
		sid, e := target(ctx, ex, a)
		if e != nil {
			return err("BAD_TARGET", e.Error()), nil
		}
		return ex.Cancel(ctx, sid)
	case "approve":
		sid, e := target(ctx, ex, a)
		if e != nil {
			return err("BAD_TARGET", e.Error()), nil
		}
		if len(a) <= 1 {
			return err("ARG", "нужен <вариант>"), nil
		}
		return ex.Approve(ctx, sid, a[1])
	case "compress":
		sid, e := target(ctx, ex, a)
		if e != nil {
			return err("BAD_TARGET", e.Error()), nil
		}
		return ex.Compress(ctx, sid)
	case "auto":
		sid, e := target(ctx, ex, a)
		if e != nil {
			return err("BAD_TARGET", e.Error()), nil
		}
		if len(a) <= 1 || (a[1] != "on" && a[1] != "off") {
			return err("ARG", "нужно on|off"), nil
		}
		return ex.Auto(ctx, sid, a[1] == "on")
	case "why":
		sid, e := target(ctx, ex, a)
		if e != nil {
			return err("BAD_TARGET", e.Error()), nil
		}
		return ex.Why(ctx, sid)
	case "open":
		sid, e := target(ctx, ex, a)
		if e != nil {
			return err("BAD_TARGET", e.Error()), nil
		}
		return ex.Open(ctx, sid)
	case "term":
		sid, e := target(ctx, ex, a)
		if e != nil {
			return err("BAD_TARGET", e.Error()), nil
		}
		return ex.Term(ctx, sid)
	case "restart-agent":
		sid, e := target(ctx, ex, a)
		if e != nil {
			return err("BAD_TARGET", e.Error()), nil
		}
		return ex.RestartAgent(ctx, sid)
	case "kill-agent":
		sid, e := target(ctx, ex, a)
		if e != nil {
			return err("BAD_TARGET", e.Error()), nil
		}
		return ex.KillAgent(ctx, sid)
	case "close":
		sid, e := target(ctx, ex, a)
		if e != nil {
			return err("BAD_TARGET", e.Error()), nil
		}
		return ex.Close(ctx, sid)
	case "restore":
		sid, e := target(ctx, ex, a)
		if e != nil {
			return err("BAD_TARGET", e.Error()), nil
		}
		return ex.Restore(ctx, sid)
	case "server":
		if len(a) <= 1 {
			return err("ARG", "нужны <операция> и <S>"), nil
		}
		return ex.ServerOp(ctx, a[0], a[1])
	case "node":
		if len(a) <= 1 {
			return err("ARG", "нужны <операция> и <узел>"), nil
		}
		return ex.NodeOp(ctx, a[0], a[1])
	}
	return err("NO_CMD", "неизвестная команда: "+p.Cmd.Name), nil
}

// constraintFromKind — pin/prefer <t> [S]: сервер — необязательный 2-й аргумент.
func constraintFromKind(kind model.ConstraintKind, a []string) model.Constraint {
	if len(a) > 1 && a[1] != "" {
		return model.Constraint{Kind: kind, Server: a[1]}
	}
	return model.NoConstraint
}
