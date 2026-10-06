# W1 — Служба и доступ: CONTROL

Дата: 2026-09-26. Все 7 строк CONTROL выполнены. `make check` — 0 (vet, staticcheck, test, lint); `go test -race ./...` — 0 падений.

Содержание этапа (раздел 20, W1): встроенный узел, повтор привязки, веб-слушатель,
вход, CSRF, `meta`, поле `actions`, `internal/texts`, линтеры R2, удаление TUI,
`aqm` без аргументов, каркас стенда. См. «Отклонения и решения» — `If-Match`/`version`
и `Idempotency-Key` (15.1) закрываются в W5/W6 вместе со схемой сущностей.

## Строки CONTROL (раздел 20, W1)

| # | Строка CONTROL (ТЗ) | Команда проверки | Артефакт | Результат |
|---|---|---|---|---|
| 1 | В `go.mod` нет `bubbletea`, `bubbles`, `lipgloss`, `teatest` | `grep -E 'bubbletea\|bubbles\|lipgloss\|teatest' go.mod` (пусто); `test -d internal/tui` (нет) | `go.mod` | PASS: из `go.mod` удалены `charmbracelet/{bubbletea,lipgloss}`, `x/exp/teatest`, `muesli/termenv` + их транзитивные зависимости; `internal/tui/` и `cmd/aqm/tui.go` удалены (golden 80×24/120×40 вместе). Остались только `gorilla/websocket`, `spf13/cobra`, `yaml.v3`, `prometheus/{client_model,common}` (монитор Э6) |
| 2 | `make lint` по всему репозиторию — 0 находок (существующий хардкод исправлен) | `make lint` | `make-lint.log` | PASS: `lintlits` (числовые/URL/IP/хост-литералы, R2) + `lintweb` (цвета/кириллица/литералы в JS — web/ появится в W3, сейчас «отсутствует») + `linthosts` (имена хостов 3p14-aaa/vzu5-big, CGNAT 100.64.0.0/10) — все 0. Хардкод устранён: числовые/URL-литералы вынесены в `consts.go` по пакетам (internal/{api,client,config,doctor,model,monitor,node,scheduler,sid,store,testutil}, cmd/{aqm,aqm-fakellm,aqm-mockcoder,aqm-stand}); `cmd/aqm-mockcoder` — имя хоста `3p14-aaa` → константа `hostLabel="node"` |
| 3 | Привязка `coordinator.bind` недоступна N попыток → веб отвечает всё время, API и шлюз отвечают не позже `bind_retry_sec` после появления адреса | `go test ./internal/web/ -run TestListenWithRetry -v` | `control-tests.log` | PASS: `web.ListenWithRetry` (internal/web/listen.go:14) возвращает listener ≤ interval после освобождения адреса, отменяется по ctx (`TestListenWithRetry`, `TestListenWithRetryCancelled`). `serve.go`: веб-слушатель поднимается ПРЯМО (без повторов, line 150-158), API+шлюз — через `retryServe`/`web.ListenWithRetry` (line 159-174) с интервалом `coordinator.bind_retry_sec`; веб отвечает во время недоступности bind API/шлюза |
| 4 | Перебор всех маршрутов веба без cookie → `401`, всех изменяющих без CSRF → `403` | `go test ./internal/web/ -run 'TestWebAllRoutes401WithoutCookie\|TestWebMutation403' -v` | `control-tests.log` | PASS: порядок middleware `cookieAuth(csrf(routes))` (web.go:95) — без cookie **все** маршруты (GET и изменяющие) → 401, с cookie без корректного `X-AQM-CSRF` изменяющие → 403, с CSRF → 200 (`TestWebAllRoutes401WithoutCookie` перебирает все GET+mutating, `TestWebMutation403WithoutCSRF`, `TestWebMutationWithCSRF`). Cookie `aqm_session`, в БД SHA-256, TTL `web.session_ttl_days`, `Secure` из `https` префикса `public_url` |
| 5 | Не петлевой `web.bind`, пустой или короткий токен → отказ старта | `go test ./internal/config/ -run 'TestValidateWebRefusesNonLoopback\|TestValidateToken' -v` | `control-tests.log` | PASS: `Config.ValidateWeb` (config.go:408) принимает только `127.0.0.1`/`::1`, отклоняет `0.0.0.0`/LAN/пустое/`localhost`; `Config.ValidateToken` (config.go:421) отклоняет пустой и короче `limits.token_min_len`. Вызываются в `serve.go` до старта (line 41,44) → отказ запуска. Убран «локальный режим» (A5, W0 RECONCILE) |
| 6 | Сессии хоста координатора видны без службы `aqm-node` | `go test ./internal/api/ -run TestEmbeddedNodeHostVisibleInState -v`; живой стэнд | `control-tests.log`, `embedded-live-state.json` | PASS: встроенный узел (`embedded_node: true`, `cmd/aqm/embedded.go:52`) — тот же путь данных, что WS-узел: `hub.Register` + `Server.NodeSeen` + `Server.ApplyNodeMsg` (server.go:497,520), транспорт — канал в памяти (`memConn`), без WebSocket. Тест гоняет этот путь: узел хоста, его панель (`ApplyNodeMsg`→`hub.SetPane`) и сессия хоста видны в `/api/v1/state`. Живой стэнд: `aqm serve` с `embedded_node:true`, `aqm run` на сокете `aqmtest` → `/state` показывает `nodes:[3p14-aaa]`, `panes:[{pane_id:%0,sid:R6MT44763K,Host:3p14-aaa}]`, `sessions:[R6MT44763K]` без службы `aqm-node` |
| 7 | Схема ответа каждой сущности содержит `actions` | `go test ./internal/api/ -run 'TestStateCarriesActions\|TestSessionsListCarriesActions' -v`; `go test ./internal/scheduler/ -run TestViewsCarryActions -v` | `control-tests.log` | PASS: `model.{Session,Server,Node,Queue}Actions` (actions.go:22,36,45,51) — `[id, enabled, reason]` с расчётом доступности по состоянию. Поле `actions` в `SessionView`/`NodeView` (api/views.go) и `QueueRow`/`ServerRow` (scheduler/views.go:25,56). Тесты: `/state` sessions+nodes несут непустые actions, `/sessions` — actions, строки очереди/серверов — actions |

