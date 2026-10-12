// Package browser — Xvfb + Google Chrome под управлением go-rod (ТЗ Relay,
// раздел 8). Координатор запускает общий Xvfb, когда включена хотя бы одна
// связь; на каждую сессию (связь) — свой Chrome-процесс со своим профилем:
// авторизация в разрезе сессии, а не проекта (у каждой сессии свои аккаунт,
// провайдер, cookie). Профиль на диске, переживает рестарты.
// Сторож: падение Xvfb → перезапуск Xvfb + всех Chrome; падение Chrome сессии →
// перезапуск этой сессии. Задержка 1 → 30 с (×2), вкладки открываются заново.
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
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"

	"runpilot/internal/clock"
	"runpilot/internal/config"
)

// Tab — вкладка сессии (связь). ID = сессия; URL хранится для повторного
// открытия после перезапуска (ТЗ Relay 8.1.5).
type Tab struct {
	ID   string
	Page *rod.Page
	URL  string
}

// sessionBrowser — Chrome-процесс сессии: свой профиль (своя авторизация),
// свой DevTools-порт на loopback.
type sessionBrowser struct {
	sid        string
	profileDir string
	launcher   *launcher.Launcher
	rod        *rod.Browser
	controlURL string
	page       *rod.Page
	url        string
}

// Browser — менеджер: общий Xvfb + по одному Chrome-профилю на сессию.
type Browser struct {
	cfg config.Browser
	log *slog.Logger
	clk clock.Clock

	mu       sync.Mutex
	xvfb     *exec.Cmd
	xvfbDead chan struct{}
	xvfbUp   bool
	sessions map[string]*sessionBrowser
	stopCh   chan struct{}
}

// New — сборка менеджера браузера.
func New(cfg config.Browser, log *slog.Logger, clk clock.Clock) *Browser {
	return &Browser{
		cfg:      cfg,
		log:      log,
		clk:      clk,
		sessions: map[string]*sessionBrowser{},
	}
}

// Running — запущен ли общий Xvfb.
func (b *Browser) Running() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.xvfbUp
}

// Start — запуск Xvfb + сторожа. Идемпотентно. Chrome стартует по сессиям в
// OpenTab (свой профиль на сессию).
func (b *Browser) Start(ctx context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.xvfbUp {
		return nil
	}
	if err := b.startXvfbLocked(ctx); err != nil {
		return err
	}
	b.stopCh = make(chan struct{})
	go b.watchdog(b.stopCh)
	return nil
}

// startXvfbLocked — запуск Xvfb (mu удерживается). Сторож не стартует — его
// запускает Start или продолжающий сторож при restartAll.
func (b *Browser) startXvfbLocked(ctx context.Context) error {
	xvfb := exec.Command(b.cfg.XvfbPath, b.cfg.Display, "-screen", "0", b.cfg.Screen)
	if err := xvfb.Start(); err != nil {
		return fmt.Errorf("browser: Xvfb: %w", err)
	}
	dead := make(chan struct{})
	go func() { _, _ = xvfb.Process.Wait(); close(dead) }()
	b.xvfb, b.xvfbDead = xvfb, dead
	if err := waitXvfb(ctx, b.cfg.Display); err != nil {
		b.teardownXvfb()
		return fmt.Errorf("browser: Xvfb не готов: %w", err)
	}
	b.xvfbUp = true
	b.log.Info("browser: Xvfb запущен", "display", b.cfg.Display)
	return nil
}

// Stop — остановка всех Chrome-профилей + Xvfb. Идемпотентно.
func (b *Browser) Stop() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.stopWatchdog()
	b.teardownAll()
	b.log.Info("browser: остановлен")
}

// stopWatchdog — сигнал сторожу остановиться (mu удерживается).
func (b *Browser) stopWatchdog() {
	if b.stopCh != nil {
		close(b.stopCh)
		b.stopCh = nil
	}
}

// teardownAll — снять все Chrome + Xvfb (mu удерживается).
func (b *Browser) teardownAll() {
	for sid, s := range b.sessions {
		b.teardownSessionLocked(s)
		delete(b.sessions, sid)
	}
	b.teardownXvfb()
}

