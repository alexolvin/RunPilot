package api

// Терминал в браузере (13.4 ТЗ): юнит-тесты моста координатора —
// закрытие по бездействию на ВИРТУАЛЬНЫХ часах, лимит на узел, реестр.

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"runpilot/internal/clock"
	"runpilot/internal/config"
	"runpilot/internal/proto"
	"runpilot/internal/store"

	"github.com/gorilla/websocket"
)

// termServer — координатор с заданными часами (виртуальными для idle-теста).
func termServer(t *testing.T, clk clock.Clock) *Server {
	t.Helper()
	dbDir := t.TempDir()
	st, err := store.Open(filepath.Join(dbDir, "runpilot.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	cfg := config.Defaults()
	hub := NewHub(clk)
	srv := NewServer(cfg, st, hub, clk, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return srv
}

// testBrowserWS — пар браузерских conn (серверная + клиентская) для терминала.
// Серверная (server) — то, что в реальном потоке является ts.conn.
func testBrowserWS(t *testing.T) (*websocket.Conn, *websocket.Conn) {
	t.Helper()
	up := websocket.Upgrader{}
	holder := make(chan *websocket.Conn, 1)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		holder <- c
		select {} // держать открытым до закрытия теста
	}))
	t.Cleanup(ts.Close)
	client, _, err := websocket.DefaultDialer.Dial("ws"+ts.URL[len("http"):], nil)
	if err != nil {
		t.Fatal(err)
	}
	server := <-holder
	t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
	return server, client
}

// TestTermIdleReapVirtualClock — бездействие дольше terminal_idle_min → закрытие
// (CONTROL W8: «закрытие по бездействию на виртуальных часах»).
func TestTermIdleReapVirtualClock(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clk := clock.NewVirtual(start)
	srv := termServer(t, clk) // TerminalIdleMin = 30 по умолчанию

	server, client := testBrowserWS(t)
	now := clk.Now()
	ts := &terminalSession{chan_: 1, host: "h1", sid: "s1", actor: "op", conn: server}
	ts.openedAt = now
	ts.last.Store(now.UnixNano())
	srv.registerTerminal(ts)

	// 29 мин — ещё не idle.
	clk.Advance(29 * time.Minute)
	if got := srv.reapIdleTerminals(); len(got) != 0 {
		t.Fatalf("до idle закрыто %v, хочу 0", got)
	}
	// +2 мин = 31 мин > 30 → закрытие.
	clk.Advance(2 * time.Minute)
	if got := srv.reapIdleTerminals(); len(got) != 1 || got[0] != 1 {
		t.Fatalf("после idle закрыто %v, хочу [1]", got)
	}
	srv.termMu.Lock()
	left := len(srv.terminals)
	srv.termMu.Unlock()
	if left != 0 {
		t.Fatalf("в реестре %d, хочу 0", left)
	}
	// Браузер получил close-кадр.
	client.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, _, err := client.ReadMessage()
	if err == nil {
		t.Fatalf("браузер не получил close после idle")
	}
	var cerr *websocket.CloseError
	if !errors.As(err, &cerr) {
		t.Fatalf("ошибка чтения = %v, хочу CloseError", err)
	}
}

// TestTermReapTouchKeepsAlive — активность (touch) сбрасывает таймер бездействия.
func TestTermReapTouchKeepsAlive(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clk := clock.NewVirtual(start)
	srv := termServer(t, clk)

	server, _ := testBrowserWS(t)
	now := clk.Now()
	ts := &terminalSession{chan_: 7, host: "h1", sid: "s1", actor: "op", conn: server}
	ts.openedAt = now
	ts.last.Store(now.UnixNano())
	srv.registerTerminal(ts)

	// 29 мин простоя, затем активность → таймер обнулится.
	clk.Advance(29 * time.Minute)
	srv.touchTerminal(ts)
	clk.Advance(29 * time.Minute) // ещё 29 от момента активности
	if got := srv.reapIdleTerminals(); len(got) != 0 {
		t.Fatalf("после touch закрыто %v, хочу 0 (активность обновила таймер)", got)
	}
}

// TestTermLimitPerNode — превышение terminals_per_node_max → 503 TERMINAL_LIMIT
// (CONTROL W8: «превышение web.terminals_per_node_max → отказ с кодом»).
func TestTermLimitPerNode(t *testing.T) {
	srv := termServer(t, clock.NewReal())
	srv.termMaxPerNode = 2
	// Панель сессии s1 на узле h1 (чтобы PaneBySID нашлась).
	srv.hub.SetPane("h1", proto.Pane{PaneID: "%0", SID: "s1", State: "IDLE"})
	// Два терминала уже открыты на h1.
	for _, c := range []int{1, 2} {
		srv.registerTerminal(&terminalSession{chan_: c, host: "h1", sid: "s1"})
	}
	if got := srv.termCountForNode("h1"); got != 2 {
		t.Fatalf("termCountForNode = %d, хочу 2", got)
	}
	// Третий терминал узла → отказ ДО апгрейда (обычный HTTP 503).
	w := httptest.NewRecorder()
	srv.TerminalWS(w, &http.Request{}, "s1", "op")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("статус = %d, хочу 503", w.Code)
	}
	if srv.termCountForNode("h1") != 2 {
		t.Fatalf("лимит не должен регистрировать терминал; count изменился")
	}
}

// TestTermPaneNotFound — нет панели сессии → 404 PANE_NOT_FOUND.
func TestTermPaneNotFound(t *testing.T) {
	srv := termServer(t, clock.NewReal())
	w := httptest.NewRecorder()
	srv.TerminalWS(w, &http.Request{}, "no-such-sid", "op")
	if w.Code != http.StatusNotFound {
		t.Fatalf("статус = %d, хочу 404", w.Code)
	}
}

// TestTermClosePtyIdempotent — повторное closePty не меняет состояние.
func TestTermClosePtyIdempotent(t *testing.T) {
	srv := termServer(t, clock.NewReal())
	server, _ := testBrowserWS(t)
	ts := &terminalSession{chan_: 3, host: "h1", sid: "s1", actor: "op", conn: server}
	ts.openedAt = clock.NewReal().Now()
	ts.last.Store(ts.openedAt.UnixNano())
	srv.registerTerminal(ts)

	srv.closePty(3)
	srv.closePty(3) // повторно — без паники
	srv.termMu.Lock()
	left := len(srv.terminals)
	srv.termMu.Unlock()
	if left != 0 {
		t.Fatalf("в реестре %d, хочу 0", left)
	}
}
