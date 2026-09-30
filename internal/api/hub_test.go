package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"runpilot/internal/clock"
	"runpilot/internal/proto"

	"github.com/gorilla/websocket"
)

var testLog = slog.New(slog.NewTextHandler(io.Discard, nil))

// TestPingerSendsPing — ping координатор→узел доходит до узла (WS-кадр
// ping; клиент gorilla отвечает pong автоматически).
func TestPingerSendsPing(t *testing.T) {
	var pings atomic.Int32
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
		c.SetPingHandler(func(appData string) error {
			pings.Add(1)
			return nil
		})
		for {
			if _, _, err := c.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer srv.Close()

	client, _, err := websocket.DefaultDialer.Dial("ws"+srv.URL[len("http"):], nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	h := NewHub(clock.NewReal())
	h.Register(&NodeInfo{Host: "n1", HostIP: "127.0.0.1", Conn: client})
	h.pingAll(testLog)

	deadline := time.Now().Add(2 * time.Second)
	for pings.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if pings.Load() != 1 {
		t.Fatalf("ping-кадров = %d, хочу 1", pings.Load())
	}
}

// TestPingerNoNodes — пустой реестр: без ошибок.
func TestPingerNoNodes(t *testing.T) {
	h := NewHub(clock.NewReal())
	h.pingAll(testLog)
}

// TestHubGPU — KindGPU: server → карты, повторная отправка обновляет.
func TestHubGPU(t *testing.T) {
	h := NewHub(clock.NewReal())
	if _, ok := h.GPU("s"); ok {
		t.Fatal("GPU до SetGPU: хочу absence")
	}
	h.SetGPU("s", "host1", []proto.GPUCard{{Index: 0, UtilPercent: 50}})
	g, ok := h.GPU("s")
	if !ok {
		t.Fatal("GPU после SetGPU: absence")
	}
	if g.Host != "host1" || len(g.Cards) != 1 || g.Cards[0].UtilPercent != 50 {
		t.Fatalf("gpu = %+v", g)
	}
	// Обновление.
	h.SetGPU("s", "host1", []proto.GPUCard{{Index: 0, UtilPercent: 90}})
	g, _ = h.GPU("s")
	if g.Cards[0].UtilPercent != 90 {
		t.Fatalf("после обновления = %+v", g)
	}
	// Пустые аргументы — игнор (не затирает).
	h.SetGPU("", "host1", []proto.GPUCard{{Index: 0}})
	h.SetGPU("s", "host1", nil)
	g, _ = h.GPU("s")
	if g.Cards[0].UtilPercent != 90 {
		t.Fatalf("пустые SetGPU затёрли: %+v", g)
	}
}

// TestStartPingerStopsOnCancel — горутина завершается по ctx.
func TestStartPingerStopsOnCancel(t *testing.T) {
	h := NewHub(clock.NewReal())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		h.StartPinger(ctx, testLog)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("StartPinger не завершился по ctx")
	}
}
