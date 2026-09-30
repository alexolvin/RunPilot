package notify

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"runpilot/internal/command"
	"runpilot/internal/model"
)

// W5 CONTROL 6: «веб и Telegram дают одинаковый результат на 30 строках».
//
// Веб (api.handleCommand) и Telegram (notify.Telegram) оба идут через общий
// internal/command: Parse + Run + один Executor. Тест гоняет 30 строк через
// оба пути с ОДНИМ детерминированным Executor и требует побайто идентичный
// Outcome — доказательство, что Telegram не дублирует разбор, а использует
// общий слой.

// mockExec — детерминированный command.Executor: результат зависит только от
// имени метода и аргументов (без состояния, без мутаций).
type mockExec struct{}

func (m *mockExec) call(name string, args ...any) command.Outcome {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = fmt.Sprint(a)
	}
	return command.Outcome{Code: "OK", Text: name + " " + strings.Join(parts, " ")}
}

func (m *mockExec) Address(_ context.Context, t string) (string, string, error) {
	return t, "n-" + t, nil
}

func (m *mockExec) Enqueue(_ context.Context, sid string, c model.QueueClass, cons model.Constraint, after string, front bool) (command.Outcome, error) {
	return m.call("Enqueue", sid, c, cons, after, front), nil
}
func (m *mockExec) Dequeue(_ context.Context, sid string) (command.Outcome, error) {
	return m.call("Dequeue", sid), nil
}
func (m *mockExec) Requeue(_ context.Context, sid string, back bool) (command.Outcome, error) {
	return m.call("Requeue", sid, back), nil
}
func (m *mockExec) RequeueAllHold(_ context.Context) (command.Outcome, error) {
	return m.call("RequeueAllHold"), nil
}
func (m *mockExec) ClearQueue(_ context.Context) (command.Outcome, error) {
	return m.call("ClearQueue"), nil
}
func (m *mockExec) Prio(_ context.Context, sid string, c model.QueueClass) (command.Outcome, error) {
	return m.call("Prio", sid, c), nil
}
func (m *mockExec) SetConstraint(_ context.Context, sid string, cons model.Constraint) (command.Outcome, error) {
	return m.call("SetConstraint", sid, cons), nil
}
func (m *mockExec) Hold(_ context.Context, sid string) (command.Outcome, error) {
	return m.call("Hold", sid), nil
}
func (m *mockExec) Unhold(_ context.Context, sid string) (command.Outcome, error) {
	return m.call("Unhold", sid), nil
}
func (m *mockExec) Cancel(_ context.Context, sid string) (command.Outcome, error) {
	return m.call("Cancel", sid), nil
}
func (m *mockExec) Auto(_ context.Context, sid string, on bool) (command.Outcome, error) {
	return m.call("Auto", sid, on), nil
}
func (m *mockExec) Why(_ context.Context, sid string) (command.Outcome, error) {
	return m.call("Why", sid), nil
}
func (m *mockExec) Paste(_ context.Context, sid, text string, enqueue bool) (command.Outcome, error) {
	return m.call("Paste", sid, text, enqueue), nil
}
func (m *mockExec) Approve(_ context.Context, sid, option string) (command.Outcome, error) {
	return m.call("Approve", sid, option), nil
}
func (m *mockExec) Compress(_ context.Context, sid string) (command.Outcome, error) {
	return m.call("Compress", sid), nil
}
func (m *mockExec) Spawn(_ context.Context, req command.SpawnRequest) (command.Outcome, error) {
	return m.call("Spawn", req.Host, req.Dir, req.Name, req.Class, req.Constraint, req.Auto), nil
}
func (m *mockExec) RestartAgent(_ context.Context, sid string) (command.Outcome, error) {
	return m.call("RestartAgent", sid), nil
}
func (m *mockExec) KillAgent(_ context.Context, sid string) (command.Outcome, error) {
	return m.call("KillAgent", sid), nil
}
func (m *mockExec) Close(_ context.Context, sid string) (command.Outcome, error) {
	return m.call("Close", sid), nil
}
func (m *mockExec) Restore(_ context.Context, sid string) (command.Outcome, error) {
	return m.call("Restore", sid), nil
}
func (m *mockExec) Open(_ context.Context, sid string) (command.Outcome, error) {
	return m.call("Open", sid), nil
}
func (m *mockExec) Term(_ context.Context, sid string) (command.Outcome, error) {
	return m.call("Term", sid), nil
}
func (m *mockExec) ServerOp(_ context.Context, op, name string) (command.Outcome, error) {
	return m.call("ServerOp", op, name), nil
}
func (m *mockExec) NodeOp(_ context.Context, op, host string) (command.Outcome, error) {
	return m.call("NodeOp", op, host), nil
}
func (m *mockExec) Pause(_ context.Context, on bool) (command.Outcome, error) {
	return m.call("Pause", on), nil
}
func (m *mockExec) Panic(_ context.Context, on bool) (command.Outcome, error) {
	return m.call("Panic", on), nil
}

