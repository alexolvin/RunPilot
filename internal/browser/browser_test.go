package browser

import (
	"context"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"runpilot/internal/clock"
	"runpilot/internal/config"
)

// testBrowser — конфиг браузера на temp-профиле и свободном дисплее.
func testBrowser(t *testing.T) *Browser {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Defaults().Browser
	cfg.ProfileDir = filepath.Join(dir, "profile")
	cfg.Display = ":97" // свободный дисплей для теста
	b := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), clock.NewReal())
	t.Cleanup(func() { b.Stop() })
	return b
}

// TestBrowserStartStop — старт Xvfb+Chrome ≤ 10 с, вкладка сессии, стоп (CONTROL R1).
func TestBrowserStartStop(t *testing.T) {
	b := testBrowser(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := b.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !b.Running() {
		t.Fatal("браузер не запущен")
	}
	// Вкладка сессии (свой профиль, своя авторизация).
	const sid = "sess-test"
	id, err := b.OpenTab(context.Background(), sid, "about:blank")
	if err != nil {
		t.Fatalf("OpenTab: %v", err)
	}
	if b.TabPage(id) == nil {
		t.Fatal("TabPage пуст")
	}
	if tabs := b.Tabs(); len(tabs) != 1 {
		t.Fatalf("вкладок = %d, хочу 1", len(tabs))
	}
	// Профиль сессии 0700 (CONTROL R1).
	profile := filepath.Join(b.cfg.ProfileDir, sid)
	fi, err := os.Stat(profile)
	if err != nil {
		t.Fatalf("профиль: %v", err)
	}
	if fi.Mode().Perm() != 0o700 {
		t.Fatalf("права профиля = %o, хочу 0700", fi.Mode().Perm())
	}
	// DevTools только на loopback (CONTROL R1: ss -ltnp → 127.0.0.1).
	b.mu.Lock()
	ctrl := b.sessions[sid].controlURL
	b.mu.Unlock()
	host, _, err := net.SplitHostPort(strings.TrimPrefix(devtoolsBase(ctrl), httpScheme))
	if err != nil || host != loopbackHost {
		t.Fatalf("DevTools host = %q (err %v), хочу 127.0.0.1", host, err)
	}
	b.CloseTab(id)
	b.Stop()
	if b.Running() {
		t.Fatal("браузер не остановлен")
	}
}

// TestBrowserWatchdogRestore — kill -9 Chrome сессии → сторож восстанавливает ≤ 15 с
// (CONTROL R1).
func TestBrowserWatchdogRestore(t *testing.T) {
	b := testBrowser(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := b.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	const sid = "sess-watch"
	if _, err := b.OpenTab(context.Background(), sid, "about:blank"); err != nil {
		t.Fatalf("OpenTab: %v", err)
	}
	// kill -9 основного процесса Chrome сессии.
	b.mu.Lock()
	pid := b.sessions[sid].launcher.PID()
	b.mu.Unlock()
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatalf("kill: %v", err)
	}
	// Ждём восстановления: сторож (опрос 1 с + backoff 1 с + перезапуск).
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		b.mu.Lock()
		var ctrl string
		if s, ok := b.sessions[sid]; ok {
			ctrl = s.controlURL
		}
		b.mu.Unlock()
		if ctrl != "" && b.chromeAlive(ctrl) {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatal("Chrome не восстановлен за 15 с")
}
