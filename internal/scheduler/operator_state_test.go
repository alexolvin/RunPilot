package scheduler

// П.1 — операторские состояния: персистятся при изменении и восстанавливаются
// при старте координатора. Сценарий: установить каждое состояние → «рестарт»
// (новый планировщик на той же БД) → состояние сохранено + восстановленные
// состояния записаны в журнал событий.

import (
	"context"
	"errors"
	"testing"

	"runpilot/internal/model"
	"runpilot/internal/proto"
	"runpilot/internal/store"
)

func containsStr(a []string, x string) bool {
	for _, v := range a {
		if v == x {
			return true
		}
	}
	return false
}

func TestOperatorStatesPersistAndRestore(t *testing.T) {
	h := newHarn(t, []ServerView{srv("a", 10, 1), srv("b", 5, 1)})

	// --- установка всех трёх видов операторских состояний ---
	// 1) пауза планировщика (meta.scheduler_paused).
	h.sch.Pause(true)
	if on, _ := h.st.SchedulerPaused(); !on {
		t.Fatal("пауза планировщика не сохранена в БД")
	}
	// 2) drain узла (operator_state).
	h.sch.NodeDrain("host1", true)
	if hosts, _ := h.st.OperatorStates(store.OperatorStateNodeDrain); !containsStr(hosts, "host1") {
		t.Fatal("drain узла не сохранён в БД")
	}
	// 3) drain сервера (как делает колбэк serve.go).
	if err := h.st.SetOperatorState("a", store.OperatorStateServerDrain, true); err != nil {
		t.Fatal(err)
	}

	// Максимальный ID события до «рестарта» (для проверки новых записей в журнал).
	evsBefore, err := h.st.EventList(0, 100000)
	if err != nil {
		t.Fatal(err)
	}
	var lastID int64
	if len(evsBefore) > 0 {
		lastID = evsBefore[len(evsBefore)-1].ID
	}

	// --- «рестарт»: новый планировщик на той же БД ---
	applied := map[string]model.ServerState{}
	sch2 := New(Options{
		Cfg: h.cfg, Store: h.st, Clk: h.clk, Servers: h.srv, Panes: h.panes,
		Dispatch: func(ctx context.Context, paneID string, m proto.Msg) (proto.Msg, error) {
			return proto.Msg{}, errors.New("dispatch отключён")
		},
		ResumeText: "continue", NoAsyncDispatch: true,
		Reload: func() error { return h.st.LoadWorkingInto(h.cfg) },
	})
	_ = sch2.LoadMode()
	d, _, n := sch2.RestoreOperatorStates(func(name string, st model.ServerState) {
		applied[name] = st
	})

	// --- проверки восстановления ---
	if m := sch2.Mode(); m != string(model.ModePaused) {
		t.Fatalf("пауза планировщика не восстановлена: Mode()=%s", m)
	}
	if applied["a"] != model.ServerDraining {
		t.Fatalf("drain сервера a не восстановлен: %q", applied["a"])
	}
	if !containsStr(d, "a") || !containsStr(n, "host1") {
		t.Fatalf("списки восстановления неверны: d=%v n=%v", d, n)
	}
	if !sch2.nodeDrain["host1"] {
		t.Fatal("nodeDrain не применён к host1 после восстановления")
	}

	// EventRecord идёт через QueueWrite (асинхронно). DoWrite — синхронный и
	// дожидается ack (очередь писателя FIFO) → барьер: гарантирует, что
	// события restore уже записаны.
	if err := h.st.SetLastAliveAt(h.clk.Now()); err != nil {
		t.Fatal(err)
	}
	evsAfter, err := h.st.EventList(lastID, 100000)
	if err != nil {
		t.Fatal(err)
	}
	var sawMode, sawNodeDrain bool
	for _, ev := range evsAfter {
		if ev.Kind == model.KindMode {
			sawMode = true
		}
		if ev.Kind == model.KindNodeState && ev.Server == "host1" {
			sawNodeDrain = true
		}
	}
	if !sawMode {
		t.Fatal("восстановленная пауза планировщика не в журнале (KindMode)")
	}
	if !sawNodeDrain {
		t.Fatal("восстановленный drain узла не в журнале (KindNodeState host1)")
	}
}
