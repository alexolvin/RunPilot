// Package browser — Xvfb + Google Chrome под управлением go-rod (ТЗ Relay,
// раздел 8). Координатор запускает браузер, когда включена хотя бы одна связь,
// и останавливает через browser.idle_stop_min после выключения последней.
// Сторож: падение Chrome или Xvfb → перезапуск с задержкой 1 → 30 с (×2),
// вкладки активных связей открываются заново.
package browser

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"

	"runpilot/internal/clock"
	"runpilot/internal/config"
)

// Tab — вкладка браузера (связь). URL хранится для повторного открытия
// после перезапуска (ТЗ Relay 8.1.5).
type Tab struct {
	ID   string
	Page *rod.Page
	URL  string
}

// Browser — менеджер Xvfb + Chrome.
type Browser struct {
	cfg config.Browser
	log *slog.Logger
	clk clock.Clock

	mu         sync.Mutex
	xvfb       *exec.Cmd
	xvfbDead   chan struct{}
	launcher   *launcher.Launcher
	rod        *rod.Browser
	controlURL string
	tabs       map[string]*Tab
	tabSeq     int
	running    bool
	stopCh     chan struct{}
}

// New — сборка менеджера браузера.
func New(cfg config.Browser, log *slog.Logger, clk clock.Clock) *Browser {
	return &Browser{
		cfg:  cfg,
		log:  log,
		clk:  clk,
		tabs: map[string]*Tab{},
	}
}

// Running — запущен ли браузер.
func (b *Browser) Running() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.running
}

// Start — запуск Xvfb + Chrome + сторож. Идемпотентно.
func (b *Browser) Start(ctx context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.running {
		return nil
	}
	if err := b.startLocked(ctx); err != nil {
		return err
	}
	b.stopCh = make(chan struct{})
	go b.watchdog(b.stopCh)
	return nil
}

// startLocked — запуск Xvfb + Chrome (mu удерживается). Сторож не стартует —
// его запускает Start или продолжающий сторож при restart.
func (b *Browser) startLocked(ctx context.Context) error {
	profile := expandTilde(b.cfg.ProfileDir)
	if err := os.MkdirAll(profile, dirPerm); err != nil {
		return fmt.Errorf("browser: каталог профиля: %w", err)
	}
	// Xvfb (отдельный процесс — сторож следит через Wait).
	xvfb := exec.Command(b.cfg.XvfbPath, b.cfg.Display, "-screen", "0", b.cfg.Screen)
	if err := xvfb.Start(); err != nil {
		return fmt.Errorf("browser: Xvfb: %w", err)
	}
	dead := make(chan struct{})
	go func() { _, _ = xvfb.Process.Wait(); close(dead) }()
	b.xvfb, b.xvfbDead = xvfb, dead
	if err := waitXvfb(ctx, b.cfg.Display); err != nil {
		b.teardown()
		return fmt.Errorf("browser: Xvfb не готов: %w", err)
	}
	// Chrome (go-rod launcher): без headless, свой профиль, DevTools на loopback.
	l := launcher.New().
		Bin(b.cfg.ChromePath).
		UserDataDir(profile).
		Headless(false).
		Set("window-size", windowSize(b.cfg.Screen))
	l.Env(append(os.Environ(), "DISPLAY="+b.cfg.Display)...)
	url, err := l.Launch()
	if err != nil {
		b.teardown()
		return fmt.Errorf("browser: Chrome: %w", err)
	}
	rodBrowser := rod.New().ControlURL(url)
	if err := rodBrowser.Connect(); err != nil {
		l.Kill()
		b.teardown()
		return fmt.Errorf("browser: rod-подключение: %w", err)
	}
	b.launcher, b.rod, b.controlURL = l, rodBrowser, url
	b.running = true
	b.log.Info("browser: запущен", "display", b.cfg.Display, "profile", profile)
	return nil
}

// Stop — остановка Chrome + Xvfb. Идемпотентно.
func (b *Browser) Stop() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.stopWatchdog()
	b.teardown()
	b.log.Info("browser: остановлен")
}

// stopWatchdog — сигнал сторожу остановиться (mu удерживается).
func (b *Browser) stopWatchdog() {
	if b.stopCh != nil {
		close(b.stopCh)
		b.stopCh = nil
	}
}

// teardown — снять процессы и вкладки (mu удерживается).
func (b *Browser) teardown() {
	b.running = false
	for id, t := range b.tabs {
		_ = t.Page.Close()
		delete(b.tabs, id)
	}
	if b.rod != nil {
		b.rod.Close()
		b.rod = nil
	}
	if b.launcher != nil {
		b.launcher.Kill()
		b.launcher = nil
	}
	b.controlURL = ""
	if b.xvfb != nil && b.xvfb.Process != nil {
		_ = b.xvfb.Process.Kill()
		b.xvfb = nil
	}
	b.xvfbDead = nil
}

// OpenTab — новая вкладка с URL. Возвращает ID.
func (b *Browser) OpenTab(ctx context.Context, url string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.running {
		return "", fmt.Errorf("browser: не запущен")
	}
	if len(b.tabs) >= b.cfg.MaxTabs {
		return "", fmt.Errorf("browser: превышен лимит вкладок %d", b.cfg.MaxTabs)
	}
	page, err := b.rod.Page(proto.TargetCreateTarget{})
	if err != nil {
		return "", fmt.Errorf("browser: вкладка: %w", err)
	}
	if url != "" {
		if err := page.Navigate(url); err != nil {
			_ = page.Close()
			return "", fmt.Errorf("browser: навигация: %w", err)
		}
	}
	b.tabSeq++
	id := "tab-" + strconv.Itoa(b.tabSeq)
	b.tabs[id] = &Tab{ID: id, Page: page, URL: url}
	return id, nil
}