// teardownSessionLocked — снять Chrome сессии (mu удерживается).
func (b *Browser) teardownSessionLocked(s *sessionBrowser) {
	if s.page != nil {
		_ = s.page.Close()
		s.page = nil
	}
	if s.rod != nil {
		s.rod.Close()
		s.rod = nil
	}
	if s.launcher != nil {
		s.launcher.Kill()
		s.launcher = nil
	}
	s.controlURL = ""
}

// teardownXvfb — снять Xvfb (mu удерживается).
func (b *Browser) teardownXvfb() {
	if b.xvfb != nil && b.xvfb.Process != nil {
		_ = b.xvfb.Process.Kill()
		b.xvfb = nil
	}
	b.xvfbDead = nil
	b.xvfbUp = false
}

// OpenTab — открыть вкладку сессии (свой профиль, своя авторизация). sid —
// сессия, она же ID вкладки. Chrome сессии стартует при первом открытии.
// Навигация — в фоне: тяжёлый SPA может не слать load-событие, и ожидание
// загрузки заблокировало бы b.mu на время загрузки. Оператор видит загрузку в
// живом просмотре.
func (b *Browser) OpenTab(ctx context.Context, sid, url string) (string, error) {
	if !safeSID.MatchString(sid) {
		return "", fmt.Errorf("browser: недопустимый ID сессии %q", sid)
	}
	b.mu.Lock()
	if !b.xvfbUp {
		b.mu.Unlock()
		return "", fmt.Errorf("browser: не запущен")
	}
	if len(b.sessions) >= b.cfg.MaxTabs {
		b.mu.Unlock()
		return "", fmt.Errorf("browser: превышен лимит сессий %d", b.cfg.MaxTabs)
	}
	s, ok := b.sessions[sid]
	if !ok {
		var err error
		s, err = b.startSessionLocked(ctx, sid)
		if err != nil {
			b.mu.Unlock()
			return "", err
		}
		b.sessions[sid] = s
	}
	s.url = url
	page := s.page
	b.mu.Unlock()

	if url != "" {
		go b.navigate(page, url)
	}
	return sid, nil
}

// startSessionLocked — запустить Chrome сессии со своим профилем (mu
// удерживается). Профиль: ProfileDir/<sid>, режим 0700, переживает рестарты.
func (b *Browser) startSessionLocked(ctx context.Context, sid string) (*sessionBrowser, error) {
	profile := filepath.Join(expandTilde(b.cfg.ProfileDir), sid)
	if err := os.MkdirAll(profile, dirPerm); err != nil {
		return nil, fmt.Errorf("browser: каталог профиля %s: %w", sid, err)
	}
	l := launcher.New().
		Bin(b.cfg.ChromePath).
		UserDataDir(profile).
		Headless(false).
		Leakless(false).
		Set("window-size", windowSize(b.cfg.Screen))
	if b.cfg.Proxy != "" {
		l.Set("proxy-server", b.cfg.Proxy)
	}
	l.Env(append(os.Environ(), "DISPLAY="+b.cfg.Display)...)
	url, err := l.Launch()
	if err != nil {
		return nil, fmt.Errorf("browser: Chrome %s: %w", sid, err)
	}
	rodBrowser := rod.New().ControlURL(url)
	if err := rodBrowser.Connect(); err != nil {
		l.Kill()
		return nil, fmt.Errorf("browser: rod-подключение %s: %w", sid, err)
	}
	page, err := rodBrowser.Page(proto.TargetCreateTarget{})
	if err != nil {
		rodBrowser.Close()
		l.Kill()
		return nil, fmt.Errorf("browser: вкладка %s: %w", sid, err)
	}
	b.log.Info("browser: сессия запущена", "sid", sid, "profile", profile)
	return &sessionBrowser{
		sid:        sid,
		profileDir: profile,
		launcher:   l,
		rod:        rodBrowser,
		controlURL: url,
		page:       page,
	}, nil
}

// navigate — фоновая навигация вкладки (таймаут полной загрузки из
// конфигурации; ошибка не критична — вкладка открыта, оператор видит состояние
// в просмотре).
func (b *Browser) navigate(page *rod.Page, url string) {
	if err := page.Timeout(b.pageLoadTimeout()).Navigate(url); err != nil {
		b.log.Warn("browser: навигация не удалась", "url", url, "err", err)
	}
}