## Дополнительно (живой стэнд, разделы 2.5/15/16 ТЗ)

| Проверка | Команда | Артефакт | Результат |
|---|---|---|---|
| Встроенный узел: хост + панель + сессия хоста без aqm-node | `aqm serve` (embedded_node:true) → `aqm run --name test --detach` → `GET /api/v1/state` (Bearer) | `embedded-live-state.json` | PASS: `nodes:[{Host:3p14-aaa,TmuxVersion:tmux 3.2a,Sockets:[aqmtest]}]`, `panes:[{pane_id:%0,sid:R6MT44763K,state:IDLE,Host:3p14-aaa}]`, `sessions:[{SID:R6MT44763K,Name:test,PaneID:%18}]` — всё с `actions`; отдельной службы aqm-node нет |
| Вход + cookie + TTL на виртуальных часах | `aqm-stand` (виртуальные часы 2026-01-01) → `POST /web/login` → `GET /web/session` | — | PASS: после входа cookie `aqm_session`, `/web/session` возвращает `expires_at = start + web.session_ttl_days (14д) = 2026-01-15` (виртуальные часы управляют TTL); без cookie `/api/v1/*` → 401 |
| `GET /api/v1/meta` (15.1) | `GET /api/v1/meta` | — | PASS: `texts.Meta` (texts.go:35) — `version`, `mode`, перечисления с подписями (sessions, panes, classes, servers, modes, severities), `hold_reasons`, `ineligible`, `units` |
| `aqm` без аргументов (после удаления TUI) | `aqm` (no args) | — | PASS: запускается CLI-сводка `runSummary` (cmd/aqm/summary.go): режим, ходы, очередь, URL веба — не TUI (TUI удалён). При недоступном координаторе — ошибка подключения (ожидаемо: данных нет) |
| Каркас стенда `aqm-stand` | `go build ./cmd/aqm-stand` | `cmd/aqm-stand/` | PASS: каркас (W1): один процесс — координатор (API+веб) на петлевых адресах, виртуальные часы, временная БД, управляющий порт `GET /status` (version/web_url/control_url/clock/db). Полный стенд с узлами/сценариями/скриншотами — W3 |

## Дополнительно (строгие правила, проверки)

| Проверка | Команда | Артефакт | Результат |
|---|---|---|---|
| `make check` (vet, staticcheck, test, lint) | `make check` | `make-check.log` | PASS: rc=0 |
| `go test -race ./...` | `go test -race ./...` | `test-race.log` | PASS: все пакеты, race чист (вкл. новые `internal/web`, `internal/texts`, `cmd/aqm-stand`) |
| Веб: 401/403/CSRF/логин/лимит | `go test ./internal/web/ -v` | `internal/web/web_test.go` | PASS: `TestWebLoginWrongToken` (401), `TestWebLoginRateLimit` (429), `TestWebGetWithCookie` (200), `TestWebSessionEndpoint` |
| Линтеры R2 | `make lint` | `make-lint.log` | PASS: 3 линтера, 0 находок |

## Отклонения и решения (зафиксированы)

### 1. `If-Match`/`version` и `Idempotency-Key` (15.1) — в W5/W6
Раздел 15.1 перечисляет `If-Match` (409 VERSION_CONFLICT) и `Idempotency-Key` как
общие правила API; они фигурируют и в содержании W1. Однако `If-Match` требует поле
`version` у сущностей (`server`, `node`, `session`, `queue_entry`, `setting` — раздел
4.2, строки 296/322/326/335 ТЗ), которое заводится вместе со схемой сущностей в БД
(W2 — настройки/серверы, W5/W6 — управление), а сами механизмы испытываются в W5/W6
(TW16 — два окна меняют класс, TW17 — двойная отправка). Поэтому инфраструктура
`version`+`If-Match` и `Idempotency-Key` закрывается в W5/W6, а не в W1. В W1 реализована
остальная часть 15.1: формат ответа `meta`, `actions`, cookie/CSRF-аутентификация,
`internal/texts` (тексты ошибок/причин). Формат ошибки `{"error":{code,message,details}}`
(15.1) приводится в W5/W6 вместе с веб-обработкой ошибок; в W1 сохранён существующий
`{"code","detail"}`, на который завязаны тесты Э0–Э6 (не ломать регрессию, R9).

### 2. `go.mod`: prometheus — из indirect в direct
В go.mod W0 `prometheus/{client_model,common}` были `// indirect`; `go mod tidy`
перенес их в direct — это корректно: `internal/monitor/metrics.go:13-15` импортирует
`client_model/go` и `common/expfmt|model` напрямую (парсер `/metrics` Э6). ТUI-зависимости
удалены полностью (строка CONTROL 1).

### 3. Встроенный узел = путь данных WS-узла
Встроенный узел (`cmd/aqm/embedded.go`) не дублирует логику: `node.New` + `node.Run`
(сканер Э2) + `srv.ApplyNodeMsg` (общий с WS-хендлером). Транспорт — `memConn`
(канал в памяти) вместо WebSocket. Служба `aqm-node` на хосте координатора не
нужна; при `embedded_node: true` её unit-файл не устанавливается (W1; миграция unit — W6).
