// Драйвер снимков W8 (Терминал в браузере, 13.4) для конвейера 19.2.
// Водит реальное UI: карточка сессии → открытый оверлей xterm (эхо-терминал
// стенда). Числа/задержки — в tools/ (R2 не применяется).
package main

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-rod/rod"
)

// driveW8 — довести UI к состоянию шага (см. W8-T в combosFor, main.go).
func driveW8(page *rod.Page, j string) {
	switch j {
	case "W8-T":
		w8OpenTerminal(page)
	}
}

// w8OpenTerminal — карточка первой сессии (контекст 13.4) + открытый оверлей
// терминала; ждём отрисовки xterm (banner от эхо-терминала стенда).
func w8OpenTerminal(page *rod.Page) {
	v, err := page.Eval(`() => (async () => {
		const r = await fetch('/api/v1/state');
		const d = await r.json();
		const s = (d.sessions || [])[0];
		return s ? JSON.stringify({ sid: s.SID }) : '';
	})()`)
	if err != nil {
		return
	}
	var s struct {
		SID string
	}
	if json.Unmarshal([]byte(v.Value.Str()), &s) != nil || s.SID == "" {
		return
	}
	// Контекст: карточка сессии (кнопка «Открыть терминал» — из карточки).
	w6Nav(page, "/sessions/"+s.SID)
	w6WaitSel(page, ".sc-body")
	// Открыть оверлей (тестовый хук) + дождаться xterm и banner.
	page.Eval(fmt.Sprintf(`() => { window.__runpilotOpenTerminal(%q); }`, s.SID), nil)
	w6WaitSel(page, ".term-body .xterm")
	time.Sleep(600 * time.Millisecond) // дождаться banner/эхо на экране
}
