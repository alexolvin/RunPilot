// View — живой просмотр вкладки (ТЗ Relay, раздел 8): потоковые JPEG-кадры
// screencast + управление (клик, ввод текста). Один контроллер на вкладку.
// Остановка screencast гарантирует выход event-цикла и возврат горутины
// (CONTROL R1: 50 open/close → 0 активных screencast, горутины восстановлены).
package browser

import (
	"fmt"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// View — контроллер живого просмотра страницы.
type View struct {
	page     *rod.Page
	quality  int
	maxWidth int

	mu       sync.Mutex
	active   bool
	stopping bool
	stopLoop func()
	done     chan struct{}
}

// NewView — сборка контроллера просмотра для страницы вкладки.
func NewView(page *rod.Page, quality, maxWidth int) *View {
	return &View{page: page, quality: quality, maxWidth: maxWidth}
}

// Active — запущен ли screencast.
func (v *View) Active() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.active
}

// StartScreencast — начать потоковую передачу JPEG-кадров в onFrame.
// Идемпотентно: повторный запуск при активном не делает ничего.
func (v *View) StartScreencast(onFrame func(data []byte)) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.active {
		return nil
	}
	q := v.quality
	w := v.maxWidth
	if err := (proto.PageStartScreencast{
		Format:   proto.PageStartScreencastFormatJpeg,
		Quality:  &q,
		MaxWidth: &w,
	}).Call(v.page); err != nil {
		return fmt.Errorf("browser: start screencast: %w", err)
	}
	v.stopping = false
	v.done = make(chan struct{})
	// Колбэк возвращает v.stopping: на следующем кадре event-цикл выходит,
	// go-rod отменяет свой контекст и чистит внутренние горутины.
	v.stopLoop = v.page.EachEvent(func(e *proto.PageScreencastFrame) bool {
		onFrame(e.Data)
		_ = proto.PageScreencastFrameAck{SessionID: e.SessionID}.Call(v.page)
		v.mu.Lock()
		stopping := v.stopping
		v.mu.Unlock()
		return stopping
	})
	go func() {
		defer close(v.done)
		v.stopLoop()
	}()
	v.active = true
	return nil
}

// StopScreencast — остановить screencast и дождаться возврата event-горутины.
// Идемпотентно. Ждёт выход цикла (кадр придёт → колбэк вернёт true);
// на статичной странице срабатывает таймаут, цикл закрывается при закрытии
// страницы, таймаут не блокирует вызывающего.
func (v *View) StopScreencast() {
	v.mu.Lock()
	if !v.active {
		v.mu.Unlock()
		return
	}
	v.stopping = true
	done := v.done
	v.mu.Unlock()
	select {
	case <-done:
	case <-time.After(viewStopTimeout):
	}
	_ = proto.PageStopScreencast{}.Call(v.page)
	v.mu.Lock()
	v.active = false
	v.mu.Unlock()
}

// Click — левый клик в точке (x, y) в CSS-пикселях вьюпорта.
func (v *View) Click(x, y float64) error {
	if err := v.page.Mouse.MoveTo(proto.Point{X: x, Y: y}); err != nil {
		return fmt.Errorf("browser: mouse move: %w", err)
	}
	if err := v.page.Mouse.Down(proto.InputMouseButtonLeft, 1); err != nil {
		return fmt.Errorf("browser: mouse down: %w", err)
	}
	if err := v.page.Mouse.Up(proto.InputMouseButtonLeft, 1); err != nil {
		return fmt.Errorf("browser: mouse up: %w", err)
	}
	return nil
}

// InsertText — ввод текста (поддерживает кириллицу через Input.insertText).
func (v *View) InsertText(text string) error {
	if err := v.page.InsertText(text); err != nil {
		return fmt.Errorf("browser: insert text: %w", err)
	}
	return nil
}
