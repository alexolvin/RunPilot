package monitor

import (
	"log/slog"
	"testing"
	"time"

	"runpilot/internal/clock"
	"runpilot/internal/config"
)

// TestFlaps — синтетические события: 3 перехода UP↔DOWN за 5 минут →
// Flaps(10m) возвращает сервер с Count=3, LastFlap = самое позднее.
func TestFlaps(t *testing.T) {
	clk := clock.NewVirtual(time.Now().UTC())
	cfg := config.Defaults()
	cfg.Servers = []config.Server{{Name: "srv1", Slots: 1}}
	m := New(cfg, clk, nil, nil, nil, nil, slog.New(slog.NewTextHandler(nil, nil)))

	now := clk.Now()
	// 3 перехода: 5, 3, 1 минуту назад.
	m.srvs["srv1"].flaps = []time.Time{
		now.Add(-5 * time.Minute),
		now.Add(-3 * time.Minute),
		now.Add(-1 * time.Minute),
	}

	// Flaps(10m) — все 3 перехода в окне.
	out := m.Flaps(10 * time.Minute)
	if len(out) != 1 {
		t.Fatalf("Flaps(10m): %d серверов, хочу 1", len(out))
	}
	if out[0].Name != "srv1" {
		t.Fatalf("Flaps: имя=%q, хочу srv1", out[0].Name)
	}
	if out[0].Count != 3 {
		t.Fatalf("Flaps: Count=%d, хочу 3", out[0].Count)
	}
	if !out[0].LastFlap.Equal(now.Add(-1 * time.Minute)) {
		t.Fatalf("Flaps: LastFlap=%s, хочу %s", out[0].LastFlap, now.Add(-1*time.Minute))
	}

	// Flaps(2m) — только 1 переход (1 минуту назад).
	out = m.Flaps(2 * time.Minute)
	if len(out) != 1 {
		t.Fatalf("Flaps(2m): %d серверов, хочу 1", len(out))
	}
	if out[0].Count != 1 {
		t.Fatalf("Flaps(2m): Count=%d, хочу 1", out[0].Count)
	}

	// Flaps(30s) — нет переходов.
	out = m.Flaps(30 * time.Second)
	if len(out) != 0 {
		t.Fatalf("Flaps(30s): %d серверов, хочу 0", len(out))
	}
}
