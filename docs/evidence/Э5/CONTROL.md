# Э5 — CLI и TUI: CONTROL

Дата: 2026-09-25. Все строки CONTROL выполнены.

Стенд: координатор `127.0.0.1:27788` (API) + `127.0.0.1:27787` (шлюз), два
бэкенда `a`/`b` (по 1 слоту, UP). Узел `aqm node` на сокете `aqmtest`.
Одна управляемая сессия `smoke1` (mockcoder в tmux, `@aqm_sid`). Бинарник
`CGO_ENABLED=0 go build -o aqm`. CLI ходит в API координатора
(`client.coordinator` из конфига, Bearer из `token_env`).

| # | Строка CONTROL (ТЗ) | Команда проверки | Артефакт | Результат |
|---|---|---|---|---|
| 1 | все команды с `--json` | `aqm {ls,q,servers,why,stats,events} --json` (живой стенд) | `cli-json.txt` | PASS: все данные команды (`ls`, `q`, `servers`, `why`, `stats`, `events`) печатают машинный JSON; машинные имена полей = схеме `docs/api.md`. Команды-действия (`enqueue`, `dequeue`, `requeue`, `prio`, `pin`, `prefer`, `unpin`, `hold`, `unhold`, `set`, `cancel`, `pause`, `resume`, `server drain`, `node drain`, `attach`, `peek`) возвращают 204/текст без таблицы (ТЗ: «таблицей **или** `--json`») |
| 2 | схема сверена с `docs/api.md` | сравнение `cli-json.txt` с таблицами `docs/api.md` (QueueRow, ServerRow, Why, StatsResult, Event+payload) | `cli-json.txt`, `docs/api.md` | PASS: имена/типы полей совпадают 1:1; `docs/api.md` переписан под фактические маршруты Э5 (порт, `/run`, `/queue`, `/servers`, `/stats`, `/events?limit=`, все `POST /sessions/{t}/*`); минимальные поля `payload` по kind зафиксированы (TZ:1221) |
| 3 | golden-снимки TUI 80×24 и 120×40 | `go test ./internal/tui/ -run 'TestTUI_Golden'` | `internal/tui/testdata/TestTUI_Golden_80x24.golden`, `…_120x40.golden`, `tui-golden.log` | PASS: оба снимка детерминированы (фикс. время + фикс. данные через `fakeProvider`); 80×24 — компактный режим (<100 колонок), 120×40 — полный (колонки TURN/GPU/VRAM/KV/GEN/EXT = «—» до Э6). Тесты PASS |
| 4 | событие → перерисовка ≤ 250 мс | `go test ./internal/tui/ -run TestTUI_RedrawLatency -v` | `tui-golden.log` | PASS: перерисовка = перечитывание среза (4 GET) + отрисовка; замер реального `Snapshot()` против локального HTTP-сервера: worst over 20 = **897 мкс** ≤ 250 мс. Диспетчеризация события → refresh — канальный вызов (<1 мс), опроса нет |
| 5 | колонка WHY и команда `aqm why` есть | `aqm q` (колонка WHY) + `aqm why <t>` / `--json`; TUI — колонка WHY в очереди | `cli-json.txt` (`q`, `why`), `tui-live-120x40.txt`, golden | PASS: `aqm q` печатает колонку WHY (`prefer_wait`, `pin_down`, …); `aqm why <t>` даёт причину/кандидатов/предикаты (приложение А); TUI-очередь несёт WHY; live TUI показывает колонку WHY |

## Дополнительно (строгие правила, поведение TUI/CLI)

| Проверка | Команда | Артефакт | Результат |
|---|---|---|---|
| `make check` (vet, staticcheck, test -race, check-literals) | `make check` | `make-check.log` | PASS: vet/staticcheck 0 замечаний, литералы только 0/1, тесты rc=0 |
| `go test -race ./...` | та же | `test-race.log` | PASS: все пакеты, race чист (вкл. новый `internal/tui`) |
| TUI не падает на неизвестном kind (TZ:1219) | `go test ./internal/tui/ -run TestTUI_UnknownEvent` | `tui-golden.log`, `tui_test.go` | PASS: `TOTALLY_UNKNOWN_KIND` показан как сырой JSON, приложение живёт |
| TUI live: state + SSE, вкладки, цвета, WHY | tmux-сессия `aqm` на 120×40, `capture-pane` | `tui-live-120x40.txt` | PASS: заголовок (коорд/очередь/ходы/пауза/время), Серверы, Очередь с WHY, События (реальные SSE), легенда клавиш; GPU/VRAM = «—» |
| адресация `<t>`: sid / имя / `host:tmux` / `--pane %N` / голый `%N` | `aqm {why,dequeue,enqueue,…}` с разными формами (живой стенд) | сессия в `cli-json.txt`; ручные прогоны | PASS: все формы разрешаются в sid; голый `%N` и `--pane` работают |
| CLI-команды раздела 11 | `aqm --help`; прогон каждой команды (живой стенд) | `cli-json.txt`, ручные прогоны | PASS: `ls q why servers enqueue dequeue requeue prio pin prefer unpin hold unhold set cancel peek attach server drain/undrain node drain/undrain pause resume stats events service` — все присутствуют и отвечают |
| `--agent` только qwen | `aqm run --agent foo` | `run.go` | PASS: иное значение — ошибка CLI (зафиксировано в Э2) |
| время — только `internal/clock`; пороги из конфига | `go run ./scripts/check-literals` | `make-check.log` | PASS: в scheduler/gateway/detect нет чужих литералов |

## Отклонения и решения (зафиксированы)

### 1. Метрики серверов в TUI — «—» до Э6
Колонки GPU/VRAM/KV/GEN t/s/EXT отрисовываются как «—»: телеметрия GPU —
раздел 14 ТЗ, этап Э6. Структура `ServerRow` уже несёт `slots_info`, метрики
подключатся в Э6 без изменения контракта TUI.

### 2. `GET /events?limit=N` — разовый JSON для `aqm events` без `-f`
ТЗ задаёт `aqm events [-f]` и SSE. Чтобы `aqm events` (без `-f`) давал журнал
без удержания соединения, тому же маршруту `/events` добавлен параметр
`?limit=N` → разовый JSON-ответ (последние N). SSE (без `limit`) не изменён.

### 3. `peek`/`cancel` требуют живую панель
`aqm peek`/`cancel` диспетчируют команду узлу; при отсутствии панели сессии —
`404 PANE` / `502 CANCEL`. Это ожидаемое поведение (раздел 8 ТЗ), не баг.

### 4. `requeue` по умолчанию в начало; `--back` — в конец
`POST /sessions/{t}/requeue {back}` — `back=false` (по умолчанию) → в начало,
`--back` → в конец. CLI-флаг `--back` — булево.
