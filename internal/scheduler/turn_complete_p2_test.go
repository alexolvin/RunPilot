package scheduler

// П.2 — правило завершения хода (раздел 8 ТЗ), виртуальные часы.
//
// Три сценария из ТЗ:
//  1. сессия 600 с в очереди с IDLE-панелью → выдача аренды → ход НЕ
//     закрывается до подтверждения старта (панель стабильна ДО старта хода);
//  2. DISPATCHING без подтверждения → HOLD(START_NOT_CONFIRMED) через
//     start_confirm_sec, ход не закрывается (его нет);
//  3. ход без запросов BUSY→IDLE → закрытие не раньше
//     turn.started_at+done_quiet_sec и не раньше stable_since+done_stable_sec.
//
// Ключевой фикс: условие панели «IDLE и stable_since ≥ turn.started_at» —
// ход, открыт после долгого ожидания в очереди (панель стабильна ДО старта),
// не закрывается мгновенно (фантом).

import (
	"testing"
	"time"

	"runpilot/internal/model"
	"runpilot/internal/proto"
)

// turnStillOpen — у сессии есть незакрытый ход (ended_at='').
func turnStillOpen(t *testing.T, h *harn, sid string) bool {
	t.Helper()
	_, err := h.st.OpenTurn(sid)
	return err == nil
}

// Тест 1: сессия 600 с в очереди с IDLE-панелью (хеш стабилен с t=0).
// В t=600 выдана аренда и открыт ход (started_at=600). Панель всё ещё
// стабильна с t=0 (ДО старта) → ход НЕ закрывается, пока кодер не начнёт
// (хеш не поменялся после started_at).
func TestP2TurnNotClosedWhilePaneStableBeforeStart(t *testing.T) {
	h := newHarn(t, []ServerView{srv("s", 10, 1)})
	// Панель IDLE, хеш стабилен с t=0 (сессия «в очереди»).
	h.setPane("s1", "host", model.PaneIdle, 1, false)
	h.advance(600 * time.Second) // t=600
	// Выдача аренды + ход (started_at=600), сессия RUNNING.
	runningWithLease(t, h, "s1", "host", "s")

	// Ход не закрывается: панель стабильна ДО старта хода (hashSince=0 < 600).
	h.tick()
	if !turnStillOpen(t, h, "s1") {
		t.Fatal("ход закрыт сразу после выдачи, хотя панель стабильна ДО старта")
	}
	// Спустя done_quiet+done_stable (30 с) — всё ещё не закрыт (хеш не менялся).
	h.advance(30 * time.Second) // t=630
	h.tick()
	if !turnStillOpen(t, h, "s1") {
		t.Fatal("ход закрыт до подтверждения старта (панель стабильна до started_at)")
	}
	// Кодер начал: хеш изменился ПОСЛЕ старта хода (t=640, hashSince=640 ≥ 600).
	h.advance(10 * time.Second)                 // t=640
	h.setPane("s1", "host", model.PaneIdle, 2, false) // hashSince=640
	// +done_stable (5 с): панель стабильна после старта, тишина от started_at.
	h.advance(5 * time.Second) // t=645
	h.tick()
	if turnStillOpen(t, h, "s1") {
		t.Fatal("ход не закрыт после стабилизации панели ПОСЛЕ старта")
	}
}

// Тест 2: DISPATCHING без подтверждения старта → HOLD(START_NOT_CONFIRMED)
// через start_confirm_sec; ход не создаётся и не закрывается (подтверждения
// не было — ход открывается только при подтверждении).
func TestP2DispatchingNoConfirmStartNotConfirmed(t *testing.T) {
	h := newHarn(t, []ServerView{srv("s", 10, 1)})
	h.addSession("s1", "s1", "host", model.SessionQueued, model.ClassNormal, model.NoConstraint)
	h.idlePane("s1", "host")
	h.enqueue("s1", model.ClassNormal, model.NoConstraint, model.QueueModeSubmit)
	// Панель стабильна ≥ dispatch_stable_sec (условие выдачи).
	h.advance(3 * time.Second)

	// Выдача: QUEUED → DISPATCHING (аренда PENDING).
	h.tick()
	if st := h.sessionState("s1"); st != model.SessionDispatching {
		t.Fatalf("state=%s, ждём DISPATCHING", st)
	}
	// Узел ответил SENT → таймер подтверждения (start_confirm_sec).
	h.sch.InjectReply("s1", proto.Msg{Result: proto.ResSent}, nil)
	// Подтверждения нет: панель IDLE (без BUSY/запроса). Хода нет.
	if turnStillOpen(t, h, "s1") {
		t.Fatal("неожиданный открытый ход до подтверждения старта")
	}
	// Прошёл start_confirm_sec → HOLD(START_NOT_CONFIRMED).
	h.advance(time.Duration(h.cfg.Dispatch.StartConfirmSec)*time.Second + time.Second)
	h.tick()
	if st := h.sessionState("s1"); st != model.SessionHold {
		t.Fatalf("state=%s, ждём HOLD(START_NOT_CONFIRMED)", st)
	}
	// Ход так и не был создан (подтверждения не было) → не закрывается.
	if turnStillOpen(t, h, "s1") {
		t.Fatal("ход создан/закрыт без подтверждения старта")
	}
}

