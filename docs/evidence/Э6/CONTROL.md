# Э6 — Мониторинг и GPU: CONTROL

Дата: 2026-09-25. Все строки CONTROL выполнены.

Стэнд: `/tmp/aqm-e6/` (копия — `stand.sh`, `config.yaml` в этой папке):
`aqm-fakellm` (двойник vLLM, model `fake-e6`, `127.0.0.1:8801`, управляемые
`running/waiting/kv` через `/_set-metrics`), «мёртвый» сервер `dead`
(`127.0.0.1:8899` — ничего не слушает), координатор `aqm serve`
(API `127.0.0.1:7791`, gateway `7792`, `external_confirm_sec: 3`,
`metrics_interval_sec: 1`), узел `aqm node` на сокете `aqmtest`
(`gpu: nvidia`, `gpu_server: fa` — реальный `nvidia-smi`, 2 карты).

| # | Строка CONTROL (ТЗ) | Команда проверки | Артефакт | Результат |
|---|---|---|---|---|
| 1 | разбор записанных `/metrics` реальных серверов и выводов nvidia-smi/rocm-smi — 100% | `go test ./internal/monitor/ -v -run 'TestParseVLLMMetricsReal\|TestParseNvidiaReal\|TestParseROCm'` | `monitor-test.log`, `testdata/fixtures/metrics/vllm_real.txt`, `testdata/fixtures/gpu/{nvidia_real.csv,amd_real.json}` | PASS: `vllm_real.txt` (688 строк, 434 `vllm:`, 78 семейств, записан 2026-09-25 read-only GET с реального vLLM `127.0.0.1:8004`, model `qwen3.8-27b`) разбирается со значениями 1:1 — running 0, waiting 0, kv 0, `generation_tokens_total` 1.018197e+06, `prompt_tokens_total` 1.6746833e+08; `Found` = 78 семейств (независимый подсчёт по `# TYPE`); `nvidia_real.csv` (2 карты, записан с машины) → util 98/100%, VRAM 23056/24576 MiB → 22.5/24 GiB, power 337/325 Вт; `amd_real.json` (формат `rocm-smi --json`, 2 карты; **синтетика** — AMD-драйвера на машине нет, зафиксировано) → 87%/0%, 54/64 GiB. Сумма по наборам меток, `waiting` не путается с `waiting_by_reason` — отдельные тесты |
| 2 | EXTERNAL ровно через `external_confirm_sec` на виртуальных часах | `go test ./internal/monitor/ -v -run TestExternalExactlyConfirm`; `go test ./internal/scheduler/ -run 'TestS8'` | `monitor-test.log` | PASS: на виртуальных часах при ext=2 (running 1 + waiting 1, inflight 0) оба слота НЕ EXTERNAL при `confirm−0.1 с` и EXTERNAL при `confirm` (±0.1 с); новая запись не выдаётся с `reason=external`; ext=0 → обратно НЕ раньше `confirm`, выдача после. Регрессия `TestS8bExternalFullLoad` (все слоты EXTERNAL, ext ≥ слотов) — держится 10 тиков подряд, слоты `external` видны в `ServersView` |
| 3 | отсутствие метрик → WARN doctor и `metrics_missing=true`, не молчаливый ext=0 без флага | `go test ./internal/monitor/ -v -run TestMetricsMissing`; `go test ./internal/doctor/ -v -run 'TestCheckMetrics\|TestCheckRequireDirect'`; живой стэнд (сервер `dead`) | `monitor-test.log`, `doctor_cli.txt`, `api_servers_p0.json` | PASS: недоступный `/metrics` → `metrics_missing=true`, ext=0, в API `ext` **опущен** (не 0), TUI/CLI «—»; doctor: `WARN metrics — dead: http://127.0.0.1:8899/metrics недоступен (metrics_missing)`; `GET /api/v1/doctor`: `{"name":"dead","state":"DOWN","metrics_missing":true}`. Отдельный required-метрик, которого нет в ответе → WARN со списком отсутствующих и найденных имён (`TestCheckMetricsMissingMetric`) |

## Живой стэнд (дополнительно, разделы 5/10/13 ТЗ)

| Проверка | Команда | Артефакт | Результат |
|---|---|---|---|
| GPU-колонки с реальным nvidia-smi | `aqm servers`; сверка с `nvidia-smi --query-gpu=…` | `servers_timeline.txt`, `nvidia_actual.csv`, `api_servers_p0.json` | PASS: `gpu_cards` узла → `gpu`/`vram_used_gb`/`vram_total_gb` в API и CLI; значения совпадают с прямым `nvidia-smi` (P0: 100%, 23056/24576 MiB → 45.0/48 GiB суммарно); без GPU/метрик — «—» |
| EXTERNAL→FREE на живом координаторе | `/_set-metrics?running=2&waiting=1` → `aqm servers` через 8 с; затем `running=0&waiting=0` | `servers_timeline.txt`, `api_servers_p1.json`, `api_servers_p2.json` | PASS: ext=3, `kv_pct=42` (0.42×100), `slots_info` = `external`,`external`; после разгрузки ext=0 и слоты `free` (подтверждение тем же `external_confirm_sec`) |
| ping координатор→узел: соединение > 2 мин без реконнекта | стэнд живёт ~2.5 мин; `grep -c "подключился" coord.log` | `stand.log` | PASS: подключений узла ровно 1 (read deadline узла 2 мин; ping каждые 15 с продлевает его через ping-хендлер узла) |
| doctor (CLI + API) | `aqm doctor`; `GET /api/v1/doctor` | `doctor_cli.txt`, `doctor_p0.json` | PASS: rc=0; Э6-строки: `WARN metrics — dead: … (metrics_missing)`, `PASS require_direct — 1 серверов: один vLLM на апстриме` (fakellm: ровно 1 модель `fake-e6`, без редиректов) |

