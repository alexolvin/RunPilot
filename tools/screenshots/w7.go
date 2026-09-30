// Драйвер сценариев W7 (Устойчивость, 19.5) для снимков: ведёт реальное UI
// через DOM до нужного состояния перед снимком.
//
// Шаги: J3/J4 (сервер DOWN/«Карантин»), J5 (аварийная остановка), J9 (внешние
// кодеры + JOB), J11 (потеря связи), J12 («Очистить очередь» → «Отменить») и
// пер-строчная навигация W7-N (здоровье узла N9/N10). J10 — карточка сессии
// (goCard, без драйвера). Вспомогательные функции клика/ожидания — из w6.go
// (один package main). Числа/задержки — в tools/ (R2 не применяется).
package main

import (
	"fmt"
	"time"

	"github.com/go-rod/rod"
)

// driveW7 — довести UI к состоянию шага (см. w7Journey / w7Combos в main.go).
func driveW7(page *rod.Page, j string) {
	switch j {
	case "W7-N": // N9/N10: здоровье узла node-02 (проблемы + расхождение часов)
		w7Nav(page, "/nodes/node-02")
		w6WaitSel(page, ".node-health")
	case "J3": // сервер DOWN: «Недоступен», ход идёт на другом сервере
		w7Nav(page, "/servers/srv-02")
		w6WaitText(page, "Недоступен")
	case "J4": // OOM → «Карантин» + «Снять ходы с сервера»
		w7Nav(page, "/servers/srv-03")
		w6WaitText(page, "Карантин")
		w6WaitText(page, "Снять ходы с сервера")
	case "J5": // аварийная остановка: красный баннер
		w6WaitText(page, "Аварийная остановка")
	case "J9": // cron qwen -p → «Завершить»; runpilot exec → JOB в работе
		w6WaitSel(page, ".ext-row")
	case "J11": // потеря связи: баннер «Нет связи» (свежий стенд, SSE живой)
		dropSSE(page)
		w6WaitText(page, "Нет связи")
	case "J12": // «Очистить очередь» → всплывающее сообщение с «Отменить»
		w6Click(page, "Очистить очередь")
		w6WaitSel(page, ".toast")
		// Перемонтирование QueuePage (queue→sessions→queue) перечитывает
		// /api/v1/queue (очередь пуста); тост живёт в общем store — остаётся.
		w7Nav(page, "/sessions")
		w7Nav(page, "/queue")
		w6WaitSel(page, ".toast-action")
	}
}

// w7Nav — hash-маршрут (детальная карточка) + догрузка (аналог w6Nav).
func w7Nav(page *rod.Page, path string) {
	page.Eval(fmt.Sprintf(`() => { location.hash = %q; }`, "#"+path), nil)
	time.Sleep(w6NavDelay)
}
