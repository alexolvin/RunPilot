# R4 — CONTROL (ТЗ Relay §13)

Этап: R4 — LLM-ассистент связи (internal/llmassist)
Дата: 2026-10-12
Версия: 2026.12.11

## Гейты

| Команда | Результат |
|---------|-----------|
| `go test -race ./...` | PASS (exit 0) |
| `CGO_ENABLED=0 staticcheck ./...` | PASS (exit 0) |
| `go run ./tools/lintlits` | PASS |
| `go run ./tools/lintweb` | PASS |
| `go run ./tools/linthosts` | PASS |

## Реализация (PASS)

- [PASS] `internal/llmassist/` — пакет помощника: types.go, llmassist.go, prompts.go (embed), consts.go
- [PASS] T-REPORT: пронумерованные сообщения → `{"start": i}` | `{"start": null}`, i в диапазоне
- [PASS] T-ANSWER: пронумерованные блоки → `{"block": i}` | `{"quote": "…"}` | null
- [PASS] T-DIALOG: вопросы+опции+ответ → per-question `{"option": i}` | `{"options": [..]}` | `{"quote": "…"}`
- [PASS] Строгий JSON: невалидный → один повтор → HOLD(LLM_INVALID)
- [PASS] validQuote: точная подстрока, длина ≥ min_quote_chars; иначе HOLD(LLM_INVALID)
- [PASS] keepTail: обрезка входа с конца, целые сообщения/блоки
- [PASS] Конфиг: `llm_assist.{class,max_tokens,timeout_sec,max_input_kb,min_quote_chars}` + defaults + schema
- [PASS] Тесты: 20-случайные референс-наборы (report/answer/dialog) на мок-LLM — 0 ошибок доставки
- [PASS] Тесты: retry→valid, invalid-after-retry→HOLD, non-substring quote→HOLD, out-of-range start→HOLD

## НЕ СДЕЛАНО (для R5–R7)

- [NOT DONE] Проводка через шлюз под JOB-арендой `relay-assist` (temperature 0, class из конфига)
- [NOT DONE] Живой прогон: 20 реальных ответов + диалоги 10/10 (нужен реальный LLM)
- [NOT DONE] Проверка «0 прямых vLLM» (все вызовы — gateway `request` с sid JOB relay-assist)

## Артефакты

- internal/llmassist/{types,llmassist,prompts,consts}.go
- internal/llmassist/prompts/{report,answer,dialog}.md
- internal/llmassist/testdata/{report,answer,dialog}.json
- internal/llmassist/llmassist_test.go
- internal/config/{config,defaults,schema}.go (llm_assist)
- internal/config/config_test.go (configKeys)
- internal/api/settings_test.go (135→140)