// webRun — путь веба (ядро api.handleCommand): Parse + Run через Executor.
// Ошибки разбора — кодовый Outcome (как веб: UNKNOWN_COMMAND / BAD_COMMAND).
func webRun(ctx context.Context, line string, ex command.Executor) command.Outcome {
	p, err := command.Parse(line)
	if err != nil {
		var uk *command.UnknownCommand
		if errors.As(err, &uk) {
			return command.Outcome{Code: "UNKNOWN_COMMAND", Text: err.Error()}
		}
		return command.Outcome{Code: "BAD_COMMAND", Text: err.Error()}
	}
	out, _ := command.Run(ctx, p, ex)
	return out
}

// TestWebTelegramParity — 30 строк: Outcome веба == Outcome Telegram.
func TestWebTelegramParity(t *testing.T) {
	lines := []string{
		`/help`,
		`/help go`,
		`/pause`,
		`/resume`,
		`/panic`,
		`/unpanic`,
		`/requeue-hold`,
		`/clear-queue`,
		`/go S123 Fix the login bug in auth.go`,
		`/send S456 just paste a note`,
		`/new node-01 /tmp/proj name:worker1 high pin:gpu1`,
		`/enqueue S123`,
		`/enqueue S123 high pin:gpu1 after:S777 front`,
		`/dequeue S123`,
		`/requeue S123 back`,
		`/prio S123 low`,
		`/pin S123 gpu1`,
		`/prefer S123`,
		`/unpin S123`,
		`/hold S123`,
		`/unhold S123`,
		`/cancel S123`,
		`/approve S123 allow`,
		`/compress S123`,
		`/auto S123 on`,
		`/why S123`,
		`/open S123`,
		`/term S123`,
		`/server drain gpu1`,
		`/node drain node-01`,
	}
	if len(lines) != 30 {
		t.Fatalf("строк: %d, хочу 30", len(lines))
	}

	ctx := context.Background()
	ex := &mockExec{}
	tg := &Telegram{Exec: ex}
	for i, line := range lines {
		w := webRun(ctx, line, ex)
		g, err := tg.Run(ctx, line)
		if err != nil {
			t.Fatalf("[%d] %q: telegram: %v", i, line, err)
		}
		if w.Code != g.Code || w.Text != g.Text {
			t.Fatalf("[%d] %q: веб=%+v telegram=%+v — не совпадают", i, line, w, g)
		}
	}
}

// TestWebTelegramParityErrors — одинаковый результат и на ошибках разбора.
func TestWebTelegramParityErrors(t *testing.T) {
	lines := []string{`/bogus`, `не команда`, `/go`, `//`}
	ctx := context.Background()
	ex := &mockExec{}
	tg := &Telegram{Exec: ex}
	for i, line := range lines {
		w := webRun(ctx, line, ex)
		g, _ := tg.Run(ctx, line)
		if w.Code != g.Code || w.Text != g.Text {
			t.Fatalf("[%d] %q: веб=%+v telegram=%+v — не совпадают", i, line, w, g)
		}
	}
}
