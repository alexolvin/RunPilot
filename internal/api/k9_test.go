package api

// v2 (K9): лимит одновременных SSE-клиентов (web.sse_clients_max) —
// при достижении лимита новое подключение отклоняется 503 SSE_LIMIT.

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"runpilot/internal/clock"
	"runpilot/internal/config"
	"runpilot/internal/store"
)

func TestSSERateLimit(t *testing.T) {
	cfg := config.Defaults()
	cfg.Web.SSEClientsMax = 1
	st, err := store.Open(filepath.Join(t.TempDir(), "runpilot.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	clk := clock.NewReal()
	srv := NewServer(cfg, st, NewHub(clk), clk,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() { ts.Close() })

	// Первый клиент — 200 (лимит 1).
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/events", nil)
	req.Header.Set("Accept", "text/event-stream")
	resp1, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp1.Body.Close()
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("первый клиент: статус %d, хочу 200", resp1.StatusCode)
	}
	// Второй клиент — 503 SSE_LIMIT (лимит исчерпан).
	resp2, err := http.Get(ts.URL + "/api/v1/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("второй клиент: статус %d, хочу 503 (SSE_LIMIT)", resp2.StatusCode)
	}
}
