# Э2 — CONTROL: PASS

Дата: 2026-09-25. Все строки CONTROL выполнены.

Стенд: один узел `3p14-aaa` (tmux-сокет `aqmtest`, tmux 3.2a), координатор
`127.0.0.1:18788`, 10 панелей `e2proj1..10` (fake-агент `qwen` → `aqm-mockcoder`),
scan_interval_sec = 1. Запуск: `harness.log` (скрипт `/tmp/aqm-e2/harness.py`,
вне репозитория — стенд приёмки).

| # | Строка CONTROL (ТЗ) | Команда проверки | Артефакт | Результат |
|---|---|---|---|---|
| 1 | 10 панелей на `-L aqmtest`: обнаружение ≤ 2 с | `harness.py` фаза [1]: 10 × `aqm run --detach`, замер от запуска до появления панели в `GET /api/v1/state` | `harness.json` `detections_sec` | PASS: max=1015 мс, p95=1013 мс (все 10 ≤ 2000 мс) |
| 2 | снимок → координатор p95 ≤ 300 мс по `received_at` | `harness.py` фаза [2]: в каждую панель печатается символ (смена хеша) → новый `stable_since` (часы узла); задержка = `received_at` (часы координатора) − `stable_since`; 2 замера на панель | `harness.json` `snapshot_latency_ms` | PASS: n=20, p95=19 мс, max=20 мс |
| 3 | dispatch p95 ≤ 200 мс | `harness.py` фаза [3]: 2 раунда × 10 панелей `POST /api/v1/internal/dispatch {sid, mode:submit, expect_hash}` → ответ узла, `rtt_ms` от координатора | `harness.json` `dispatch_rtt_ms` | PASS: n=20, p95=4 мс, max=4 мс; все ответы `EMPTY_INPUT` (ввод пуст — легитимный код раздела 8, Enter не слан) |
| 4 | CPU узла ≤ 2% в среднем за 60 с | `harness.py` фаза [4]: utime+stime из `/proc/<pid>/stat` до/после 60 с работы с 10 активными панелями | `harness.json` `node_cpu_pct_60s` | PASS: 0.02% |
| 5 | повтор `aqm run` с тем же именем → 409 NAME_IN_USE | `harness.py` фаза [5]: повторный `aqm run --name e2proj1` → вывод клиента в op-панели | `harness.json` `name_in_use_lines`, `op-pane.txt` | PASS: `aqm: 409 NAME_IN_USE: имя занято сессией NEQ6KS80DE (IDLE)` |
| 6 | неизвестный proto → соединение закрыто | `harness.py` фаза [6]: `go run ./scripts/protocheck -proto 999` → hello с proto=999 | `harness.json` `protocheck`, `serve.log` | PASS: `close_code=1002 reason="неизвестная версия протокола"` |

Дополнительно (строгие правила и приёмка кода):

| Проверка | Команда | Артефакт | Результат |
|---|---|---|---|
| `make check` (vet, staticcheck, test -race, check-literals) | `make check` | `test-race.log` | PASS: vet/staticcheck 0 замечаний, тесты rc=0, «в scheduler/gateway/detect нет числовых литералов, кроме 0 и 1» |
| `go test -race -count=1 ./...` | та же команда | `test-race.log` | PASS: 10 пакетов с тестами, race-detector чист |
| host_ip hello обязан совпадать с IP подключения (раздел 13) | `internal/api/server_test.go` `TestNodeWSHostIPMismatchClosed` | `test-race.log` | PASS: hello с `host_ip=10.9.9.9` → close 1002 |
| коллизии `aqm run`: TMUX_SESSION_EXISTS, PANE_ALREADY_MANAGED, qwen-уже-запущен-без-sid | `cmd/aqm` (коды в `run.go`), ручная проверка на стенде | `run.go` | PASS: коды печатаются клиентом, 409-семантика |
| `aqm tmux-setup` идемпотентен (блок между маркерами) | `go test ./cmd/aqm/ -run TestTmuxSetup` | `test-race.log` | PASS: вставка / замена / идемпотентность |
| doctor: qwen_settings (selectedType=openai, alias не в modelProviders) | `go test ./internal/doctor/` | `test-race.log` | PASS: 3 сценария (OK / FAIL selectedType / FAIL alias) |
| `received_at` ставит координатор (строгое правило) | `internal/api/hub.go` `SetPane`/`SetPanes`: `h.clk.Now()` | `state-final.json` | PASS: узел в снимок `received_at` не передаёт |
| токен не в БД (строгое правило) | `internal/store/session.go` — колонки без токена; токен только env-переменная | схема `internal/store` | PASS |
| только профиль qwen | `handleRun`: profile≠qwen → 400 BAD_AGENT; `aqm run --agent` ≠ qwen → ошибка | `server_test.go` `TestRunBadAgent` | PASS |
| `make` CGO_ENABLED=0, один статический бинарник | `make build` | — | PASS (не изменилось с Э0) |