// Тест 3: ход БЕЗ запросов, панель BUSY→IDLE. Закрытие не раньше
// max(turn.started_at+done_quiet_sec, stable_since+done_stable_sec).
func TestP2NoRequestTurnCloseLowerBound(t *testing.T) {
	h := newHarn(t, []ServerView{srv("s", 10, 1)})
	// RUNNING + аренда + ход (started_at=0). Панель BUSY (кодер работает).
	runningWithLease(t, h, "s1", "host", "s")
	h.setPane("s1", "host", model.PaneBusy, 1, false) // t=0: BUSY, hashSince=0
	// Кодер закончил: IDLE (t=1) → stable_since=1 (после старта).
	h.advance(1 * time.Second)
	h.setPane("s1", "host", model.PaneIdle, 2, false) // t=1: IDLE, hashSince=1

	q := time.Duration(h.cfg.Turn.DoneQuietSec) * time.Second   // 5s
	st := time.Duration(h.cfg.Turn.DoneStableSec) * time.Second // 5s
	_ = q
	_ = st
	// Границы: started_at+done_quiet = 0+5 = 5s; stable_since+done_stable =
	// 1+5 = 6s. Закрытие — на 6s (позднее), а не раньше.
	// t=5: stable 5-1=4 < 5 → НЕ закрыт.
	h.advance(4 * time.Second) // t=5
	h.tick()
	if !turnStillOpen(t, h, "s1") {
		t.Fatalf("ход закрыт на 5s, раньше stable_since+done_stable_sec = 6s")
	}
	// t=6: stable 6-1=5 >= 5 и тишина 6-0=6 >= 5 → закрыт.
	h.advance(1 * time.Second) // t=6
	h.tick()
	if turnStillOpen(t, h, "s1") {
		t.Fatalf("ход не закрыт на stable_since+done_stable_sec = 6s")
	}
}

// Тест 4 (п.2): условие stable_since ≥ turn.started_at — ко ВСЕМ ходам, не
// только без запросов. Ход С запросами, хеш панели стабилен ДО начала хода →
// ход не закрывается (анти-фантом); закрывается только после изменения хеша
// ПОСЛЕ старта (кодер отработал).
func TestP2RequestTurnNotClosedIfPaneStableBeforeStart(t *testing.T) {
	h := newHarn(t, []ServerView{srv("s", 10, 1)})
	// Панель IDLE, хеш стабилен с t=0 (до начала хода).
	h.setPane("s1", "host", model.PaneIdle, 1, false)
	h.advance(600 * time.Second) // t=600
	// Выдача + ход (started_at=600), RUNNING.
	runningWithLease(t, h, "s1", "host", "s")
	// Запрос хода (2xx): кодер отработал, но хеш панели в тесте не меняется.
	h.sch.RequestTick("s1", true)
	if err := h.st.RequestRecord(model.Request{
		SID: "s1", Server: "s", Path: "v1/chat/completions", Status: 200,
		TStart: h.clk.Now(), TFirstByte: h.clk.Now(), TEnd: h.clk.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	waitLastGenerative(t, h, "s1", 200)
	h.sch.RequestTick("s1", false) // last_request_end = 600
	// Панель НЕ изменилась (hashSince=0 < started_at=600): даже ход С запросом
	// не закрывается (анти-фантом ко всем ходам).
	h.advance(time.Duration(h.cfg.Turn.DoneQuietSec+h.cfg.Turn.DoneStableSec+1) * time.Second)
	h.tick()
	if !turnStillOpen(t, h, "s1") {
		t.Fatal("ход с запросами закрыт, хотя хеш панели стабилен ДО начала хода")
	}
	// Хеш изменился ПОСЛЕ старта (кодер отработал) → ход закрывается.
	h.setPane("s1", "host", model.PaneIdle, 2, false) // hashSince > 600
	h.advance(time.Duration(h.cfg.Turn.DoneStableSec)*time.Second + time.Second)
	h.tick()
	if turnStillOpen(t, h, "s1") {
		t.Fatal("ход не закрыт после стабилизации панели ПОСЛЕ старта (с запросом)")
	}
}
