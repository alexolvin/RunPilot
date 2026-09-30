// apiExecutor — реализация command.Executor координатором (раздел 12 v2):
// веб и Telegram делят один и тот же разбор/выполнение. Очередь/сессии/управление
// — существующие методы планировщика; ввод (Paste/Approve/Compress) — только
// по запросу оператора (R6).
package api

import (
	"context"
	"strconv"

	"runpilot/internal/command"
	"runpilot/internal/model"
)

// apiExecutor — обёртка над *Server.
type apiExecutor struct{ s *Server }

// newExecutor — Executor для command.Run.
func (s *Server) newExecutor() *apiExecutor { return &apiExecutor{s: s} }

// outErr — результат-ошибка (ошибка-значение, не error).
func outErr(code, text string) command.Outcome {
	return command.Outcome{Code: code, Text: text}
}

// unimpl — операция ещё не реализована в этом инкременте (добавляется далее).
func unimpl(op string) (command.Outcome, error) {
	return outErr("NOT_IMPLEMENTED", op+": не реализовано (следующий инкремент W5)"), nil
}

// Address — <t>: имя | sid → sid + имя.
func (e *apiExecutor) Address(_ context.Context, t string) (string, string, error) {
	if r, err := e.s.store.GetSession(t); err == nil {
		return r.SID, r.Name, nil
	}
	r, err := e.s.store.FindLiveByName(t)
	if err != nil {
		return "", "", err
	}
	return r.SID, r.Name, nil
}

// --- Очередь / сессии (→ планировщик) ---

func (e *apiExecutor) Enqueue(_ context.Context, sid string, c model.QueueClass, cons model.Constraint, after string, front bool) (command.Outcome, error) {
	eq := model.QueueEntry{SID: sid, Class: c, Constraint: cons, AfterSID: after}
	if err := e.s.sched.Enqueue(sid, eq, front); err != nil {
		return outErr("ENQUEUE", err.Error()), nil
	}
	return command.Outcome{Code: "OK", Text: "в очереди"}, nil
}

func (e *apiExecutor) Dequeue(_ context.Context, sid string) (command.Outcome, error) {
	if err := e.s.sched.Dequeue(sid); err != nil {
		return outErr("DEQUEUE", err.Error()), nil
	}
	return command.Outcome{Code: "OK", Text: "снято с очереди"}, nil
}

func (e *apiExecutor) Requeue(_ context.Context, sid string, back bool) (command.Outcome, error) {
	if err := e.s.sched.Requeue(sid, back); err != nil {
		return outErr("REQUEUE", err.Error()), nil
	}
	return command.Outcome{Code: "OK", Text: "переставлено в очередь"}, nil
}

// RequeueAllHold — «Вернуть все» (O5): все сессии, требующие внимания (HOLD),
// возвращаются в очередь; отмена в течение web.undo_sec (снова в HOLD).
func (e *apiExecutor) RequeueAllHold(_ context.Context) (command.Outcome, error) {
	if e.s.sched == nil {
		return outErr("NO_SCHEDULER", "планировщик не запущен"), nil
	}
	holds, err := e.s.holdSIDs()
	if err != nil {
		return outErr("HOLD", err.Error()), nil
	}
	for _, sid := range holds {
		_ = e.s.sched.Requeue(sid, true)
	}
	e.s.storeUndo("requeue-hold", nil, holds)
	return command.Outcome{Code: "OK", Text: "вернуто в очередь: " + strconv.Itoa(len(holds)),
		Details: map[string]any{"requeued": len(holds), "undo": len(holds) > 0}}, nil
}

// ClearQueue — «Очистить очередь» (O5): снять все записи; отмена в течение
// web.undo_sec возвращает очередь в исходный порядок (J12).
func (e *apiExecutor) ClearQueue(_ context.Context) (command.Outcome, error) {
	if e.s.sched == nil {
		return outErr("NO_SCHEDULER", "планировщик не запущен"), nil
	}
	entries, err := e.s.sched.ClearQueueAll()
	if err != nil {
		return outErr("CLEAR", err.Error()), nil
	}
	e.s.storeUndo("clear", entries, nil)
	return command.Outcome{Code: "OK", Text: "очередь очищена: " + strconv.Itoa(len(entries)),
		Details: map[string]any{"removed": len(entries), "undo": len(entries) > 0}}, nil
}

func (e *apiExecutor) Prio(_ context.Context, sid string, c model.QueueClass) (command.Outcome, error) {
	if err := e.s.sched.Prio(sid, c); err != nil {
		return outErr("PRIO", err.Error()), nil
	}
	return command.Outcome{Code: "OK", Text: "класс обновлён"}, nil
}

func (e *apiExecutor) SetConstraint(_ context.Context, sid string, cons model.Constraint) (command.Outcome, error) {
	var err error
	switch cons.Kind {
	case model.ConstraintPin:
		err = e.s.sched.Pin(sid, cons.Server)
	case model.ConstraintPrefer:
		err = e.s.sched.Prefer(sid, cons.Server)
	default:
		err = e.s.sched.Unpin(sid)
	}
	if err != nil {
		return outErr("CONSTRAINT", err.Error()), nil
	}
	return command.Outcome{Code: "OK", Text: "ограничение: " + constraintLabel(cons)}, nil
}

