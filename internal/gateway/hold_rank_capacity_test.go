package gateway

// Тесты ранга и ёмкости выдачи (П.10: аренду выдаёт только планировщик).

import (
	"testing"
	"time"

	"runpilot/internal/config"
	"runpilot/internal/model"
	"runpilot/internal/testutil/fakellm"
)

// Ранг: слот занят; в очереди допустимая запись X с рангом выше (RESUME=0) и
// удерживаемый запрос B (HIGH=1). При освобождении слота аренду получает X,
// B остаётся в удержании.
func TestHoldRankResumeBeatsHeld(t *testing.T) {
	a := fakellm.Start("srv-model")
	defer a.Close()
	t.Setenv("RK_KEY", "k")
	st, gw, sch, clk, _ := vclockEnv(t, []config.Server{srvCfg("a", 10, 1, a.URL, "RK_KEY")})

	// X: допустимая запись с рангом RESUME (выше HIGH) + снимок панели.
	x := vcSession(t, st, model.SessionQueued)
	now := clk.Now()
	if err := st.QueueUpsert(model.QueueEntry{
		SID: x, Class: model.ClassResume, EnqueuedAt: now, NotBefore: now,
		Constraint: model.NoConstraint, Mode: model.QueueModeSubmit, HeldRequest: false,
	}); err != nil {
		t.Fatal(err)
	}
	sch.PaneUpdate(model.PaneKeyOf("vc", "%2"), x, model.PaneIdle, 1, clk.Now())
	// B: удерживаемый запрос (HIGH) — запись held_request=1 (как создаёт holdPass).
	b := vcHeld(t, st, clk)

	// Слот занят (holder): ждущие не выдаются.
	holder := vcSession(t, st, model.SessionRunning)
	occupy(t, st, clk, holder, "a", 1)
	sch.Tick()
	assertHeld(t, st, b, "rank-before")
	if _, err := st.LeaseGet(x); err == nil {
		t.Fatal("X не должна получить аренду, пока слот занят")
	}

	// Освобождение слота: holder завершает ход (closeTurn) → слот COOLDOWN,
	// таймер COOLDOWN на +cooldown_sec (граница тика).
	sch.PaneUpdate(model.PaneKeyOf("vc", "%1"), holder, model.PaneIdle, 1, clk.Now())
	clk.Advance(time.Duration(cfgDoneStableSec) * time.Second + time.Second)
	sch.Tick() // closeTurn(holder) → слот COOLDOWN (таймер на +cooldown)

	// Снимок X — свежий ДО конца COOLDOWN: выдача теперь в at+cooldown (таймер),
	// а не на следующем тике; если бы X устарел на границе, выдан был held-B.
	sch.PaneUpdate(model.PaneKeyOf("vc", "%2"), x, model.PaneIdle, 1, clk.Now())
	clk.Advance(time.Duration(cfgCooldownSec)*time.Second + 100*time.Millisecond)
	// таймер COOLDOWN срабатывает в at+cooldown → tickGrant: X (RESUME) > B (HIGH).
	lx, errX := st.LeaseGet(x)
	if errX != nil {
		t.Fatalf("X должна получить аренду (RESUME > HIGH): %v", errX)
	}
	if lx.Server != "a" {
		t.Fatalf("X: сервер %s, хочу a", lx.Server)
	}
	// B остаётся в удержании (запись на месте, аренды нет).
	if _, err := st.LeaseGet(b); err == nil {
		t.Fatal("B не должна получить аренду (слот у X)")
	}
	if _, err := st.QueueGet(b); err != nil {
		t.Fatal("запись B должна остаться в очереди (в удержании)")
	}

	// Следующий слот: X освобождает → B получает аренду (HIGH).
	gw.Release(x, "TURN_DONE")
	sch.Tick()
	clk.Advance(time.Duration(cfgCooldownSec)*time.Second + 100*time.Millisecond)
	sch.Tick() // COOLDOWN→FREE + tickGrant: B (HIGH) выдаётся
	lb, errB := st.LeaseGet(b)
	if errB != nil {
		t.Fatalf("B должна получить аренду (следующий слот): %v", errB)
	}
	_ = lb
}

// Ёмкость: 1 слот, 3 удерживаемых запроса + 2 записи очереди, слот освобождается
// 10 раз. На каждом тике активных аренд ≤ 1.
func TestHoldCapacityOneSlotTenFrees(t *testing.T) {
	a := fakellm.Start("srv-model")
	defer a.Close()
	t.Setenv("CP_KEY", "k")
	st, _, sch, clk, _ := vclockEnv(t, []config.Server{srvCfg("a", 10, 1, a.URL, "CP_KEY")})

	// 3 удерживаемых (HIGH) + 2 записи (NORMAL).
	for i := 0; i < 3; i++ {
		vcHeld(t, st, clk)
	}
	for i := 0; i < 2; i++ {
		s := vcSession(t, st, model.SessionQueued)
		if err := st.QueueUpsert(model.QueueEntry{
			SID: s, Class: model.ClassNormal, EnqueuedAt: clk.Now(), NotBefore: clk.Now(),
			Constraint: model.NoConstraint, Mode: model.QueueModeSubmit, HeldRequest: false,
		}); err != nil {
			t.Fatal(err)
		}
	}

	// 10 итераций: слот занят → освобождаем → tick → активных аренд ≤ 1.
	for i := 0; i < 10; i++ {
		// Слот занят (holder).
		holder := vcSession(t, st, model.SessionRunning)
		occupy(t, st, clk, holder, "a", 1)
		st.TurnStart(holder, clk.Now())
		st.SetSessionState(holder, model.SessionRunning, "", clk.Now())
		sch.PaneUpdate(model.PaneKeyOf("vc", "%1"), holder, model.PaneIdle, 1, clk.Now())
		clk.Advance(time.Duration(cfgDoneStableSec)*time.Second + time.Second)
		sch.Tick() // closeTurn → слот COOLDOWN
		clk.Advance(time.Duration(cfgCooldownSec)*time.Second + 100*time.Millisecond)
		sch.Tick() // COOLDOWN→FREE + tickGrant: 1 выдача
		// Активных аренд ≤ 1.
		leases, _ := st.LeaseListActive()
		if len(leases) > 1 {
			t.Fatalf("итерация %d: активных аренд %d, хочу ≤ 1", i, len(leases))
		}
	}
}
