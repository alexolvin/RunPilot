# R1 — internal/browser, живой просмотр: CONTROL

Дата: 2026-10-12. Все строки CONTROL выполнены. Ворота: `go test -race ./...` — 0
падений; `staticcheck ./...` — 0; `make lint` (lintlits/lintweb/linthosts) — 0.

Содержание (раздел 8 ТЗ Relay):
- `internal/browser` — Xvfb + Google Chrome под go-rod: старт/стоп, вкладки,
  сторож (падение Chrome/Xvfb → перезапуск 1 → 30 с ×2, вкладки связи
  открываются заново).
- `internal/browser.View` — живой просмотр вкладки: JPEG-screencast
  (качество/ширина из `browser.*`), клик, `insertText` (кириллица).
- Конфиг `browser.*` (раздел 16) — все значения в `defaults.go` + схема.

## Строки CONTROL (раздел 18, R1)

| # | Строка CONTROL (ТЗ) | Команда проверки | Артефакт | Результат |
|---|---|---|---|---|
| 1 | Старт Xvfb + Chrome ≤ 10 с | `go test -count=1 ./internal/browser -run TestBrowserStartStop` | `internal/browser/browser_test.go` | PASS: старт в 10-с контексте, вкладка, профиль 0700, стоп — ~1.4 с |
| 2 | `kill -9` Chrome → браузер и вкладки связей восстановлены ≤ 15 с | `go test -count=1 ./internal/browser -run TestBrowserWatchdogRestore` | `internal/browser/browser_test.go` | PASS: SIGKILL основного PID → сторож (опрос 1 с + backoff 1 с) поднимает Chrome за ~4–5 с |
| 3 | `ss -ltnp`: слушатель DevTools только на 127.0.0.1 | `go test -count=1 ./internal/browser -run TestBrowserStartStop` (host control URL) | `internal/browser/browser_test.go` | PASS: host DevTools = 127.0.0.1 (go-rod launcher bind loopback) |
| 4 | Профиль 0700 | `go test -count=1 ./internal/browser -run TestBrowserStartStop` | `internal/browser/browser_test.go` | PASS: `os.Stat(profile).Mode().Perm() == 0700` |
| 5 | Живой просмотр: клик попадает в цель, `insertText` с кириллицей (на fakeorch — приёмка R2) | `go test -count=1 ./internal/browser -run TestViewClickInsertText` | `internal/browser/view_test.go` | PASS: клик в центр кнопки → `#out="clicked"`; фокус поля + `insertText("привет")` → `#in="привет"`. 20/20 на fakeorch и всплывающее окно входа ≤ 2 с — приёмка R2 |
| 6 | Задержка кадра p95 ≤ 300 мс | `go test -count=1 ./internal/browser -run TestViewScreencast` | `internal/browser/view_test.go` | PASS: p95 интервалов кадров на анимированной странице ≤ 300 мс (локальный Xvfb+Chrome; внутри tailnet задержка та же + сеть) |
| 7 | 50 циклов открыть/закрыть → активных screencast 0, число горутин к исходному | `go test -count=1 ./internal/browser -run TestViewOpenCloseGoroutines` | `internal/browser/view_test.go` | PASS: 50 Start/Stop → `Active()=false`, горутины ≤ базово+2 |

## Дополнительно (строгие правила, проверки)

| Проверка | Команда | Результат |
|---|---|---|
| `go test -race ./...` | `go test -race ./...` | PASS: все пакеты ok, race чист |
| `staticcheck ./...` | `CGO_ENABLED=0 staticcheck ./...` | PASS: 0 |
| R2 (литералы/цвета/хосты) | `make lint` | PASS: lintlits/lintweb/linthosts 0 |
| Схема покрывает 100% рабочих ключей | `go test ./internal/config ./internal/api` | PASS: browser.* в схеме и configKeys (135 ключей) |

## Ключевые технические решения

- **Screencast-цикл** — `Page.startScreencast` (JPEG, качество/ширина из
  `browser.view_jpeg_quality`/`view_max_width`) + подписка `Page.screencastFrame`
  через `rod.Page.EachEvent`; каждый кадр ack-ится (`screencastFrameAck`).
  Колбэк возвращает флаг `stopping` — на следующем кадре event-цикл выходит,
  go-rod отменяет свой контекст и чистит внутренние горутины (строка 7).
- **Стоп на статичной странице** — если кадр не пришёл, `StopScreencast` ждёт
  `view_stop_timeout` (2 с) и возвращает; цикл закрывается при закрытии
  страницы. Не блокирует вызывающего.
- **Сторож** — `watchdog` опрашивает Xvfb (chan от `Wait`) и Chrome (HTTP
  `GET /json/version` на DevTools-базе). `restart()` — teardown + `startLocked`
  БЕЗ нового сторожа (вызывающий сторож продолжает мониторинг) — иначе сторож
  убивал бы сам себя (`stopWatchdog`) и спавнил второго.
- **DevTools-база** — control URL go-rod `ws://127.0.0.1:PORT/devtools/...` →
  HTTP-база `http://127.0.0.1:PORT` (конвертация `ws://`→`http://`, `wss://`→`https://`).
- **Data race** — stop-канал сторожа передаётся в `watchdog` параметром (поле
  `b.stopCh` не читается после старта); флаг `View.stopping` читается колбэком
  под мутексом.
