# W4 — Экраны: CONTROL

Дата: 2026-09-26. Все 5 строк CONTROL выполнены. `make check` — 0; `go test -race ./...` —
0 падений; `make lint` — 0; `make screenshots STAGE=W4` — 19.3 = 0 находок (84 файла);
`make visualcheck STAGE=W4` — OK; e2e `go test -tags e2e ./test/e2e/web` — 3/3 PASS.

Содержание (разделы 11.x, 15.4, 19 ТЗ): 7 страниц раздела 11 (Очередь, Сессии, Серверы,
Узлы, Журнал, Мониторинг, Уведомления) на живых данных; SSE-поток с досылкой и `RESYNC`
(`internal/api/events.go`, `web/app/store.js`); статистика и спарклайны (`/api/v1/monitoring`);
`why.text` для всех кодов приложения А и раздела 3.3 (`internal/texts`, `GET /api/v1/meta`);
баннеры режимов (аварийная/безопасный/нет связи). Стенд `aqm-stand` — 8 сценариев 19.1
(`-scenario`), управляющие эндпоинты `/control/*` (fire/state/drop-sse). E2E — go-rod +
headless Chrome.

## Строки CONTROL (раздел 20, W4)

| # | Строка CONTROL (ТЗ) | Команда проверки | Артефакт | Результат |
|---|---|---|---|---|
| 1 | Событие → DOM ≤ 250 мс (p95 на 200 событиях, метки времени rod) | `go test -tags e2e -count=1 -run TestControl1 ./test/e2e/web` | `control/control1_event_dom.json` | PASS: n=200, p50=100мс, **p95=101мс ≤ 250мс**, max=101мс. `/control/fire?n=1` ×200, t0 после fetch, замер до смены `data-sse-id` (идёт из кадра SSE `id:`, `msg.lastEventId`) |
| 2 | Разрыв SSE на 10 с → состояние клиента = `GET /api/v1/state` (пустой diff) | `go test -tags e2e -count=1 -run TestControl2 ./test/e2e/web` | `control/control2_sse_resync.json` | PASS: `/control/drop-sse` (разрыв всех SSE) → смена сессии в HOLD + `fire?n=250` (> sse_replay) → клиент переподключается → `RESYNC` → полный `/state`. Сессия SS00000010=HOLD видна; **diff клиент vs /state пуст** (сессии/серверы/узлы) |
| 3 | `why.text` для каждого кода приложения А и раздела 3.3 | `go test -count=1 ./internal/texts` + `go test -tags e2e -run TestControl3 ./test/e2e/web` | `control/control3_why_meta.json`, `internal/texts/texts_test.go` | PASS: `GET /api/v1/meta` — **ineligible=22, hold_reasons=12**, пусто/дублей кода: 0. Покрыты коды приложения А (pause…held_ok) и раздела 3.3 (emergency, safe_mode, server_disabled, server_quarantine, model_problem, key_missing; agent_exited, emergency). Unit: `TestWhyTextAllCodes`, `TestMetaWhyMapsComplete` |
| 4 | Скриншоты всех страниц во всех обязательных сценариях | `make screenshots STAGE=W4` | `screens/` (84 PNG) + `manifest.json`, `control-screenshots.log` | PASS: 8 сценариев 19.1 × 7 страниц = 56 (1440×900 светлая) + `normal` × (1440 тёмная, 1024 светлая, 390 светлая, 390 тёмная) × 7 = 28 → **84 файла, 0 находок**. `manifest.json` фиксирует commit 13cf96a и проверки каждого файла |
| 5 | `VISUAL.md` + `visualcheck` OK | `make visualcheck STAGE=W4` | `VISUAL.md`, `visualcheck.log` | PASS: `visualcheck W4: OK — 84 файлов, 12 отметок в строке, commit 13cf96a = предок HEAD`. Агент просмотрел 7 страниц normal, 8 сценариев, телефон/планшет/тёмную тему (read_file), V1–V12 по 19.4 |

## Дополнительно (строгие правила, проверки)