// pageLoadTimeout — таймаут полной загрузки страницы (browser.page_load_timeout_sec).
func (b *Browser) pageLoadTimeout() time.Duration {
	return time.Duration(b.cfg.PageLoadTimeoutSec) * time.Second
}

// CloseTab — закрыть вкладку сессии (остановить её Chrome).
func (b *Browser) CloseTab(sid string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if s, ok := b.sessions[sid]; ok {
		b.teardownSessionLocked(s)
		delete(b.sessions, sid)
	}
}

// Tabs — список вкладок сессий.
func (b *Browser) Tabs() []Tab {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Tab, 0, len(b.sessions))
	for _, s := range b.sessions {
		out = append(out, Tab{ID: s.sid, Page: s.page, URL: s.url})
	}
	return out
}

// TabPage — страница вкладки сессии (nil, если нет).
func (b *Browser) TabPage(sid string) *rod.Page {
	b.mu.Lock()
	defer b.mu.Unlock()
	if s, ok := b.sessions[sid]; ok {
		return s.page
	}
	return nil
}

// watchdog — сторож (ТЗ Relay 8.1.5): падение Xvfb → перезапуск Xvfb + всех
// Chrome; падение Chrome сессии → перезапуск этой сессии. Задержка 1 → 30 с (×2).
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
			if !b.xvfbUp {
				b.mu.Unlock()
				return
			}
			xvfbDead := b.xvfbDeadClosed()
			var checks []struct{ sid, ctrl string }
			for sid, s := range b.sessions {
				checks = append(checks, struct{ sid, ctrl string }{sid, s.controlURL})
			}
			b.mu.Unlock()
			if xvfbDead {
				b.log.Warn("browser: Xvfb упал, перезапуск", "backoff", backoff.String())
				time.Sleep(backoff)
				b.restartAll()
				backoff = nextBackoff(backoff)
				continue
			}
			var dead []string
			for _, c := range checks {
				if !b.chromeAlive(c.ctrl) {
					dead = append(dead, c.sid)
				}
			}
			if len(dead) > 0 {
				b.log.Warn("browser: Chrome сессии упал, перезапуск",
					"sessions", dead, "backoff", backoff.String())
				time.Sleep(backoff)
				for _, sid := range dead {
					b.restartSession(sid)
				}
				backoff = nextBackoff(backoff)
				continue
			}
			backoff = restartBackoffBase
		}
	}
}

// nextBackoff — следующая задержка перезапуска (×2, до max).
func nextBackoff(cur time.Duration) time.Duration {
	cur *= restartBackoffStep
	if cur > restartBackoffMax {
		return restartBackoffMax
	}
	return cur
}

// restartAll — перезапуск Xvfb + всех Chrome сессий.
func (b *Browser) restartAll() {
	b.mu.Lock()
	type snap struct{ sid, url string }
	var snaps []snap
	for sid, s := range b.sessions {
		snaps = append(snaps, snap{sid, s.url})
		b.teardownSessionLocked(s)
		delete(b.sessions, sid)
	}
	b.teardownXvfb()
	err := b.startXvfbLocked(context.Background())
	b.mu.Unlock()
	if err != nil {
		b.log.Error("browser: перезапуск Xvfb не удался", "err", err)
		return
	}
	for _, sn := range snaps {
		if _, err := b.OpenTab(context.Background(), sn.sid, sn.url); err != nil {
			b.log.Warn("browser: сессия не открыта после перезапуска", "sid", sn.sid, "err", err)
		}
	}
}

// restartSession — перезапуск Chrome одной сессии.
func (b *Browser) restartSession(sid string) {
	b.mu.Lock()
	s, ok := b.sessions[sid]
	if !ok {
		b.mu.Unlock()
		return
	}
	url := s.url
	b.teardownSessionLocked(s)
	delete(b.sessions, sid)
	b.mu.Unlock()
	if _, err := b.OpenTab(context.Background(), sid, url); err != nil {
		b.log.Warn("browser: сессия не открыта после перезапуска", "sid", sid, "err", err)
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

// chromeAlive — жив ли Chrome: DevTools HTTP отвечает.
func (b *Browser) chromeAlive(controlURL string) bool {
	base := devtoolsBase(controlURL)
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
