package node

import (
	"strings"
	"testing"
	"time"

	"runpilot/internal/clock"
	"runpilot/internal/detect"
)

var t0 = time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)

func TestSnapshotPeriods(t *testing.T) {
	cases := []struct {
		st   detect.State
		want time.Duration
	}{
		{detect.Busy, periodBusy},
		{detect.Idle, periodWaiting},
		{detect.Prompt, periodWaiting},
		{detect.WaitUI, periodWaiting},
		{detect.Unknown, periodUnknown},
	}
	for _, c := range cases {
		if got := period(c.st); got != c.want {
			t.Fatalf("period(%s) = %s, хочу %s", c.st, got, c.want)
		}
	}
}

func TestSnapshotSyncSendOnChange(t *testing.T) {
	clk := clock.NewVirtual(t0)
	s := newSnapshotter(clk, "0.24.4")
	info := PaneInfo{PaneID: "%10", SID: "S1", Session: "sess1"}

	// Первый снимок: отправляется, stable_since = t0.
	p, changed := s.sync(info, detect.Idle, detect.Hash(idleScreen), "", clk.Now())
	if !changed {
		t.Fatal("первый снимок обязан быть отправлен")
	}
	if p.StableSince != protoStableSince(t0) {
		t.Fatalf("stable_since = %s, хочу %s", p.StableSince, protoStableSince(t0))
	}
	if p.State != "IDLE" || p.InputEmpty != true {
		t.Fatalf("pane = %+v", p)
	}

	// Повтор без смены: не отправляется, пульса ещё нет.
	if _, changed := s.sync(info, detect.Idle, detect.Hash(idleScreen), "", clk.Now()); changed {
		t.Fatal("без смены состояния отправлять нечего")
	}
	if _, ok := s.pulse(info, clk.Now().Add(9*time.Second)); ok {
		t.Fatal("пульс раньше 10 с не положен")
	}

	// Пульс через 10 с.
	if _, ok := s.pulse(info, clk.Now().Add(10*time.Second)); !ok {
		t.Fatal("пульс через 10 с обязан отправить (раздел 7 ТЗ)")
	}
}

func TestSnapshotStableSinceTracksHashChange(t *testing.T) {
	clk := clock.NewVirtual(t0)
	s := newSnapshotter(clk, "0.24.4")
	info := PaneInfo{PaneID: "%10", SID: "S1", Session: "sess1"}

	h1 := detect.Hash(idleScreen)
	s.sync(info, detect.Idle, h1, "", t0)
	// Экран меняется (хеш) — stable_since обновляется.
	h2 := detect.Hash(busyScreen)
	p, changed := s.sync(info, detect.Busy, h2, "", t0.Add(3*time.Second))
	if !changed {
		t.Fatal("смена хеша/состояния обязан отправить")
	}
	if p.StableSince != protoStableSince(t0.Add(3*time.Second)) {
		t.Fatalf("stable_since не обновился: %s", p.StableSince)
	}
}

func TestSnapshotInputChangeSends(t *testing.T) {
	clk := clock.NewVirtual(t0)
	s := newSnapshotter(clk, "0.24.4")
	info := PaneInfo{PaneID: "%10", SID: "S1", Session: "sess1"}

	_, _ = s.sync(info, detect.Idle, detect.Hash(idleScreen), "", t0)
	p, changed := s.sync(info, detect.Idle, detect.Hash(idleInputScreen),
		"Однострочный тестовый промпт", t0.Add(time.Second))
	if !changed {
		t.Fatal("смена ввода обязан отправить")
	}
	if p.InputEmpty {
		t.Fatal("ввод непустой: input_empty = true")
	}
	if !strings.Contains(p.InputPreview, "тестовый промпт") {
		t.Fatalf("input_preview = %q", p.InputPreview)
	}
}

func TestSnapshotForgetsGonePanels(t *testing.T) {
	clk := clock.NewVirtual(t0)
	s := newSnapshotter(clk, "0.24.4")
	s.sync(PaneInfo{PaneID: "%10"}, detect.Idle, detect.Hash(idleScreen), "", t0)
	s.forget("%10")
	if len(s.panes) != 0 {
		t.Fatal("забытая панель осталась в состоянии")
	}
}

// protoStableSince — дублирует форматирование для проверок.
func protoStableSince(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}
