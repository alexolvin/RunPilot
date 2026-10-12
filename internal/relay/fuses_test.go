package relay

import (
	"testing"
	"time"

	"runpilot/internal/clock"
	"runpilot/internal/config"
)

// TestFusedCycles — предохранитель циклов (ТЗ §14): auto_cycles ≥ max → FUSE_CYCLES.
func TestFusedCycles(t *testing.T) {
	r := relayTest()
	max := r.cfg.MaxAutoCycles
	if !r.Fused(max) {
		t.Fatal("Fused(max) = false, хочу true")
	}
	if r.Fused(max-1) {
		t.Fatal("Fused(max-1) = true, хочу false")
	}
	if r.Fused(0) {
		t.Fatal("Fused(0) = true, хочу false")
	}
}

// TestNextState — переходы состояний (ТЗ §5).
func TestNextState(t *testing.T) {
	if st := NextState(true, false, ""); st != "ARMED" {
		t.Fatalf("ok → %s, хочу ARMED", st)
	}
	if st := NextState(false, true, ""); st != "DONE" {
		t.Fatalf("stop → %s, хочу DONE", st)
	}
	if st := NextState(false, false, "ORCH_RATE_LIMIT"); st != "HOLD" {
		t.Fatalf("hold → %s, хочу HOLD", st)
	}
}

// TestWaitMinSendIntervalVirtual — пауза между отправками на виртуальных часах.
func TestWaitMinSendIntervalVirtual(t *testing.T) {
	cfg := config.Defaults().Relay
	cfg.MinSendIntervalSec = 30
	clk := clock.NewVirtual(time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC))
	r := &Relay{cfg: cfg, clk: clk, lastSent: clk.Now()}
	// 10 с прошло — нужно подождать 20 с.
	clk.Advance(10 * time.Second)
	// waitMinSendInterval должен вернуть nil (подошёл) или ошибку ctx.
	// Здесь проверяем, что после 30 с паузы не требуется.
	clk.Advance(20 * time.Second)
	if err := r.waitMinSendInterval(t.Context()); err != nil {
		t.Fatalf("waitMinSendInterval после 30 с: %v", err)
	}
}
