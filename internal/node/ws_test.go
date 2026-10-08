package node

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"runpilot/internal/clock"
	"runpilot/internal/proto"

	"github.com/gorilla/websocket"
)

// wsTestLog — discard-лог для тестов WS-клиента.
var wsTestLog = slog.New(slog.NewTextHandler(io.Discard, nil))

// TestWSClientSelfUpdateExits — 14.2 ТЗ: успешный self-update → бинарник
// заменён, Run() возвращает ErrUpdated (процесс завершится кодом 75,
// systemd перезапустит новый бинарник). Без ErrUpdated узел бесконечно
// переподключался бы старым кодом (defect, найден на W9).
func TestWSClientSelfUpdateExits(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "runpilot")
	if err := os.WriteFile(bin, []byte("OLD-BIN"), 0o755); err != nil {
		t.Fatal(err)
	}
	newContent := []byte("NEW-BIN")
	sum := sha256.Sum256(newContent)

	// Дистрибутив: координатор отдаёт новый бинарник.
	dist := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer node-tok" {
			t.Errorf("download без Bearer-токена узла: %q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write(newContent)
	}))
	defer dist.Close()

	// Координатор (WS): hello + panes → команда update → ждём ответ UPDATED.
	updated := make(chan string, 1)
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		for i := 0; i < 2; i++ { // hello, panes
			if _, _, err := c.ReadMessage(); err != nil {
				return
			}
		}
		m := proto.New(proto.KindUpdate)
		m.CmdID = "u1"
		m.Version = "9.9.9"
		m.URL = dist.URL
		m.SHA256 = hex.EncodeToString(sum[:])
		if err := c.WriteJSON(m); err != nil {
			return
		}
		// Ответ на update придёт не сразу: до него идут node_health и
		// снимки панелей от цикла узла — читаем до KindReply с CmdID "u1".
		for {
			var rep proto.Msg
			if err := c.ReadJSON(&rep); err != nil {
				return
			}
			if rep.Type == proto.KindReply && rep.CmdID == "u1" {
				updated <- rep.Result
				return
			}
		}
	}))
	defer srv.Close()

	withSelftest(t, func(_ context.Context, _ string, _ time.Duration) error { return nil })

	ex := &fakeExec{}
	n := newTestNode(t, ex)
	n.SetBinPath(bin)
	n.SetNodeToken("node-tok")

	ws := NewWSClient("ws"+srv.URL[len("http"):], "tok", "old", clock.NewReal(), wsTestLog, n)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- ws.Run(ctx) }()

	select {
	case res := <-updated:
		if res != proto.ResUpdated {
			t.Fatalf("ответ координатору = %q, хочу UPDATED", res)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("координатор не получил ответ UPDATED за 10 с")
	}

	select {
	case err := <-errCh:
		if !errors.Is(err, ErrUpdated) {
			t.Fatalf("Run() = %v, хочу ErrUpdated", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run() не вернулся после успешного self-update")
	}

	if got, _ := os.ReadFile(bin); string(got) != "NEW-BIN" {
		t.Errorf("бинарник не заменён: %q", got)
	}
}

// TestWSClientBackoffResetOnHello — задержка переподключения сбрасывается к
// 1 с после успешного hello: 6 отказов dial (backoff 1→2→4→8→16→30 с),
// затем успешная сессия (hello) + обрыв → 8-е подключение через 1 с (не 30).
// Без сброса 8-е подключение потребовало бы 30 с вирт. времени.
func TestWSClientBackoffResetOnHello(t *testing.T) {
	clk := clock.NewVirtual(time.Unix(0, 0).UTC())
	var connCount int32
	connEvents := make(chan int32, 32)
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&connCount, 1)
		connEvents <- n
		if n <= 6 {
			// отказ dial (координатор «не поднят»)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		// hello + panes, потом обрыв (defer c.Close) — рестарт координатора.
		if _, _, err := c.ReadMessage(); err != nil {
			return
		}
		if _, _, err := c.ReadMessage(); err != nil {
			return
		}
	}))
	defer srv.Close()

	withSelftest(t, func(_ context.Context, _ string, _ time.Duration) error { return nil })
	ex := &fakeExec{}
	n := newTestNode(t, ex)
	ws := NewWSClient("ws"+srv.URL[len("http"):], "tok", "old", clk, wsTestLog, n)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = ws.Run(ctx) }()

	// advanceUntil — сдвигать вирт. часы (шаги 100 мс), пока не придёт
	// соединение `want` или не исчерпан `maxV` вирт. времени. Устойчиво к
	// гонке: таймер ставится горутиной асинхронно, цикл ловит его на любом шаге.
	advanceUntil := func(want int32, maxV time.Duration) time.Time {
		t.Helper()
		start := clk.Now()
		realDeadline := time.Now().Add(5 * time.Second)
		for clk.Now().Sub(start) < maxV && time.Now().Before(realDeadline) {
			select {
			case nn := <-connEvents:
				if nn == want {
					return clk.Now()
				}
			default:
			}
			clk.Advance(100 * time.Millisecond)
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("подключение %d не произошло за %s вирт. времени", want, maxV)
		return time.Time{}
	}

	// 6 отказов dial: backoff 1→2→4→8→16→30 с. maxV — backoff + запас.
	maxV := []time.Duration{2, 4, 6, 10, 18, 35}
	for i := 1; i <= 6; i++ {
		advanceUntil(int32(i), maxV[i-1]*time.Second)
	}
	// 7-е: успешная сессия (hello прошёл), обрыв.
	t7 := advanceUntil(7, 35*time.Second)
	// backoff сброшен к 1 с: 8-е подключение в пределах 2 с вирт. времени от
	// 7-го. Без сброса потребовалось бы 30 с.
	got8 := false
	realDeadline := time.Now().Add(5 * time.Second)
	for clk.Now().Sub(t7) < 2*time.Second && time.Now().Before(realDeadline) {
		select {
		case nn := <-connEvents:
			if nn == 8 {
				got8 = true
			}
		default:
		}
		clk.Advance(100 * time.Millisecond)
		time.Sleep(5 * time.Millisecond)
	}
	if !got8 {
		t.Fatal("8-е подключение не произошло за 2 с вирт. времени — backoff не сброшен к 1 с")
	}
}
