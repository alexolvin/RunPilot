package browser

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
	"time"
)

// viewPageHTML — страница с непрерывной отрисовкой (CSS-анимация → кадры
// screencast), кнопкой в фиксированной точке и полем ввода.
const viewPageHTML = `<html><head><style>
@keyframes spin{from{transform:rotate(0)}to{transform:rotate(360deg)}}
#anim{position:absolute;left:0;top:200px;width:80px;height:80px;background:red;animation:spin 0.5s linear infinite}
</style></head><body>
<button id="btn" style="position:absolute;left:100px;top:50px;width:60px;height:30px"
 onclick="document.getElementById('out').textContent='clicked'">Клик</button>
<input id="in" style="position:absolute;left:200px;top:50px">
<div id="out"></div>
<div id="anim"></div>
</body></html>`

// writeViewPage — запись тестовой страницы, возвращает file:// URL.
func writeViewPage(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "page.html")
	if err := os.WriteFile(p, []byte(viewPageHTML), 0o600); err != nil {
		t.Fatal(err)
	}
	return "file://" + p
}

// TestViewScreencast — кадры приходят, Active/StopScreencast корректны.
func TestViewScreencast(t *testing.T) {
	b := testBrowser(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := b.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	id, err := b.OpenTab(context.Background(), writeViewPage(t))
	if err != nil {
		t.Fatalf("OpenTab: %v", err)
	}
	rodPage := b.TabPage(id)
	if err := rodPage.WaitLoad(); err != nil {
		t.Fatalf("WaitLoad: %v", err)
	}
	v := NewView(rodPage, 70, 1440)
	// Кадры собираем каналом — без shared state (race-чисто).
	frameCh := make(chan time.Time, 64)
	if err := v.StartScreencast(func(data []byte) {
		if len(data) == 0 {
			return
		}
		select {
		case frameCh <- time.Now():
		default:
		}
	}); err != nil {
		t.Fatalf("StartScreencast: %v", err)
	}
	if !v.Active() {
		t.Fatal("Active = false после старта")
	}
	// Страница анимирована — кадры должны прийти.
	var timestamps []time.Time
	deadline := time.Now().Add(5 * time.Second)
	for len(timestamps) < 15 && time.Now().Before(deadline) {
		select {
		case ts := <-frameCh:
			timestamps = append(timestamps, ts)
		case <-time.After(200 * time.Millisecond):
		}
	}
	if len(timestamps) < 5 {
		t.Fatalf("кадров = %d, хочу ≥ 5", len(timestamps))
	}
	// p95 задержки кадра (CONTROL R1: ≤ 300 мс).
	intervals := make([]time.Duration, 0, len(timestamps)-1)
	for i := 1; i < len(timestamps); i++ {
		intervals = append(intervals, timestamps[i].Sub(timestamps[i-1]))
	}
	sort.Slice(intervals, func(i, j int) bool { return intervals[i] < intervals[j] })
	p95 := intervals[int(float64(len(intervals))*p95Quantile)]
	if p95 > 300*time.Millisecond {
		t.Fatalf("p95 кадра = %v, хочу ≤ 300 мс", p95)
	}
	v.StopScreencast()
	if v.Active() {
		t.Fatal("Active = true после стопа")
	}
	// Повторный стоп — идемпотентно, без паники.
	v.StopScreencast()
	b.CloseTab(id)
	b.Stop()
}

// TestViewClickInsertText — клик по кнопке и ввод кириллицы работают.
func TestViewClickInsertText(t *testing.T) {
	b := testBrowser(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := b.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	id, err := b.OpenTab(context.Background(), writeViewPage(t))
	if err != nil {
		t.Fatalf("OpenTab: %v", err)
	}
	rodPage := b.TabPage(id)
	if err := rodPage.WaitLoad(); err != nil {
		t.Fatalf("WaitLoad: %v", err)
	}
	v := NewView(rodPage, 70, 1440)
	// Клик в центр кнопки (100..160 × 50..80) → #out = "clicked".
	if err := v.Click(130, 65); err != nil {
		t.Fatalf("Click: %v", err)
	}
	out, err := rodPage.Eval(`() => document.getElementById('out').textContent`)
	if err != nil {
		t.Fatalf("Eval out: %v", err)
	}
	if out.Value.Str() != "clicked" {
		t.Fatalf("#out = %q, хочу clicked", out.Value.Str())
	}
	// Ввод кириллицы: фокус поля + InsertText.
	in, err := rodPage.Element(`#in`)
	if err != nil {
		t.Fatalf("Element #in: %v", err)
	}
	if err := in.Focus(); err != nil {
		t.Fatalf("Focus: %v", err)
	}
	if err := v.InsertText("привет"); err != nil {
		t.Fatalf("InsertText: %v", err)
	}
	val, err := rodPage.Eval(`() => document.getElementById('in').value`)
	if err != nil {
		t.Fatalf("Eval in: %v", err)
	}
	if val.Value.Str() != "привет" {
		t.Fatalf("#in = %q, хочу «привет»", val.Value.Str())
	}
	b.CloseTab(id)
	b.Stop()
}

// TestViewOpenCloseGoroutines — 50 open/close → 0 активных screencast,
// горутины возвращаются к базовой линии (CONTROL R1).
func TestViewOpenCloseGoroutines(t *testing.T) {
	b := testBrowser(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := b.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	id, err := b.OpenTab(context.Background(), writeViewPage(t))
	if err != nil {
		t.Fatalf("OpenTab: %v", err)
	}
	v := NewView(b.TabPage(id), 70, 1440)
	// Базовая линия горутин после стабилизации.
	time.Sleep(500 * time.Millisecond)
	base := runtime.NumGoroutine()
	for i := 0; i < 50; i++ {
		if err := v.StartScreencast(func(data []byte) {}); err != nil {
			t.Fatalf("цикл %d: Start: %v", i, err)
		}
		if !v.Active() {
			t.Fatalf("цикл %d: не активен после старта", i)
		}
		v.StopScreencast()
		if v.Active() {
			t.Fatalf("цикл %d: активен после стопа", i)
		}
	}
	time.Sleep(500 * time.Millisecond)
	if got := runtime.NumGoroutine(); got > base+2 {
		t.Fatalf("горутины: базово %d, после 50 циклов %d", base, got)
	}
	b.CloseTab(id)
	b.Stop()
}