func (e *apiExecutor) Hold(_ context.Context, sid string) (command.Outcome, error) {
	if err := e.s.sched.Hold(sid); err != nil {
		return outErr("HOLD", err.Error()), nil
	}
	return command.Outcome{Code: "OK", Text: "удержание"}, nil
}

func (e *apiExecutor) Unhold(_ context.Context, sid string) (command.Outcome, error) {
	if err := e.s.sched.Unhold(sid); err != nil {
		return outErr("UNHOLD", err.Error()), nil
	}
	return command.Outcome{Code: "OK", Text: "удержание снято"}, nil
}

func (e *apiExecutor) Cancel(_ context.Context, sid string) (command.Outcome, error) {
	if err := e.s.sched.Cancel(sid); err != nil {
		return outErr("CANCEL", err.Error()), nil
	}
	return command.Outcome{Code: "OK", Text: "отменено"}, nil
}

func (e *apiExecutor) Auto(_ context.Context, sid string, on bool) (command.Outcome, error) {
	if err := e.s.sched.SetAutoEnqueue(sid, on); err != nil {
		return outErr("AUTO", err.Error()), nil
	}
	st := "выкл"
	if on {
		st = "вкл"
	}
	return command.Outcome{Code: "OK", Text: "авто-очередь: " + st}, nil
}

func (e *apiExecutor) Why(_ context.Context, sid string) (command.Outcome, error) {
	w, err := e.s.sched.Why(sid)
	if err != nil {
		return outErr("WHY", err.Error()), nil
	}
	text := "получает слот"
	if w.Reason != "" {
		text = w.Reason
	}
	out := command.Outcome{Code: "OK", Text: text, Details: map[string]any{
		"state": w.State, "candidates": w.Candidates,
	}}
	return out, nil
}

// --- Ввод (R6: только по запросу оператора) ---

func (e *apiExecutor) Paste(ctx context.Context, sid, text string, enqueue bool) (command.Outcome, error) {
	return e.s.CmdPaste(ctx, sid, text, enqueue)
}
func (e *apiExecutor) Approve(ctx context.Context, sid, option string) (command.Outcome, error) {
	return e.s.CmdApprove(ctx, sid, option)
}
func (e *apiExecutor) Compress(ctx context.Context, sid string) (command.Outcome, error) {
	return e.s.CmdCompress(ctx, sid)
}

// --- Сессии (agent-операции — следующий инкремент W5) ---

func (e *apiExecutor) Spawn(context.Context, command.SpawnRequest) (command.Outcome, error) {
	return unimpl("new")
}
func (e *apiExecutor) RestartAgent(context.Context, string) (command.Outcome, error) {
	return unimpl("restart-agent")
}
func (e *apiExecutor) KillAgent(context.Context, string) (command.Outcome, error) {
	return unimpl("kill-agent")
}
func (e *apiExecutor) Close(ctx context.Context, sid string) (command.Outcome, error) {
	return e.s.CmdClose(ctx, sid)
}
func (e *apiExecutor) Restore(context.Context, string) (command.Outcome, error) {
	return unimpl("restore")
}

// --- Навигация (веб — URL карточки/терминала) ---

func (e *apiExecutor) Open(_ context.Context, sid string) (command.Outcome, error) {
	return command.Outcome{Code: "OK", Text: "карточка сессии", Details: map[string]any{"url": "/sessions/" + sid}}, nil
}
func (e *apiExecutor) Term(_ context.Context, sid string) (command.Outcome, error) {
	return command.Outcome{Code: "OK", Text: "терминал сессии", Details: map[string]any{"url": "/terminals/" + sid}}, nil
}

// --- Управление ---

func (e *apiExecutor) ServerOp(_ context.Context, op, name string) (command.Outcome, error) {
	switch op {
	case "drain":
		if e.s.serverDrain != nil {
			if err := e.s.serverDrain(name, true); err != nil {
				return outErr("SERVER", err.Error()), nil
			}
			return command.Outcome{Code: "OK", Text: "сервер в drain: " + name}, nil
		}
	case "undrain":
		if e.s.serverDrain != nil {
			if err := e.s.serverDrain(name, false); err != nil {
				return outErr("SERVER", err.Error()), nil
			}
			return command.Outcome{Code: "OK", Text: "drain снят: " + name}, nil
		}
	}
	return unimpl("server " + op)
}

func (e *apiExecutor) NodeOp(_ context.Context, op, host string) (command.Outcome, error) {
	switch op {
	case "drain":
		e.s.sched.NodeDrain(host, true)
		return command.Outcome{Code: "OK", Text: "узел в drain: " + host}, nil
	case "undrain":
		e.s.sched.NodeDrain(host, false)
		return command.Outcome{Code: "OK", Text: "drain снят: " + host}, nil
	}
	return unimpl("node " + op)
}

func (e *apiExecutor) Pause(_ context.Context, on bool) (command.Outcome, error) {
	e.s.sched.Pause(on)
	if on {
		return command.Outcome{Code: "OK", Text: "пауза"}, nil
	}
	return command.Outcome{Code: "OK", Text: "работает"}, nil
}

func (e *apiExecutor) Panic(context.Context, bool) (command.Outcome, error) { return unimpl("panic") }

// constraintLabel — подпись ограничения.
func constraintLabel(c model.Constraint) string {
	switch c.Kind {
	case model.ConstraintPin:
		return "pin:" + c.Server
	case model.ConstraintPrefer:
		return "prefer:" + c.Server
	default:
		return "нет"
	}
}
