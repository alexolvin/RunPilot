package node

// node_health узла (N9/N10, 14.4): узел шлёт {tmux_version, qwen_path,
// qwen_version, disk_free_mb, time}; период node.health_interval_sec.

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"runpilot/internal/clock"
	"runpilot/internal/config"
	"runpilot/internal/proto"
	"runpilot/profiles"
)

// newHealthNode — узел с виртуальными часами и fakeExec для node_health.
func newHealthNode(t *testing.T, ex *fakeExec, clk *clock.Virtual) *Node {
	t.Helper()
	cfg := config.Defaults()
	cfg.Node.TmuxSockets = []string{"default"}
	prof, err := profiles.LoadEmbeddedQwen()
	if err != nil {
		t.Fatal(err)
	}
	n, err := New(cfg, prof, ex, clk, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestNodeHealthMsg(t *testing.T) {
	ex := &fakeExec{respond: func(name string, args ...string) (string, error) {
		switch {
		case name == "tmux" && strings.Contains(strings.Join(args, " "), "-V"):
			return "tmux 3.4\n", nil
		case name == "which" && len(args) > 0 && args[0] == "qwen":
			return "/usr/local/bin/qwen\n", nil
		}
		return "", nil
	}}
	clk := clock.NewVirtual(time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC))
	n := newHealthNode(t, ex, clk)

	var got proto.Msg
	n.sendHealth(context.Background(), func(m proto.Msg) error { got = m; return nil })
	if got.Type != proto.KindNodeHealth {
		t.Fatalf("type=%q, хочу node_health", got.Type)
	}
	if got.TmuxVersion != "tmux 3.4" {
		t.Fatalf("tmux_version=%q, хочу \"tmux 3.4\"", got.TmuxVersion)
	}
	if got.QwenPath != "/usr/local/bin/qwen" {
		t.Fatalf("qwen_path=%q, хочу /usr/local/bin/qwen", got.QwenPath)
	}
	if got.NodeTimeMS != clk.Now().UnixMilli() {
		t.Fatalf("time=%d, хочу %d", got.NodeTimeMS, clk.Now().UnixMilli())
	}
	if got.DiskFreeMB < 0 {
		t.Fatalf("disk_free_mb=%d, хочу >=0", got.DiskFreeMB)
	}
}

// TestNodeHealthMissing — tmux/qwen нет: пустые tmux_version/qwen_path (N9:
// координатор выводит причину неактивности).
func TestNodeHealthMissing(t *testing.T) {
	ex := &fakeExec{respond: func(name string, args ...string) (string, error) {
		return "", errors.New("not found")
	}}
	clk := clock.NewVirtual(time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC))
	n := newHealthNode(t, ex, clk)
	var got proto.Msg
	n.sendHealth(context.Background(), func(m proto.Msg) error { got = m; return nil })
	if got.TmuxVersion != "" {
		t.Fatalf("tmux_version=%q, хочу пусто (нет tmux)", got.TmuxVersion)
	}
	if got.QwenPath != "" {
		t.Fatalf("qwen_path=%q, хочу пусто (нет qwen)", got.QwenPath)
	}
}

// TestNodeHealthInterval — node_health раз в health_interval_sec (14.4):
// в пределах интервала повтор не шлётся, после — шлётся.
func TestNodeHealthInterval(t *testing.T) {
	ex := &fakeExec{}
	clk := clock.NewVirtual(time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC))
	n := newHealthNode(t, ex, clk)
	iv := n.healthIv
	if iv == 0 {
		t.Fatal("healthIv не задан")
	}
	n.lastHealth = clk.Now()
	sends := 0
	// В пределах интервала — без отправки.
	n.maybeHealth(context.Background(), func(m proto.Msg) error { sends++; return nil })
	if sends != 0 {
		t.Fatalf("в пределах интервала: отправка %d, хочу 0", sends)
	}
	// После интервала — отправка.
	clk.Advance(iv + time.Second)
	n.maybeHealth(context.Background(), func(m proto.Msg) error { sends++; return nil })
	if sends != 1 {
		t.Fatalf("после интервала: отправка %d, хочу 1", sends)
	}
}
