# Э4 — CONTROL: PASS

Дата: 2026-09-25. Все строки CONTROL выполнены.

Стенд: координатор `127.0.0.1:27788` (API) + `127.0.0.1:27787` (шлюз),
два OpenAI-совместимых бэкенда `a` (priority 10, **1 слот**,
`127.0.0.1:27001`) и `b` (priority 5, **1 слот**, `127.0.0.1:27002`) —
standalone-фэйки (аналог `fakellm`, раздел 15 ТЗ: SSE, `/health`,
`/_stats`). `tick_ms=100`, `auto_enqueue_stable_sec=2`,
`done_quiet_sec=3`, `done_stable_sec=3`, `cooldown_sec=1`,
`budget_p95_ms=1500`, `node_lost_release_sec=120`, `scan_interval_sec=1`.
6 узлов-агентов — `mockcoder` (`--turns 3 --posts 3`) в tmux на сокете
`aqmtest` (раздел 15 ТЗ). Запуск стенда: `stand.sh`; клиент `aqm run`
повторяет `aqmr.py` (tmux на сокете `aqmtest`); измерения: `harness.py`
(вход — ввод промпта без Enter, Enter жмёт диспетчер; исходники приложены).
Бинарник: `CGO_ENABLED=0 go build`.

| # | Строка CONTROL (ТЗ) | Команда проверки | Артефакт | Результат |
|---|---|---|---|---|
| 1 | S1–S14 — PASS | `go test -race -count=1 -run 'TestS[0-9]' ./internal/scheduler/` (виртуальные часы, фэйки серверов/панелей, детерминированно) | `test-race.log`, `scenario_test.go` | PASS: 15 тестов S1–S14 (включая S4b) — приоритет, pin_mismatch, prefer_wait, warm-cache, migrated-не-тёплый, aging, resume-backoff, accept, external, слоты, pause, enqueue-running, after, pin_unavailable, stale_snapshot |
| 2 | e2e 2 × fakellm по 1 слоту, 6 × mockcoder по 3 хода | `stand.sh` (2 фэйка по 1 слоту) + `harness.py` (6 сессий × 3 хода = 18 ходов, 54 POST) | `harness.json`, `serve.log`, `harness.log` | PASS: 18/18 ходов `outcome=OK`, 54/54 запросов доставлены (fake `a`=27, `b`=27), 0 `API Error` |
| 3 | одновременных аренд на сервер ≤ 1 на каждом сэмпле (шаг 100 мс) | `harness.py` фоновый сэмплер: `SELECT server, COUNT(*) FROM lease WHERE state IN ('PENDING','ACTIVE') GROUP BY server` каждые 100 мс на протяжении всего прогона | `harness.json` `lease_violation_count`, `lease_max_seen` | PASS: 600 сэмплов, max одновременно на сервер = **1**, нарушений **0** |
| 4 | 0 таймаутов клиента | `harness.py`: 18 ходов дошли до `IDLE` и закрылись `OK` в пределах watchdog (240 с/раунд); 54/54 запросов со статусом 200 | `harness.json` `turns_ok`, `requests_total`, `requests_by_status` | PASS: 18/18 OK, 54 запроса `status=200`, 0 таймаутов/ошибок |
| 5 | задержка диспетчеризации p95 ≤ `dispatch.budget_p95_ms` | `GET /api/v1/internal/dispatch-latency` (замер координатор→узел→reply на каждый dispatch) | `harness.json` `dispatch_p95_ms` | PASS: n=18, **p95=100 мс** ≤ 1500 мс |
| 6 | снятие аренды ≤ `done_quiet_sec + done_stable_sec + 2 с` | `harness.py`: по каждому ходу `turn.ended_at − max(request.t_end хода)` (связка по sid + временное окно хода) | `harness.json` `release_max_gap_sec`, `release_over_budget` | PASS: max gap = **3.99 с** ≤ 8 с (3+3+2), превышений 0 |
| 7 | каждое SKIP пишет `ineligible_reason` | `harness.py` сэмплер `queue_entry`: нарушение = `ineligible_since` ≠ 0 (реальное время) при пустом `ineligible_reason`; + юнит-проверки причины на каждом пропуске | `harness.json` `skip_violation_count`; `test-race.log` (S2/S7/S9/S13/S14) | PASS: live — **0** пропусков без причины (причины наблюдались у 6 сессий: `no_up_server`, `operator_typing` и др.); юнит — reason на каждом из S2/S7/S9/S13/S14 |
| 8 | ход не закрывается, пока панель UNKNOWN | `go test -race -run TestUnknownPane ./internal/scheduler/` (RUNNING + UNKNOWN панель: < unknown_max_sec аренда ACTIVE, ≥ unknown_max_sec — `PANE_UNKNOWN`+HOLD, строка `turn` остаётся открытой) | `test-race.log`, `dispatch_test.go` | PASS: ход не закрыт при UNKNOWN; после `unknown_max_sec` — HOLD(PANE_UNKNOWN), аренда снята, `turn` не закрыт |