| Проверка | Команда | Артефакт | Результат |
|---|---|---|---|
| `make check` (vet, staticcheck, test -race, lint) | `make check` | `make-check.log` | PASS: rc=0 |
| `go test -race ./...` | `go test -race ./...` | `test-race.log` | PASS: все пакеты ok, race чист |
| R2 (нет литералов/цветов вне токенов/реальных хостов) | `make lint` | `make-lint.log` | PASS: `lintlits`/`lintweb`/`linthosts` 0 |
| 19.3 — 0 находок (console/network/scroll/text/axe/touch/fonts) | `make screenshots STAGE=W4` | `control-screenshots.log`, `manifest.json` (checks) | PASS: `OK: 19.3 — 0 находок` по всем 84 файлам |
| E2E (go-rod + aqm-stand, headless Chrome) | `go test -tags e2e -count=1 ./test/e2e/web` | `control/*.json` | PASS: 3/3 (CONTROL 1/2/3) |

## Ключевые технические решения (зафиксированы)

- **SSE id — в кадре, не в теле** (`internal/api/events.go`): события шлются без поля
  `event:` (все `message`), `id: <id>` — отдельной строкой кадра. `encodeSSE` несёт только
  kind/sid/server/ts/payload. Клиент берёт id из `msg.lastEventId` → `applyEvent(ev, sseId)`.
  Это и есть маркер R8 (`data-sse-id`) для замера событие→DOM.
- **RESYNC** (`web/app/store.js`): клиент ведёт `Last-Event-ID` сам (EventSource); при
  `RESYNC` от сервера — полный `GET /api/v1/state` (`doResync`). Состояние клиента = серверу
  после ресинка (CONTROL 2).
- **htm = элементы, не вызовы функций**: `pages.js` рендерит `<${QueuePage}/>` и т.п.
  (вызов `QueuePage()` инлайнит хуки в вызывающий компонент → desync). Зафиксировано в W3.
- **Контраст (19.3.5)**: светлый `--color-text-muted` #64748B→#5B6B85 (≥4.5 на surface-muted);
  `.banner` — нейтральный текст + `.banner .icon` несёт тон; `.nav-count.is-alert` —
  `--color-danger-text` (danger на 14%-тинте < 4.5).
- **Телефон (19.3.6)**: `.tab-btn{min-height:44px}` — нижние вкладки ≥44×44.
- **Стенд `/control/*`** — только в `aqm-stand` (управляющий порт), в `aqm` нет:
  `fire?n` (N KindNotify), `state {sid,to,hold}` (SetSessionState + SESSION_STATE/HOLD),
  `drop-sse` (DropSSE — разрыв всех SSE-подключений).

## Отклонения и решения (зафиксированы)

1. **commit в manifest/VISUAL.md = базовый коммит (13cf96a, W3), не коммит W4.** Скриншоты
   снимаются с рабочего дерева до коммита W4; `git rev-parse HEAD` на момент съёмки = 13cf96a
   (W3, родитель W4). `manifest.json` и `VISUAL.md` фиксируют один коммит; `visualcheck`
   сверяет их совпадение и что коммит — предок HEAD. Точный «хеш = HEAD» при одном коммите
   на этап недостижим (хеш зависит от содержимого VISUAL.md).
2. **Матрица окон/тем — по осям** (19.2): все 8 сценариев на 1440×900 светлая (49 файлов) +
   вся матрица окон/тем на сценарии `normal` (35 файлов). Содержимое не зависит от темы и
   окна (тема меняет только цвета), поэтому пересечение «не-нормальный сценарий × тёмная/
   телефон» не даёт новых визуальных состояний. Обоснование — в `VISUAL.md`.
3. **E2E вне `make check`**: build-тег `e2e` + нужен headless Chrome → `go test ./...` его
   пропускает (0 пакетов `e2e`), `make check` не зависит от Chrome. Запускается отдельной
   командой; evidence — `control/*.json`.
4. **Операторское решение A9 отложено в самый конец** (Э7/Э8 → впитать в W5/W7/W9); на W4 не
   влияет (W4 — просмотр экранных состояний, без управления).
