# W15 Этап 0 — Диагностика до правок кода

Дата: 2026-10-11. Узел диагностики: 3p14-aaa (координатор, PID 14276 `/home/user/.local/bin/runpilot serve`).
Версия координатора: `4580797` (HEAD main). Узлы: 3p14-aaa `4580797` (embedded), vzu5-claw `dfed163`.

## 1. Живые сессии qwen — cmdline + environ

Требование: `--auth-type openai`, `--model "<алиас>"`, `--openai-base-url <gateway_url>/s/<sid>/v1`.

| SID | Имя | Узел | PID (root) | cmdline (коротко) | environ |
|-----|-----|------|-----------|-------------------|---------|
| C66PZQ88PA | BotBoss | 3p14-aaa | 3608824 | `qwen --approval-mode yolo --auth-type openai --model "qwen3.8-27b (RunPilot)" --openai-base-url http://100.124.155.106:8787/s/C66PZQ88PA/v1` | OPENAI_API_KEY=<masked>, OPENAI_BASE_URL=http://100.124.155.106:8787/s/C66PZQ88PA/v1, OPENAI_MODEL=qwen3.8-27b (RunPilot), QWEN_CODE_API_TIMEOUT_MS=5400000 |
| E4MHXQHSBY | (E4MHXQHSBY) | 3p14-aaa | 3739166 | `qwen --approval-mode yolo --auth-type openai --model "qwen3.8-27b (RunPilot)" --openai-base-url http://100.124.155.106:8787/s/E4MHXQHSBY/v1` | OPENAI_API_KEY=<masked>, OPENAI_BASE_URL=http://100.124.155.106:8787/s/E4MHXQHSBY/v1, OPENAI_MODEL=qwen3.8-27b (RunPilot), QWEN_CODE_API_TIMEOUT_MS=5400000 |

**Итог 1: PASS.** Обе сессии соответствуют требованию. `QWEN_CODE_SYSTEM_SETTINGS_PATH` / `QWEN_CODE_SYSTEM_DEFAULTS_PATH` ещё не заданы (это работа этапа 3). Дефектов запуска нет.

## 2. Источник 100.126.222.23:8004

Файл: `~/.qwen/settings.json` (операторский).
Ключ: `modelProviders.openai-vzu5[0].baseUrl` = `"http://100.126.222.23:8004/v1"` (строка 63).
Повторно: `/mdisk/myhub/BotBoss/.qwen/settings.json` строка 59 (тот же baseUrl).

Это запись **операторского** провайдера для прямого (внеочередного) доступа к vzu5; управляемые сессии ходят через шлюз (OPENAI_BASE_URL = gateway), поэтому коллизии id с model_alias нет (алиас `qwen3.8-27b (RunPilot)` ≠ id `qwen3.8-27b`). По установившемуся в задаче: к причине не относится.

## 3. Состояние серверов из /api/v1/state

| Сервер | state | slots | used | slot state | priority | примечание |
|--------|-------|-------|------|-----------|----------|-----------|
| 3p14   | UP    | 1     | 0    | external  | 10       | gpu 100%, 674 W, kv 38.6% |
| vzu5   | DOWN  | 1     | 0    | free      | 5        | metrics_missing |

`gateway` из state: `url=http://100.124.155.106:8787`, `model_alias=qwen3.8-27b (RunPilot)`.

## 4. Шлюз — текущее состояние (до правок)

- **Тип декодирования тела:** `map[string]any` (`internal/gateway/body.go:transformBody`).
  Всё, кроме `model`/`max_tokens`/`max_completion_tokens`, проходит как `any` (вложенные объекты → `map[string]any`).
  → Этап 1 требует `map[string]json.RawMessage` (типовизированные структуры сообщений запрещены).
- **Эффективное `gateway.max_body_mb`:** 32 (default из `defaults.go`; секции `gateway:` в `~/.config/runpilot/config.yaml` нет).
- **`max_buffered_mb`:** отсутствует (новый ключ этапа 1).
- **ReadTimeout / WriteTimeout / ReadHeaderTimeout** сервера шлюза: `ReadTimeout=0`, `WriteTimeout=0`, `ReadHeaderTimeout=0` (не задан; `newHTTPServer`, `cmd/runpilot/serve.go:49`).
- **Строка request:** поля `model.Request` без `BodyBytes`/`body_bytes` (`internal/model/model.go:317`).
- **/metrics:** гистограмма размера тела отсутствует (`internal/gateway/metrics.go`).

### Дефекты/работы, вытекающие из диагностики
1. `transformBody` → `map[string]json.RawMessage` (этап 1.1).
2. `max_body_mb` 32 → 128, новый `max_buffered_mb` = 1024 (этап 1.2).
3. `ReadHeaderTimeout` из ключа `defaults.go` (этап 1.3).
4. `body_bytes` в строке request + гистограмма размера тела (этап 1.4).
