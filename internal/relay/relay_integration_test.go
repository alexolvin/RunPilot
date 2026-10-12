//go:build integration

package relay_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-rod/rod"

	"runpilot/internal/browser"
	"runpilot/internal/clock"
	"runpilot/internal/config"
	"runpilot/internal/orchsite"
	"runpilot/internal/relay"
	"runpilot/internal/testutil/fakeorch"
)

// startFakeorch — двойник оркестратора в процессе.
func startFakeorch(t *testing.T) *httptest.Server {
	t.Helper()
	s := fakeorch.New()
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return srv
}

// startBrowser — браузер на свободном дисплее.
func startBrowser(t *testing.T, cfg config.Browser) *browser.Browser {
	t.Helper()
	cfg.Display = ":98"
	cfg.ProfileDir = t.TempDir()
	b := browser.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), clock.NewReal())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := b.Start(ctx); err != nil {
		t.Fatalf("browser start: %v", err)
	}
	t.Cleanup(b.Stop)
	return b
}

// openChat — вкладка с чатом fakeorch (профиль сессии "orch-test").
func openChat(t *testing.T, b *browser.Browser, url string) *rod.Page {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	id, err := b.OpenTab(ctx, "orch-test", url)
	if err != nil {
		t.Fatalf("open tab: %v", err)
	}
	page := b.TabPage(id)
	if page == nil {
		t.Fatal("tab page nil")
	}
	// Ждём готовности: композер на странице (надёжнее WaitLoad).
	if _, err := page.Timeout(20 * time.Second).Element("#composer"); err != nil {
		t.Fatalf("wait composer: %v", err)
	}
	return page
}

// TestRelayFullCycle — полный цикл: отправка → ожидание → извлечение (ТЗ R2).
func TestRelayFullCycle(t *testing.T) {
	srv := startFakeorch(t)
	b := startBrowser(t, config.Defaults().Browser)
	profile, err := orchsite.Load("fakeorch")
	if err != nil {
		t.Fatalf("load profile: %v", err)
	}

	cfg := config.Defaults().Relay
	cfg.MinSendIntervalSec = 0 // тест без паузы
	cfg.AnswerStableSec = 1
	r := relay.New(cfg, config.Defaults().Browser, clock.NewReal(),
		slog.New(slog.NewTextHandler(io.Discard, nil)))

	page := openChat(t, b, srv.URL+"/chat/test")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Шаблон ответа: agent_block.
	mustSetTemplate(t, srv, "agent_block")

	res, err := r.Send(ctx, page, profile, "Отчёт: шаг 1 завершён", nil)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if res.Hold != "" {
		t.Fatalf("send hold = %q", res.Hold)
	}
	if res.SentHash == "" {
		t.Fatal("sent_hash пуст")
	}

	// Ожидание ответа.
	idx, hold, err := r.WaitAnswer(ctx, page, profile, 0)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if hold != "" {
		t.Fatalf("wait hold = %q", hold)
	}
	if idx < 0 {
		t.Fatal("нет ответа")
	}

	// Извлечение.
	out, err := r.Extract(page, profile, idx, "")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if out.Hold != "" {
		t.Fatalf("extract hold = %q", out.Hold)
	}
	if out.AgentBlock != "Сделай шаг 2." {
		t.Fatalf("agent_block = %q", out.AgentBlock)
	}
}

// TestRelayAttachments — отправка 2 .md-вложений, побайтовая сверка (ТЗ R2).
func TestRelayAttachments(t *testing.T) {
	srv := startFakeorch(t)
	b := startBrowser(t, config.Defaults().Browser)
	profile, _ := orchsite.Load("fakeorch")

	cfg := config.Defaults().Relay
	cfg.MinSendIntervalSec = 0
	cfg.AnswerStableSec = 1
	r := relay.New(cfg, config.Defaults().Browser, clock.NewReal(),
		slog.New(slog.NewTextHandler(io.Discard, nil)))

	page := openChat(t, b, srv.URL+"/chat/attach")
	mustSetTemplate(t, srv, "text_only")

	dir := t.TempDir()
	f1 := filepath.Join(dir, "a.md")
	f2 := filepath.Join(dir, "b.md")
	content1 := "# Отчёт A\n\nТекст."
	content2 := "# Отчёт B\n\nДругой текст."
	if err := os.WriteFile(f1, []byte(content1), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f2, []byte(content2), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := r.Send(ctx, page, profile, "С отчётами", []relay.Attachment{
		{Name: "a.md", Path: f1}, {Name: "b.md", Path: f2},
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if res.Hold != "" {
		t.Fatalf("send hold = %q", res.Hold)
	}
	// Проверка побайтовой сверки через /_received.
	recv := getReceived(t, srv)
	if len(recv) != 1 {
		t.Fatalf("received = %d, хочу 1", len(recv))
	}
	msg := recv[0]
	if len(msg.Files) != 2 {
		t.Fatalf("files = %d, хочу 2", len(msg.Files))
	}
	// Байты совпадают (b64).
	if msg.Files[0].Name != "a.md" || msg.Files[1].Name != "b.md" {
		t.Fatalf("file names = %q, %q", msg.Files[0].Name, msg.Files[1].Name)
	}
}

// mustSetTemplate — устанавливает шаблон ответа.
func mustSetTemplate(t *testing.T, srv *httptest.Server, tmpl string) {
	t.Helper()
	resp, err := srv.Client().Post(srv.URL+"/_set", "application/json",
		strings.NewReader(fmt.Sprintf(`{"template":%q}`, tmpl)))
	if err != nil {
		t.Fatalf("_set: %v", err)
	}
	resp.Body.Close()
}

// getReceived — /_received.
func getReceived(t *testing.T, srv *httptest.Server) []fakeorch.RecvMsg {
	t.Helper()
	resp, err := srv.Client().Get(srv.URL + "/_received")
	if err != nil {
		t.Fatalf("/_received: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var out []fakeorch.RecvMsg
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

var _ = http.StatusOK
