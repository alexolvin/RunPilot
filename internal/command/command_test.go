package command

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"runpilot/internal/model"
)

// mockExec — фиксирует последнюю операцию Executor (веб/Telegram общий
// разбор, раздел 12: «тест разбора и выполнения на каждую команду»).
type mockExec struct {
	method string
	args   []any
}

func (m *mockExec) rec(method string, args ...any) Outcome {
	m.method, m.args = method, args
	return ok(method)
}
func (m *mockExec) Address(_ context.Context, t string) (string, string, error) {
	m.method, m.args = "Address", []any{t}
	return "sid:" + t, t, nil
}
func (m *mockExec) Enqueue(_ context.Context, sid string, c model.QueueClass, cons model.Constraint, after string, front bool) (Outcome, error) {
	return m.rec("Enqueue", sid, c, cons, after, front), nil
}
func (m *mockExec) Dequeue(_ context.Context, sid string) (Outcome, error)   { return m.rec("Dequeue", sid), nil }
func (m *mockExec) Requeue(_ context.Context, sid string, back bool) (Outcome, error) {
	return m.rec("Requeue", sid, back), nil
}
func (m *mockExec) RequeueAllHold(_ context.Context) (Outcome, error) { return m.rec("RequeueAllHold"), nil }
func (m *mockExec) ClearQueue(_ context.Context) (Outcome, error)    { return m.rec("ClearQueue"), nil }
func (m *mockExec) Prio(_ context.Context, sid string, c model.QueueClass) (Outcome, error) {
	return m.rec("Prio", sid, c), nil
}
func (m *mockExec) SetConstraint(_ context.Context, sid string, cons model.Constraint) (Outcome, error) {
	return m.rec("SetConstraint", sid, cons), nil
}
func (m *mockExec) Hold(_ context.Context, sid string) (Outcome, error)        { return m.rec("Hold", sid), nil }
func (m *mockExec) Unhold(_ context.Context, sid string) (Outcome, error)      { return m.rec("Unhold", sid), nil }
func (m *mockExec) Cancel(_ context.Context, sid string) (Outcome, error)      { return m.rec("Cancel", sid), nil }
func (m *mockExec) Auto(_ context.Context, sid string, on bool) (Outcome, error) {
	return m.rec("Auto", sid, on), nil
}
func (m *mockExec) Why(_ context.Context, sid string) (Outcome, error)         { return m.rec("Why", sid), nil }
func (m *mockExec) Paste(_ context.Context, sid, text string, enqueue bool) (Outcome, error) {
	return m.rec("Paste", sid, text, enqueue), nil
}
func (m *mockExec) Approve(_ context.Context, sid, option string) (Outcome, error) {
	return m.rec("Approve", sid, option), nil
}
func (m *mockExec) Compress(_ context.Context, sid string) (Outcome, error)     { return m.rec("Compress", sid), nil }
func (m *mockExec) Spawn(_ context.Context, req SpawnRequest) (Outcome, error)  { return m.rec("Spawn", req), nil }
func (m *mockExec) RestartAgent(_ context.Context, sid string) (Outcome, error) { return m.rec("RestartAgent", sid), nil }
func (m *mockExec) KillAgent(_ context.Context, sid string) (Outcome, error)    { return m.rec("KillAgent", sid), nil }
func (m *mockExec) Close(_ context.Context, sid string) (Outcome, error)        { return m.rec("Close", sid), nil }
func (m *mockExec) Restore(_ context.Context, sid string) (Outcome, error)      { return m.rec("Restore", sid), nil }
func (m *mockExec) Open(_ context.Context, sid string) (Outcome, error)         { return m.rec("Open", sid), nil }
func (m *mockExec) Term(_ context.Context, sid string) (Outcome, error)         { return m.rec("Term", sid), nil }
func (m *mockExec) ServerOp(_ context.Context, op, name string) (Outcome, error) {
	return m.rec("ServerOp", op, name), nil
}
func (m *mockExec) NodeOp(_ context.Context, op, host string) (Outcome, error)  { return m.rec("NodeOp", op, host), nil }
func (m *mockExec) Pause(_ context.Context, on bool) (Outcome, error)           { return m.rec("Pause", on), nil }
func (m *mockExec) Panic(_ context.Context, on bool) (Outcome, error)           { return m.rec("Panic", on), nil }