// CloseTab — закрыть вкладку.
func (b *Browser) CloseTab(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if t, ok := b.tabs[id]; ok {
		_ = t.Page.Close()
		delete(b.tabs, id)
	}
}

// Tabs — список вкладок.
func (b *Browser) Tabs() []Tab {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Tab, 0, len(b.tabs))
	for _, t := range b.tabs {
		out = append(out, *t)
	}
	return out
}

// TabPage — страница вкладки по ID (nil, если нет).
func (b *Browser) TabPage(id string) *rod.Page {
	b.mu.Lock()
	defer b.mu.Unlock()
	if t, ok := b.tabs[id]; ok {
		return t.Page
	}
	return nil
}

// watchdog — сторож (ТЗ Relay 8.1.5): падение Chrome или Xvfb → перезапуск
// с задержкой 1 → 30 с (×2), вкладки открываются заново.
func (b *Browser) watchdog(stopCh <-chan struct{}) {
	ticker := time.NewTicker(watchInterval)
	defer ticker.Stop()
	backoff := restartBackoffBase
	for {
		select {
		case <-stopCh:
			return
		case <-ticker.C:
			b.mu.Lock()
			if !b.running {
				b.mu.Unlock()
				return
			}
			xvfbDead := b.xvfbDeadClosed()
			chromeDead := !b.chromeAlive()
			b.mu.Unlock()
			if !xvfbDead && !chromeDead {
				backoff = restartBackoffBase
				continue
			}
			b.log.Warn("browser: падение, перезапуск",
				"xvfb", xvfbDead, "chrome", chromeDead, "backoff", backoff.String())
			time.Sleep(backoff)
			b.restart()
			backoff *= restartBackoffStep
			if backoff > restartBackoffMax {
				backoff = restartBackoffMax
			}
		}
	}
}

// restart — перезапуск браузера + повторное открытие вкладок (mu не удерживает).
// Сторож не останавливается: вызывающий (сторож) продолжает мониторинг.
func (b *Browser) restart() {
	b.mu.Lock()
	var urls []string
	for _, t := range b.tabs {
		urls = append(urls, t.URL)
	}
	b.teardown()
	err := b.startLocked(context.Background())
	b.mu.Unlock()
	if err != nil {
		b.log.Error("browser: перезапуск не удался", "err", err)
		return
	}
	// Повторно открыть вкладки активных связей.
	for _, u := range urls {
		if _, err := b.OpenTab(context.Background(), u); err != nil {
			b.log.Warn("browser: вкладка не открыта после перезапуска", "err", err)
		}
	}
}

// xvfbDeadClosed — Xvfb завершён (mu удерживается).
func (b *Browser) xvfbDeadClosed() bool {
	if b.xvfbDead == nil {
		return true
	}
	select {
	case <-b.xvfbDead:
		return true
	default:
		return false
	}
}

// chromeAlive — жив ли Chrome: DevTools HTTP отвечает (mu удерживается).
func (b *Browser) chromeAlive() bool {
	base := devtoolsBase(b.controlURL)
	if base == "" {
		return false
	}
	client := &http.Client{Timeout: chromePollTimeout}
	resp, err := client.Get(base + versionPath)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return true
}

// waitXvfb — ждать, пока Xvfb примет соединения (появится X11-сокет).
func waitXvfb(ctx context.Context, display string) error {
	deadline := time.Now().Add(xvfbWaitMax)
	for time.Now().Before(deadline) {
		if xvfbReady(display) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(xvfbPoll):
		}
	}
	return fmt.Errorf("таймаут ожидания Xvfb %s", display)
}

// xvfbReady — X11-сокет дисплея существует.
func xvfbReady(display string) bool {
	num := strings.TrimPrefix(display, ":")
	_, err := net.Dial("unix", filepath.Join(x11UnixDir, "X"+num))
	if err == nil {
		return true
	}
	_, statErr := os.Stat(filepath.Join(x11UnixDir, "X"+num))
	return statErr == nil
}

// devtoolsBase — база DevTools HTTP из control URL go-rod (ws:// → http://).
func devtoolsBase(controlURL string) string {
	base := controlURL
	if i := strings.Index(base, devtoolsSuffix); i > 0 {
		base = base[:i]
	}
	base = strings.Replace(base, wssScheme, httpsScheme, 1)
	base = strings.Replace(base, wsScheme, httpScheme, 1)
	return base
}

// expandTilde — ~ → home.
func expandTilde(s string) string {
	if s == "~" || len(s) > 1 && s[:tildePrefixLen] == "~/" {
		home, err := os.UserHomeDir()
		if err != nil {
			return s
		}
		if s == "~" {
			return home
		}
		return filepath.Join(home, s[tildePrefixLen:])
	}
	return s
}

// windowSize — «ШxВxБит» → «Ш,В» для --window-size.
func windowSize(screen string) string {
	parts := strings.Split(screen, "x")
	if len(parts) >= minScreenParts {
		return parts[0] + "," + parts[1]
	}
	return screen
}
