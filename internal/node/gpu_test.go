package node

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"runpilot/internal/clock"
	"runpilot/internal/config"
	"runpilot/internal/proto"
	"runpilot/profiles"
)

// startGPU — RunGPU в горутине; возвращает канал сообщений и cancel.
func startGPU(t *testing.T, gpu, server string, ex *fakeExec) (<-chan proto.Msg, context.CancelFunc) {
	t.Helper()
	cfg := config.Defaults()
	cfg.Node.GPU = gpu
	cfg.Node.GPUServer = server
	cfg.Monitor.MetricsIntervalSec = 60 // второй цикл за время теста не успевает
	prof, err := profiles.LoadEmbeddedQwen()
	if err != nil {
		t.Fatal(err)
	}
	n, err := New(cfg, prof, ex, clock.NewReal(),
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	ch := make(chan proto.Msg, 4)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- n.RunGPU(ctx, func(m proto.Msg) error {
			ch <- m
			return nil
		})
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return ch, cancel
}

// TestRunGUNvidia — фиксированная команда nvidia-smi (без оболочки),
// разбор, KindGPU{server, cards} (2 карты, MiB→GB).
func TestRunGUNvidia(t *testing.T) {
	ex := &fakeExec{}
	ex.respond = func(name string, args ...string) (string, error) {
		if name != "nvidia-smi" {
			t.Fatalf("команда = %s, хочу nvidia-smi", name)
		}
		joined := argsToString(args)
		if joined != "--query-gpu=index,utilization.gpu,temperature.gpu,power.draw --format=csv,noheader,nounits" {
			t.Fatalf("аргументы = %q", joined)
		}
		return "0, 98, 65, 337.84\n1, 100, 63, 325.40\n", nil
	}
	ch, _ := startGPU(t, "nvidia", "node-a", ex)
	select {
	case m := <-ch:
		if m.Type != proto.KindGPU {
			t.Fatalf("type = %s, хочу gpu", m.Type)
		}
		if m.Server != "node-a" {
			t.Fatalf("server = %q, хочу node-a", m.Server)
		}
		if len(m.Cards) != 2 {
			t.Fatalf("карт = %d, хочу 2", len(m.Cards))
		}
		c0, c1 := m.Cards[0], m.Cards[1]
		if c0.Index != 0 || c0.UtilPercent != 98 || c0.TempC != 65 || c0.PowerW != 337 {
			t.Errorf("card0 = %+v", c0)
		}
		if c1.Index != 1 || c1.UtilPercent != 100 || c1.PowerW != 325 {
			t.Errorf("card1 = %+v", c1)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("KindGPU не получен")
	}
}

// TestRunGUAMd — rocm-smi --json.
func TestRunGUAMd(t *testing.T) {
	ex := &fakeExec{}
	ex.respond = func(name string, args ...string) (string, error) {
		if name != "rocm-smi" {
			t.Fatalf("команда = %s, хочу rocm-smi", name)
		}
		return `{"card0":{"GPU use (%)":87,"GPU temp (degC)":61,"GPU power (watts)":312.5}}`, nil
	}
	ch, _ := startGPU(t, "amd", "srv2", ex)
	select {
	case m := <-ch:
		if m.Server != "srv2" || len(m.Cards) != 1 {
			t.Fatalf("msg = %+v", m)
		}
		c := m.Cards[0]
		if c.UtilPercent != 87 || c.TempC != 61 || c.PowerW != 312 {
			t.Errorf("card = %+v", c)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("KindGPU не получен")
	}
}

// TestRunGPUError — ошибка команды → отправки нет (WARN, цикл жив).
func TestRunGPUError(t *testing.T) {
	ex := &fakeExec{}
	ex.respond = func(name string, args ...string) (string, error) {
		return "", errors.New("no devices found")
	}
	ch, _ := startGPU(t, "nvidia", "s", ex)
	select {
	case m := <-ch:
		t.Fatalf("при ошибке команды не шлём: %+v", m)
	case <-time.After(200 * time.Millisecond):
	}
	if ex.callCount("nvidia-smi") == 0 {
		t.Fatal("команда не запускалась")
	}
}

// TestGPUEnabled — none/пустое имя сервера → выключено.
func TestGPUEnabled(t *testing.T) {
	cases := []struct {
		gpu, server string
		want        bool
	}{
		{"nvidia", "s", true},
		{"amd", "s", true},
		{"none", "s", false},
		{"nvidia", "", false},
	}
	for _, c := range cases {
		cfg := config.Defaults()
		cfg.Node.GPU = c.gpu
		cfg.Node.GPUServer = c.server
		prof, err := profiles.LoadEmbeddedQwen()
		if err != nil {
			t.Fatal(err)
		}
		n, err := New(cfg, prof, &fakeExec{}, clock.NewReal(),
			slog.New(slog.NewTextHandler(io.Discard, nil)))
		if err != nil {
			t.Fatal(err)
		}
		if got := n.GPUEnabled(); got != c.want {
			t.Errorf("GPUEnabled(gpu=%q, server=%q) = %v, хочу %v", c.gpu, c.server, got, c.want)
		}
	}
}

func argsToString(args []string) string {
	out := ""
	for i, a := range args {
		if i > 0 {
			out += " "
		}
		out += a
	}
	return out
}