## Отклонения и решения (зафиксированы)

### 1. Сопоставление cmdline: сам процесс панели и его потомки

`matchesTree` (сканер, раздел 7) проверяет `cmdline_regex` сначала против
**самого процесса панели** (`#{pane_pid}`), затем — BFS по потомкам
(единый снимок `ps -A -o pid=,ppid=,args=` за цикл, 1 запуск).

Обоснование production-наблюдения (ps оператора, 2026-09-25): процесс панели
реального qwen-сеанса — `node /home/user/.local/bin/qwen`
(`qwen` — symlink на `cli-entry.js` с shebang node); его потомок
(`node cli.js ...`) с argv-именем уже не содержит `qwen`. Без проверки
самого процесса панели production-панели бы не обнаруживались.
Тестовый кейс: `%14` в `scanner_test.go`.

### 2. Периоды опроса панелей на узле

Карта session-state ТЗ → период снимка панели на узле:
BUSY → 1 с; IDLE / PROMPT / WAIT_UI → 2 с; UNKNOWN → 5 с;
плюс обязательный пульс не реже 10 с на панель даже без изменений.
Снимок отправляется при смене state/hash/ввода; `stable_since`
обновляется только при смене хеша (время узла).

### 3. `POST /api/v1/internal/dispatch` — замерный хук Э2

Эндпоинт «координатор → узел → reply» с замером `rtt_ms` существует для
приёмки Э2 (строка «dispatch p95 ≤ 200 мс»). В Э4 эту же примитивную
функцию `Hub.Dispatch` использует планировщик; эндпоинт остаётся
внутренним (под токеном) инструментом диагностики.

### 4. Пустой токен = локальный dev-режим

`token_env` пуст/не задан → авторизация на API выключена (dev-стенд).
В production токен обязателен (doctor проверяет наличие env-переменной).

### 5. `stable_since` сериализуется в RFC3339Nano

Формат `stable_since` — RFC3339 с дробной секундой
(`time.RFC3339Nano`). Без дробной части задержка «снимок → координатор»
(строка 2) квантовалась бы до секунды и замер по `received_at` терял бы
точность; `received_at` уже шёл в RFC3339Nano (стандартное
JSON-маршалирование `time.Time`).

### 6. Защита от мёртвого WS-канала: read deadline 2 мин

Чит-цикл канала узла ставит `SetReadDeadline(2 мин)` перед каждым
`ReadJSON`: соединение, потерявшее FIN/NAT-сессию, не живёт вечно —
узел переподключается по экспоненциальной задержке 1 → 30 с.

### 7. `host_ip` в hello узла

`host_ip` вычисляется узлом как локальный адрес UDP-соединения с
координатором (раздел 13: обязан совпадать с IP подключения).
Координатор при рассогласовании закрывает соединение (close 1002) —
защита от спуфинга hello.

### 8. `agent_version`

Узел сообщает `agent_version` в каждом снимке панели (раздел 13 ТЗ,
таблица сообщений: `pane` несёт `agent_version`); узел определяет её
один раз через `version_cmd` профиля при старте. Клиент `aqm run`
дополнительно передаёт версию в сессию (колонка `agent_version`,
раздел 9/таблица `session`) — для `--here` и диагностики;
`PATCH .../pane` сохраняет её через `CASE WHEN '' keep` (не затирает
пустую). На стенде обе версии совпали: `0.99.0-e2`
(узел запускался с тем же PATH, что и клиент).