Дополнительно (строгие правила, таблица диспетчеризации, снятие аренды):

| Проверка | Команда | Артефакт | Результат |
|---|---|---|---|
| `make check` (vet, staticcheck, test -race, check-literals) | `make check` | `make-check.log` | PASS: vet/staticcheck 0 замечаний, тесты rc=0, «в scheduler/gateway/detect нет числовых литералов, кроме 0 и 1» |
| `go test -race -count=1 ./...` | та же команда | `test-race.log` | PASS: все пакеты, race-detector чист (в т.ч. новый `TestAutoEnqueueFlow`) |
| таблица диспетчеризации: no-reply → QUEUED, pane_changed → QUEUED, empty_input → HOLD, start_not_confirmed → HOLD, already_busy → RUNNING | `go test -race -run 'TestDispatch|TestStartNotConfirmed' ./internal/scheduler/` | `test-race.log`, `dispatch_test.go` | PASS: 5 переходов таблицы раздела 8 |
| NODE_LOST: узел молчит ≥ `node_lost_release_sec` без трафика → аренда снята (NODE_LOST), сессия HOLD | `go test -race -run TestNodeLost ./internal/scheduler/` | `test-race.log`, `dispatch_test.go` | PASS: < 120 с аренда жива, ≥ 120 с — HOLD(NODE_LOST) |
| автопостановка (раздел 9): IDLE + auto_enqueue + панель IDLE с вводом → очередь → слот | `go test -race -run TestAutoEnqueueFlow ./internal/scheduler/` | `test-race.log`, `scenario_test.go` | PASS: регрессия на дедлок тика (см. отклонение 1) |
| live: автопостановка → dispatch → ход (smoke) | `aqmr.py` (1 сессия, ввод промпта, наблюдение) | `serve.log` (сессия создана), `harness.json` (полный прогон) | PASS: IDLE→RUNNING→IDLE, `turn outcome=OK requests=3`, аренда RELEASED/TURN_DONE |
| live: узел переживает молчание координатора > 2 мин (wsReadDeadline) | ручной прогон: 130 с без dispatch, затем проверка `node.log`/`serve.log` | `node.log`, `serve.log` | PASS: на 120 с `read: i/o timeout` → узел **переподключился** через ~1 с (не повис; см. отклонение 2) |
| время — только `internal/clock`; пороги — только из конфигурации | `go run ./scripts/check-literals` + код `internal/scheduler` | `make-check.log` | PASS: литералы только 0/1; все пороги из `cfg.*` |

## Отклонения и решения (зафиксированы)

### 1. Мёртвая петля тика при автопостановке (найден live-стендом, исправлен)

`Tick()` захватывает `s.mu.Lock()` и вызывает `tickAutoEnqueue`, который
звал публичный `Enqueue` — тот снова `s.mu.Lock()`. `sync.Mutex` не
реентрантен → планировщик вешался навсегда на первом же авто-enqueue
(симптом: сессия не уходила из IDLE, тики прекращались, `queue_entry` не
создавалась). Исправлено: вынесено ядро `enqueueLocked` (без блокировки),
публичный `Enqueue` — обёртка с `Lock`, тик зовёт `enqueueLocked`.
Регрессионный тест `TestAutoEnqueueFlow` (без поправки вешается).

