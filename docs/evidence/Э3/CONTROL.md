# Э3 — CONTROL: PASS

Дата: 2026-09-25. Все строки CONTROL выполнены.

Стенд: координатор `127.0.0.1:28788` (API) + `127.0.0.1:28787` (шлюз),
два OpenAI-совместимых бэкенда `a` (priority 10, 1 слот, `127.0.0.1:29001`)
и `b` (priority 5, 1 слот, `127.0.0.1:29002`) — standalone-фэйки (аналог
`fakellm`, раздел 15 ТЗ: SSE, управляемые отказы до/после первого байта,
задержки, `/health`). `hold_max_sec=2`, `retry_after_sec=7`,
`max_inflight=4`, `max_body_mb=1`, `max_output_tokens=1000`.
Запуск стенда: `stand.sh`; измерения: `harness.py` (вне репозитория —
стенд приёмки; исходники приложены). Бинарник: `CGO_ENABLED=0 go build`.

| # | Строка CONTROL (ТЗ) | Команда проверки | Артефакт | Результат |
|---|---|---|---|---|
| 1 | накладные расходы p95 ≤ 5 мс на 1000 запросов против прямого вызова | `harness.py` фаза [3]: 1000 последовательных пар — POST `fakellm-a` напрямую и POST через шлюз `/s/<sid>/v1/chat/completions` (та же сессия, ACTIVE-аренда); Δ = t_шлюз − t_прямой по паре | `harness.json` `overhead` | PASS: n=1000, p50=0.26 мс, **p95=0.34 мс**, p99=0.42 мс; ср. прямой 0.30 мс, ср. шлюз 0.56 мс |
| 2 | разница TTFB p95 ≤ 50 мс | `harness.py` фаза [4]: 100 пар стриминг-запросов (SSE, `first_byte_delay=30 мс`), TTFB = момент получения заголовков ответа; Δ = TTFB_шлюз − TTFB_прямой по паре | `harness.json` `ttfb` | PASS: n=100, p50=0.30 мс, **p95=0.48 мс**, p99=0.59 мс (ср. прямой 30.46 мс, ср. шлюз 30.78 мс — стриминг не буферизуется, `FlushInterval:-1`) |
| 3 | число SSE-чанков совпадает 1:1 | `harness.py` фаза [5]: один стриминг-запрос с 7 чанками напрямую и через шлюз; подсчёт строк `data: {` в клиентском потоке | `harness.json` `sse` | PASS: прямой=7, шлюз=7; в репо дополнительно `go test ./internal/gateway/ -run TestSSEChunks1to1` |
| 4 | миграция до первого байта успешна в 50 из 50 отказов | `go test ./internal/gateway/ -run TestMigration50` (fakellm `a`: 50 отказов 503 ДО первого байта, `/health` жив; на каждом ходе аренда снимается, следующий запрос снова на `a` → отказ → миграция на `b`) | `test-race.log` | PASS: **50/50** — 50 запросов к `a` (по одному отказу), 50 успешных к `b`, `last_migrated_from='a'`, `a` остаётся UP (отказ транзиторный); live-стенд: 10/10 (`harness.json` `migration_live`) |
| 5 | код и тело удержания побайтно как в разделе 6 | `harness.py` фаза [7]: оба слота заняты медленными запросами, третий → удержание `hold_max_sec=2 с` → ответ; побайтовое сравнение тела и заголовка; запись очереди в БД | `harness.json` `hold`, `console.log` | PASS: HTTP **503**, `Retry-After: 7`, удержание 2.00 с, тело побайто `{"error":{"message":"aqm: no free slot, queue position 1","type":"server_error","code":"aqm_no_slot"}}`; очередь: `held_request=0`, `mode='resume'`; в репо `TestHoldBodyExact` (то же, `hold_max_sec=1`) |
| 6 | в коде шлюза нет разбора usage и нет 529 | `go test ./internal/gateway/ -run TestNoUsageNo529` (скан всех .go пакета `internal/gateway` вне тестов на литералы `usage`/`529`) + grep | `test-race.log`, `console.log` | PASS: 0 вхождений в production-коде пакета; документация `doc.go` перефразирована, чтобы и в комментариях не было запрещённых литералов |

Дополнительно (строгие правила и приёмка кода):