func eq(t *testing.T, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

// TestRunEveryCommand — разбор + выполнение на каждую команду раздела 12.
func TestRunEveryCommand(t *testing.T) {
	ctx := context.Background()
	m := &mockExec{}
	sid := "sid:alpha"

	run := func(line string) Outcome {
		m.method, m.args = "", nil
		p, err := Parse(line)
		if err != nil {
			t.Fatalf("Parse(%q): %v", line, err)
		}
		out, err := Run(ctx, p, m)
		if err != nil {
			t.Fatalf("Run(%q): %v", line, err)
		}
		return out
	}

	// Без аргументов на операцию (метод проверяется ниже).
	eq(t, run("/pause").Code, "OK")
	eq(t, m.method, "Pause")
	eq(t, m.args[0], true)
	eq(t, m.method, "Pause")
	run("/resume")
	eq(t, m.args[0], false)
	run("/panic")
	eq(t, m.method, "Panic")
	eq(t, m.args[0], true)
	run("/unpanic")
	eq(t, m.args[0], false)
	run("/requeue-hold")
	eq(t, m.method, "RequeueAllHold")
	run("/clear-queue")
	eq(t, m.method, "ClearQueue")

	run("/go alpha hello world here")
	eq(t, m.method, "Paste")
	eq(t, m.args[0], sid)
	eq(t, m.args[1], "hello world here")
	eq(t, m.args[2], true)
	run("/send alpha just insert")
	eq(t, m.method, "Paste")
	eq(t, m.args[0], sid)
	eq(t, m.args[1], "just insert")
	eq(t, m.args[2], false)

	run("/enqueue alpha high pin:srv1 after:beta front")
	eq(t, m.method, "Enqueue")
	eq(t, m.args[0], sid)
	eq(t, m.args[1], model.ClassHigh)
	eq(t, m.args[2], model.Constraint{Kind: model.ConstraintPin, Server: "srv1"})
	eq(t, m.args[3], "beta")
	eq(t, m.args[4], true)

	run("/dequeue alpha")
	eq(t, m.method, "Dequeue")
	run("/requeue alpha back")
	eq(t, m.method, "Requeue")
	eq(t, m.args[1], true)
	run("/requeue alpha")
	eq(t, m.method, "Requeue")
	eq(t, m.args[1], false)
	run("/prio alpha low")
	eq(t, m.method, "Prio")
	eq(t, m.args[1], model.ClassLow)
	run("/pin alpha srv1")
	eq(t, m.method, "SetConstraint")
	eq(t, m.args[1], model.Constraint{Kind: model.ConstraintPin, Server: "srv1"})
	run("/prefer alpha srv2")
	eq(t, m.method, "SetConstraint")
	eq(t, m.args[1], model.Constraint{Kind: model.ConstraintPrefer, Server: "srv2"})
	run("/unpin alpha")
	eq(t, m.method, "SetConstraint")
	eq(t, m.args[1], model.NoConstraint)
	run("/hold alpha")
	eq(t, m.method, "Hold")
	run("/unhold alpha")
	eq(t, m.method, "Unhold")
	run("/cancel alpha")
	eq(t, m.method, "Cancel")
	run("/approve alpha yes")
	eq(t, m.method, "Approve")
	eq(t, m.args[1], "yes")
	run("/compress alpha")
	eq(t, m.method, "Compress")
	run("/auto alpha on")
	eq(t, m.method, "Auto")
	eq(t, m.args[1], true)
	run("/auto alpha off")
	eq(t, m.method, "Auto")
	eq(t, m.args[1], false)
	run("/why alpha")
	eq(t, m.method, "Why")
	run("/open alpha")
	eq(t, m.method, "Open")
	run("/term alpha")
	eq(t, m.method, "Term")
	run("/restart-agent alpha")
	eq(t, m.method, "RestartAgent")
	run("/kill-agent alpha")
	eq(t, m.method, "KillAgent")
	run("/close alpha")
	eq(t, m.method, "Close")
	run("/restore alpha")
	eq(t, m.method, "Restore")
	run("/server drain srv1")
	eq(t, m.method, "ServerOp")
	eq(t, m.args[0], "drain")
	eq(t, m.args[1], "srv1")
	run("/node update node1")
	eq(t, m.method, "NodeOp")
	eq(t, m.args[0], "update")
	eq(t, m.args[1], "node1")

	run("/new node1 /path/x name:foo high pin:srv1 auto")
	eq(t, m.method, "Spawn")
	sr := m.args[0].(SpawnRequest)
	eq(t, sr.Host, "node1")
	eq(t, sr.Dir, "/path/x")
	eq(t, sr.Name, "foo")
	eq(t, sr.Class, model.ClassHigh)
	eq(t, sr.HasClass, true)
	eq(t, sr.Constraint, model.Constraint{Kind: model.ConstraintPin, Server: "srv1"})
	eq(t, sr.Auto, true)

	// /help — инлайн (метод Executor не вызывается).
	out := run("/help pause")
	eq(t, out.Code, "OK")
	eq(t, m.method, "")
	if !strings.Contains(out.Text, "/pause") {
		t.Fatalf("help pause: нет /pause в ответе: %q", out.Text)
	}
}

// TestEveryRegistryCommand — разбор + выполнение на КАЖДУЮ команду реестра
// (раздел 12): итерация по All(), образец строки на команду. Если команда
// добавлена в реестр, но нет образца — тест падает (контроль W5, CONTROL 5).
func TestEveryRegistryCommand(t *testing.T) {
	sample := map[string]string{
		"new":           "new node1 /path/x name:foo high pin:srv1 auto",
		"go":            "go alpha hello world here",
		"send":          "send alpha just insert",
		"enqueue":       "enqueue alpha high pin:srv1 after:beta front",
		"dequeue":       "dequeue alpha",
		"requeue":       "requeue alpha back",
		"requeue-hold":  "requeue-hold",
		"clear-queue":   "clear-queue",
		"prio":          "prio alpha low",
		"pin":           "pin alpha srv1",
		"prefer":        "prefer alpha srv2",
		"unpin":         "unpin alpha",
		"hold":          "hold alpha",
		"unhold":        "unhold alpha",
		"cancel":        "cancel alpha",
		"approve":       "approve alpha yes",
		"compress":      "compress alpha",
		"auto":          "auto alpha on",
		"why":           "why alpha",
		"open":          "open alpha",
		"term":          "term alpha",
		"restart-agent": "restart-agent alpha",
		"kill-agent":    "kill-agent alpha",
		"close":         "close alpha",
		"restore":       "restore alpha",
		"server":        "server drain srv1",
		"node":          "node update node1",
		"pause":         "pause",
		"resume":        "resume",
		"panic":         "panic",
		"unpanic":       "unpanic",
		"help":          "help pause",
	}
	all := All()
	if len(sample) != len(all) {
		t.Fatalf("образцов команд %d, в реестре раздела 12: %d", len(sample), len(all))
	}
	ctx := context.Background()
	m := &mockExec{}
	for _, c := range all {
		line, ok := sample[c.Name]
		if !ok {
			t.Fatalf("нет образца строки для команды раздела 12: %s", c.Name)
		}
		p, err := Parse("/" + line)
		if err != nil {
			t.Fatalf("Parse(%q): %v", line, err)
		}
		if p.Cmd.Name != c.Name {
			t.Fatalf("Parse(%q): cmd=%s, хочу %s", line, p.Cmd.Name, c.Name)
		}
		if _, err := Run(ctx, p, m); err != nil {
			t.Fatalf("Run(%q): %v", line, err)
		}
	}
}

// TestParse — разбор: текст go/send, неизвестная команда, пустая, без «/».
func TestParse(t *testing.T) {
	p, err := Parse("/go a b c")
	if err != nil {
		t.Fatal(err)
	}
	eq(t, p.Cmd.Name, "go")
	eq(t, p.Text, "b c")
	if _, err := Parse("no slash"); err != ErrNotCommand {
		t.Fatalf("без «/» = ErrNotCommand, got %v", err)
	}
	if _, err := Parse("  /"); err != ErrEmpty {
		t.Fatalf("пусто = ErrEmpty, got %v", err)
	}
	_, err = Parse("/нет-такой")
	if _, ok := err.(*UnknownCommand); !ok {
		t.Fatalf("неизвестная команда = *UnknownCommand, got %v", err)
	}
}

// TestComplete — подсказки команд и аргументов.
func TestComplete(t *testing.T) {
	dp := testDP{}
	// Имена команд по префиксу.
	ins := Complete("/enq", dp)
	if len(ins) != 1 || ins[0].Text != "enqueue " {
		t.Fatalf("Complete(/enq) = %v", ins)
	}
	// <t> — имя сессии.
	ins = Complete("/cancel task", dp)
	if len(ins) != 1 || ins[0].Text != "task-a " {
		t.Fatalf("Complete(/cancel task) = %v", ins)
	}
	// класс.
	ins = Complete("/prio t nor", dp)
	if len(ins) != 1 || ins[0].Text != "normal" {
		t.Fatalf("Complete(/prio t nor) = %v", ins)
	}
	// сервер для pin.
	ins = Complete("/pin t srv1", dp)
	if len(ins) != 1 || ins[0].Text != "srv1" {
		t.Fatalf("Complete(/pin t srv1) = %v", ins)
	}
	// after:<t>.
	ins = Complete("/enqueue t after:tas", dp)
	if len(ins) != 1 || ins[0].Text != "after:task-a " {
		t.Fatalf("Complete(/enqueue t after:tas) = %v", ins)
	}
	// on|off.
	ins = Complete("/auto t of", dp)
	if len(ins) != 1 || ins[0].Text != "off" {
		t.Fatalf("Complete(/auto t of) = %v", ins)
	}
}

type testDP struct{}

func (testDP) Targets() []TargetValue {
	return []TargetValue{{SID: "SS1", Name: "task-a", State: "QUEUED"}}
}
func (testDP) Servers() []string { return []string{"srv1", "srv2"} }
func (testDP) Nodes() []string   { return []string{"node1"} }
func (testDP) Catalogs() []string { return []string{"/repo/a"} }

// TestDocs — docs/commands.md генерируется из реестра (все команды).
func TestDocs(t *testing.T) {
	d := Docs()
	for _, c := range registry {
		if !strings.Contains(d, "/"+c.Name) {
			t.Fatalf("Docs: нет /%s", c.Name)
		}
	}
	eq(t, strings.Count(d, "| /"), len(registry))
}

// bigDP — DataProvider на N сессий (CONTROL W5: 50 сессий для бюджета).
type bigDP struct {
	sessions []TargetValue
	servers  []string
	nodes    []string
}

func (b bigDP) Targets() []TargetValue { return b.sessions }
func (b bigDP) Servers() []string      { return b.servers }
func (b bigDP) Nodes() []string        { return b.nodes }
func (b bigDP) Catalogs() []string     { return nil }

func makeSessions(n int) []TargetValue {
	out := make([]TargetValue, 0, n)
	states := []string{"IDLE", "RUNNING", "QUEUED", "PROMPT"}
	for i := 0; i < n; i++ {
		out = append(out, TargetValue{
			SID:   fmt.Sprintf("SS%05d", i),
			Name:  fmt.Sprintf("task-%02d", i),
			State: states[i%len(states)],
		})
	}
	return out
}

// TestControl7CompleteBudget — подсказки ≤ ui.complete_budget_ms p95 на 1000
// запросах при 50 сессиях (CONTROL W5, раздел 12).
func TestControl7CompleteBudget(t *testing.T) {
	dp := bigDP{
		sessions: makeSessions(50),
		servers:  []string{"srv-01", "srv-02", "srv-03"},
		nodes:    []string{"node-01", "node-02"},
	}
	// Разные виды строк: префикс команды, имя сессии, класс, сервер, after:.
	lines := []string{
		"/", "/e", "/enq", "/enqueue t after:tas", "/cancel", "/cancel task-0",
		"/prio t n", "/pin t s", "/pin t srv-0", "/auto t o", "/new node1 /path",
		"/server", "/server dr", "/node up", "/help", "/go task-0", "некоманда", "/нет",
	}
	const reqs = 1000
	durs := make([]time.Duration, 0, reqs)
	for i := 0; i < reqs; i++ {
		start := time.Now()
		_ = Complete(lines[i%len(lines)], dp)
		durs = append(durs, time.Since(start))
	}
	sort.Slice(durs, func(a, b int) bool { return durs[a] < durs[b] })
	p95 := durs[int(float64(len(durs))*0.95)]
	budget := time.Duration(CompleteBudgetMS) * time.Millisecond
	if p95 > budget {
		t.Fatalf("p95 подсказок = %v, бюджет %d мс (1000 запросов, 50 сессий)", p95, CompleteBudgetMS)
	}
	t.Logf("подсказки p95 = %v (бюджет %d мс, 1000 запросов / 50 сессий)", p95, CompleteBudgetMS)
}