### 2. Вис узла после молчания координатора (потенциальный баг Э2, исправлен)

Горутина записи WS-канала узла следила только за внешним `ctx`, а не за
контекстом соединения. При срабатывании `wsReadDeadline` (2 мин
молчания координатора) `readErr` ждал `<-writerDone`, который не
завершался → `session()` не возвращался, узел больше не переподключался
(«жив, но молчит»; симптом `node: ps: context canceled` ровно на 120 с).
Исправлено: создан `connCtx` до горутины записи, запись и цикл снимков
наблюдаются за ним, `readErr` его отменяет. Подтверждено live: на 120 с
узел переподключился за ~1 с, сканер продолжил работать. Ограничение:
без периодического пульса координатора→узел при долгом простое узел
переподключается каждые ~2 мин (безопасно: `nodeDown` << `node_lost_release_sec`,
аренды не снимаются); периодический ping координатора — в Э6 (мониторинг).

### 3. `no_up_server` для «все слоты заняты»

Раздел 5 ТЗ (приложение А) не имеет отдельного кода «серверы UP, но все
слоты LEASED». Ближайший код — `no_up_server` («сервера, способного
отдать слот, нет»); более специфичные коды (`pin_mismatch`, `accept`,
`cooldown`, `external`) имеют приоритет. Зафиксировано в `primaryReason`
и покрывается S9 (3-я запись при 2 слотах → причина не пуста).

### 4. `request.turn_id` = 0 (запросы не привязаны к строке `turn`)

Ход открывается в момент **подтверждения** старта (`ensureTurn` при
первом запросе / BUSY-панели), а первый запрос шлюза фиксируется до
этого, поэтому надёжной привязки «запрос → turn_id» на момент записи нет;
`recordRequest` пишет `turn_id=0` (заглушка Э3, комментарий «ходы ведёт
планировщик (Э4)»). Для замера «снятие аренды ≤ … от последнего запроса»
harness связывает запросы с ходом по `sid` + вхождению во временное окно
хода `[started_at, ended_at]` (детерминированно: запросы только в ходе,
следующий ход — после возврата в IDLE). Полная привязка `turn_id` —
уточнение для последующего этапа (не строка CONTROL Э4).

### 5. `received_at` — координатором, `auto_enqueue` — механизм e2e

`received_at` снимка присваивает координатор (`s.clk.Now()` в
`PaneUpdate`/`applyPanes`), не узел. В e2e постановка в очередь идёт
через **автопостановку** (раздел 9 ТЗ): `--auto-enqueue`, панель IDLE с
непустым вводом, стабильность `auto_enqueue_stable_sec` — отдельного
enqueue-API/CLI нет (появится в Э5), поэтому harness вводит промпт
`tmux send-keys` без Enter, а Enter жмёт диспетчер после выдачи слота.

### 6. Повторный заход после ERROR_TRANSIENT — `after_sid` не сохраняется

Автоперепостановка (`requeueOrHold`) создаёт RESUME-запись с
`not_before = now + resume_backoff_sec × attempts`; поле `after_sid`
при этом не наследуется (в Э4 авто-перепостановка без зависимостей).
Зависимости `--after` для ручных заходов покрывает S12. Не строка
CONTROL Э4.

### 7. RUNNING → HOLD при фатальной ошибке — явный `hold_reason`

Переход RUNNING→HOLD при NODE_LOST/PANE_UNKNOWN/UPSTREAM_4XX
реализован через `EvErrorFatal` с явным `hold_reason` (NODE_LOST /
PANE_UNKNOWN / UPSTREAM_4XX) — автоматон раздела 4 требует явной причины
HOLD. Подтверждено юнит-тестами `TestNodeLost`/`TestUnknownPane`.