## Дополнительно (строгие правила, проверки)

| Проверка | Команда | Артефакт | Результат |
|---|---|---|---|
| `make check` (vet, staticcheck, test, check-literals) | `make check` | `make-check.log` | PASS: vet/staticcheck 0 замечаний, в scheduler/gateway/detect нет литералов кроме 0/1, тесты rc=0 |
| `go test -race ./...` | `go test -race ./...` | `test-race.log` | PASS: все пакеты, race чист (вкл. новые `internal/monitor`, `cmd/aqm-fakellm`) |
| TUI golden 80×24/120×40 с колонками GPU/VRAM/KV/GEN/EXT | `go test ./internal/tui/` | `internal/tui/testdata/*.golden` | PASS: сервер с метриками — «98% 23/24 42% 13 1», без метрик — «— — — — —» (EXT не 0 молча) |
| pinger координатора | `go test ./internal/api/ -run 'TestPinger\|TestStartPinger'` | `hub_test.go` | PASS: ping-кадр доходит до узла, пустой реестр — без ошибок, горутина завершается по ctx |
| GPU-цикл узла (фиксированная команда, без оболочки) | `go test ./internal/node/ -run TestRunGPU` | `gpu_test.go` | PASS: аргументы `nvidia-smi`/`rocm-smi` совпадают 1:1, MiB→GiB, ошибка → WARN без send |
| fakellm `/metrics` в формате vLLM | `go test ./internal/testutil/fakellm/` | `fakellm_test.go` | PASS: разбор монитором, управляемые running/waiting/kv, счётчики растут на запросах |

## Отклонения и решения (зафиксированы)

### 1. `amd_real.json` — синтетика (формат реального `rocm-smi --json`)
AMD-драйвера на машине стэнда нет (только NVIDIA). Фикстура собрана по
задокументированному формату вывода `rocm-smi --showuse --showmeminfo vram
--showtemp --showpower --json` (ключи `cardN`, значения число/строка).
Парсер `ParseROCm` покрывает оба варианта типов (число и строка) —
`TestParseROCmReal`, `TestParseROCmStrings`.

### 2. `vllm_real.txt` записан с сервера оператора (read-only)
Один `GET /metrics` с `127.0.0.1:8004` (2026-09-25) — read-only, без
изменений состояния; в стэнды/тесты пошло только тело ответа.

### 3. BUG Э4, найден и исправлен в Э6: `externalWant` осциллировал
Старый `scheduler.externalWant` считал `min(ext, freeSlots())`, а
`freeSlots()` исключал EXTERNAL-слоты → при ext ≥ числа слотов (все слоты
EXTERNAL — типичный случай полной внешней нагрузки) получался 0, и
EXTERNAL сносился следующим тиком. Исправлено: «свободный» = не LEASED и не
COOLDOWN. Регрессия `TestS8bExternalFullLoad` (проверена на застарелом коде:
FAIL → на новом: PASS).

### 4. EXTERNAL в `slots_info` — «первые N свободных»
Планировщик держит EXTERNAL **по числу** (резерв слотов, не привязка к
номерам). Снимок `ServersView` помечает первыми N свободными (не LEASED/
COOLDOWN) слотами состояние `external` — оператор видит резерв в
`aqm servers --json`/`/api/v1/servers` (колонки TUI/CLI ориентированы на
сессии; численно резерв также виден в `ext`).

### 5. Пинг продлевает read deadline узла через ping-хендлер
WS-пинг потребляется внутри `ReadJSON` (авто-pong, до цикла чтения не
доходит) — без явного действия узла соединение умирало ровно через
`wsReadDeadline` (2 мин) при простое. Узел ставит `SetPingHandler`, который
продлевает `SetReadDeadline` и отвечает pong (раздел 13 ТЗ: канал не должен
жить вечно, но и не должен умирать от нормального пинга).

### 6. `kv_pct` — проценты, не доля
`vllm:kv_cache_usage_perc` — доля 0..1 (семантика vLLM). В
`Sample`/`HistoryPoint`/API (`kv_pct`) хранятся проценты (×100);
`VLLMMetrics.KV` (сырой разбор) — доля. Live-проверка: 0.42 → `kv_pct: 42`.

### 7. Спарклайны и «сводка в tmux/Telegram» — вне строк CONTROL Э6
Требование раздела 10 «координатор хранит окно `history_window_min` минут с
шагом интервала метрик» выполнено: окно хранится в памяти
(`monitor.History`, `HistoryPoints`, трим по `history_window_min`).
Отображение спарклайнов в панели деталей TUI и публикация сводки (tmux-
статус, Telegram `/q` — раздел 12) не входят в строки CONTROL Э6 и не
входят в скоуп утверждённого плана этапа; данные для них доступны
(`HistoryPoints`, `GET /api/v1/servers`), реализуются в Э7 (Telegram,
сводки) с пометкой в HANDOFF.
