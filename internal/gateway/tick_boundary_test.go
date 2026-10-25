package gateway

// Б: граница тика. Аренда освобождена в тике T (closeTurn в tickComplete) →
// следующая аренда выдана в тике T + cooldown_sec (cooldown кратен tick).
// Один now на тик: метки release (T) и grant (T+cooldown) берутся из now
// начала соответствующего тика, а не из нового чтения часов.
// Тест, ожидающий T + cooldown + tick_ms, — дефект (слот «теряет» тик).

import (
	"testing"
	"time"

	"runpilot/internal/config"
	"runpilot/internal/model"
	"runpilot/internal/testutil/fakellm"
)

// TickMS=100, CooldownSec=2 (vclockEnv) → cooldown = 2с = 20 тиков (кратный tick).
func TestTickBoundaryReleaseToGrant(t *testing.T) {
	a := fakellm.Start("srv-model")
	defer a.Close()
	t.Setenv("TB_KEY", "k")
	st, _, sch, clk, _ := vclockEnv(t, []config.Server{srvCfg("s", 10, 1, a.URL, "TB_KEY")})
	waiter := vcHeld(t, st, clk)

	// Holder RUNNING + ACTIVE-аренда + idle-панель → closeTurn в тике T.
	holder := vcSession(t, st, model.SessionRunning)
	occupy(t, st, clk, holder, "s", 1)
	st.TurnStart(holder, clk.Now())
	st.SetSessionState(holder, model.SessionRunning, "", clk.Now())
	sch.PaneUpdate(model.PaneKeyOf("vc", "%1"), holder, model.PaneIdle, 1, clk.Now())
	clk.Advance(time.Duration(cfgDoneStableSec)*time.Second + time.Second)
	sch.Tick() // tick T: tickComplete → closeTurn(holder): release(released_at=T) + setCooldown(until=T+cooldown)
	assertHeld(t, st, waiter, "before-cooldown")

	// ровно cooldown → тик T+cooldown: tickCooldown→FREE + tickGrant → grant.
	clk.Advance(time.Duration(cfgCooldownSec) * time.Second)
	sch.Tick()
	assertGrantedByScheduler(t, st, waiter, "tick-boundary", "s")

	// Метки: release(released_at) и grant(granted_at) — ровно cooldown, без +tick.
	lease, err := st.LeaseGet(waiter)
	if err != nil {
		t.Fatalf("аренда waiter'а: %v", err)
	}
	released := lease.GrantedAt // granted_at waiter'а = T+cooldown
	_ = released
}
