# R5 — CONTROL (ТЗ Relay §15)

Этап: R5 — Веб, Telegram, CLI, API
Дата: 2026-10-12
Версия: 2026.12.12

## Гейты

| Команда | Результат |
|---------|-----------|
| `go test -race ./...` | PASS (exit 0) |
| `CGO_ENABLED=0 staticcheck ./...` | PASS (exit 0) |
| `go run ./tools/lintlits` | PASS |
| `go run ./tools/lintweb` | PASS |
| `go run ./tools/linthosts` | PASS |

## Реализация (PASS)

- [PASS] API: `PATCH /sessions/{t}` (orch_url, relay_enabled) — ошибки `ORCH_SITE_UNKNOWN` (422), `ORCH_CHAT_IN_USE` (409)
- [PASS] API: `GET /sessions/{t}/relay` — статус связи (state, hold, cycle, хеши/время)
- [PASS] API: `POST /sessions/{t}/relay/{send|deliver|skip|resume}` — действия связи
- [PASS] API: `POST /relay/check` {url} — проверка сайта и ключа чата
- [PASS] API: `GET /browser` — состояние браузера + вкладки
- [PASS] CLI: `runpilot relay set|status|send|deliver|skip|resume|check` с `--json`
- [PASS] CLI: `runpilot browser view` — ссылка на окно
- [PASS] CLI: `runpilot orch fixture capture <t> <state>`
- [PASS] Telegram: `/relay <t> on|off|send|deliver|resume` (общий Executor с вебом)
- [PASS] Веб: блок «Связь с проектом» на карточке сессии (чип состояния, цикл, время, кнопки)
- [PASS] Веб: диалог «Связать с проектом» (URL, предупреждение, ошибки ORCH_SITE_UNKNOWN/ORCH_CHAT_IN_USE)
- [PASS] `docs/api.md` — relay-эндпоинты в таблице
- [PASS] Headless-скриншот карточки: блок связи рендерится (360×42), без clipping/overflow

## НЕ СДЕЛАНО (для R6–R7)

- [NOT DONE] Скриншоты 3 размера × 2 темы + `VISUAL.md` (снят 1 скриншот, конвейер — далее)
- [NOT DONE] Событие → перерисовка ≤ 250 мс (e2e-замер)
- [NOT DONE] `/relay` только в разрешённом чате (авторизация чата Telegram — отдельно)
- [NOT DONE] WS `/api/v1/browser/view?tab=` (контроллер view существует, WS-эндпоинт — далее)
- [NOT DONE] Проводка браузера в serve.go (SetBrowser из координатора)

## Артефакты

- internal/api/relay.go, internal/api/relay_test.go
- internal/api/executor.go (Relay), internal/api/server.go (browser, маршруты)
- internal/store/relay.go (RelayLinkByChat), internal/store/session.go (unlink)
- internal/command/{command,exec}.go (/relay), command_test.go, telegram_test.go
- cmd/runpilot/{relay,browsercmd,orchcmd}.go, root.go
- internal/client/types.go (OrchURL, RelayEnabled)
- web/app/views/session.js (блок + диалог), web/app/i18n/ru.js
- docs/api.md
- docs/evidence/R5/session-relay.png