| Проверка | Команда | Артефакт | Результат |
|---|---|---|---|
| `make check` (vet, staticcheck, test -race, check-literals) | `make check` | `make-check.log` | PASS: vet/staticcheck 0 замечаний, тесты rc=0, «в scheduler/gateway/detect нет числовых литералов, кроме 0 и 1» |
| `go test -race -count=1 ./...` | та же команда | `test-race.log` | PASS: 10 пакетов с тестами, race-detector чист (15 тестов `internal/gateway`, включая новые: миграция, модель после миграции, удержание, ёмкость, 4xx, pin/prefer, passthrough) |
| доступ: 404 неизвестный/GONE, 403 чужой IP, 200 свой | `go test ./internal/gateway/ -run TestAccess` + `harness.py` фаза [1] | `test-race.log`, `console.log` | PASS: live — 404/404/403/200 |
| подмена model и ключа (Bearer из `key_env`), кап `max_*` по `max_output_tokens` | `go test ./internal/gateway/ -run TestBodyTransform` + `harness.py` фаза [2] | `test-race.log`, `console.log` | PASS: live — эхо `model: srv-a` (aqm подменён), `Authorization: Bearer ka`; репо — `max_tokens`/`max_completion_tokens` 999999/5000 → 1000/1000; `stream_options` шлюзом не добавляется |
| `max_body_mb` → 413 | `go test ./internal/gateway/ -run TestBodyLimit413` | `test-race.log` | PASS: тело 1 МиБ + 1 Б → 413 |
| `max_inflight` → 503 aqm_no_slot без постановки в очередь | `go test ./internal/gateway/ -run TestMaxInflight503` | `test-race.log` | PASS: 3-й параллельный запрос при `max_inflight=2` → немедленный 503, записи в `queue_entry` нет |
| разрешение аренды: правила 1–3 (ACTIVE; PENDING→ACTIVE+RUNNING; IMPLICIT+удаление записи очереди; IDLE→RUNNING) | `go test ./internal/gateway/ -run TestResolveRules` | `test-race.log` | PASS |
| удержание: слот выдан во время ожидания → проксирование, запись очереди удалена | `go test ./internal/gateway/ -run TestHoldSlotGranted` | `test-race.log` | PASS |
| миграция: мёртвый сервер (`/health` 503) → DOWN, ход с `b` | `go test ./internal/gateway/ -run TestMigrationServerDown` | `test-race.log` | PASS |
| миграция: тело повторно подменяется под **новый** сервер (модель + `max_output_tokens`) | `go test ./internal/gateway/ -run TestMigrationModelSwap` | `test-race.log` | PASS: `b` получает `model-b`, не `model-a` |
| 4xx — без изменений, без миграции, аренда сохранена | `go test ./internal/gateway/ -run Test4xxPassthrough` | `test-race.log` | PASS: 400 апстрима → 400 клиенту, тело переслано |
| прочие пути (`v1/models`): без аренды → UP с наибольшим priority, аренды нет | `go test ./internal/gateway/ -run TestPassthroughModels` | `test-race.log` | PASS: ответ с `b` (priority 10 > 5) |
| pin/prefer: prefer ставит сервер первым; pin на занятый → удержание 503 | `go test ./internal/gateway/ -run TestPinPrefer` | `test-race.log` | PASS |
| строки `request` без токенов: на каждый генерирующий запрос | `harness.py` фаза [8]: `PRAGMA table_info(request)` + `COUNT(*)` | `harness.json` `request_rows` | PASS: 1093 строки; колонки `id, sid, turn_id, server, path, status, t_start, t_first_byte, t_end, held_ms, error` — `tokens_*` нет |
| время — только `internal/clock`; пороги — только из конфигурации | `go run ./scripts/check-literals` + код `internal/gateway` | `make-check.log` | PASS: литералы только 0/1; HTTP-коды — константы `net/http`; 1 МиБ в `internal/config` (`MaxBodyBytes`) |

## Отклонения и решения (зафиксированы)

### 1. Повторный отказ до первого байта (оба сервера отказали)

ТЗ описывает один повтор через миграцию; поведение при отказе **второго**
сервера не описано. Реализовано: внеочередная проверка `/health` второго
сервера (нездоров → DOWN), аренда освобождается с `SERVER_DOWN`, клиенту
502. Задокументировано как расширение; тесты на него не накладывают
ограничений, повторный повтор не выполняется.

### 2. Транзиторный отказ не меняет состояние сервера

Отказ апстрима до первого байта при живом `/health` (проверка
выполняется внеочередно) оставляет сервер UP: сервер не валится от
одного транзиторного 502/503/504. Автомат UP/DOWN по счётчикам
`health_down_after`/`health_up_after` — монитор (Э6).

### 3. Путь «прочие пути» без единого UP-сервера

`handlePassthrough` при отсутствии UP-серверов отвечает 503 aqm_no_slot
(тело общее). Раздел 6 ТЗ это не описывает; выбор — не выдумывать новый
код. Генерирующие запросы в этой ситуации проходят удержание по правилу 4.

### 4. `Content-Length` после подмены тела

Подмена `model`/лимита `max_*` меняет длину тела; `Content-Length`
пересчитывается в каждой попытке (`doAttempt`), иначе транспорт
отклоняет запрос (`ContentLength ≠ len(body)`). Найдено live-стендом.

### 5. Тело повторно подменяется при миграции

После миграции повторный запрос проходит `transformBody` заново под
**новый** сервер: `model` из `upstreams.openai.model` и
`max_output_tokens` нового сервера (раздел 6 ТЗ: «model заменяется на
модель апстрима **выбранного** сервера»). Без этого новый сервер
получал бы модель старого. Найдено live-стендом; регрессионный тест
`TestMigrationModelSwap`.

### 6. `last_request_end` — в памяти шлюза, не в схеме `lease`

`lease.last_request_end` (раздел 6 ТЗ: на каждый запрос) хранится в
`map[leaseID]time.Time` в памяти координатора (доступ
`Gateway.LastRequestEnd` для планировщика Э4). В схему SQLite не
вывается: колонки `lease` зафиксированы в Э0, поле не участвует в
персистентных инвариантах (после рестарта координатора запросы
перезапускаются, погрешность допустима до Э4/Э7).

### 7. `WriteTimeout=0` у обоих HTTP-серверов координатора

Сервер API и сервер шлюза поднимаются в `serve.go` с `WriteTimeout: 0`
(раздел 6 ТЗ — явно для шлюза; для API — те же длинные SSE-ответы
не предусмотрены, но единый параметр проще и безопаснее).
