# R3 CONTROL — доставка агенту (вставка+очередь, диалог вопроса)

## PASS (юнит-тесты, виртуальные часы/мок)

| № | Проверка | Команда | Результат |
|---|----------|---------|-----------|
| 1 | DeliveryProof kind=report (хеш последнего user-сообщения) | `go test -race ./internal/agentlog/ -run TestDeliveryProof -v` | PASS |
| 2 | DeliveryProof kind=question (ask_user_question: хеш/подпись) | `go test -race ./internal/agentlog/ -run TestDeliveryProof -v` | PASS |
| 3 | dialog_answer: шаги + выделенная строка (SENT) | `go test -race ./internal/node/ -run TestDialogAnswer -v` | PASS |
| 4 | dialog_answer: DIALOG_MISMATCH / BAD_KEYS / PANE_GONE / не в диалоге | `go test -race ./internal/node/ -run TestDialogAnswer -v` | PASS |
| 5 | kind=report: IDLE → вставка → очередь → доказательство | `go test -race ./internal/relay/ -run TestDeliver -v` | PASS |
| 6 | kind=question: dialog_answer → доказательство | `go test -race ./internal/relay/ -run TestDeliver -v` | PASS |
| 7 | OPERATOR_INPUT: оператор печатает → HOLD через input_wait_sec (виртуальные часы) | `go test -race ./internal/relay/ -run TestCheckIdle -v` | PASS |
| 8 | Панель становится IDLE в пределах input_wait_sec → доставка | `go test -race ./internal/relay/ -run TestCheckIdle -v` | PASS |
| 9 | DELIVERY_MISMATCH / DIALOG_MISMATCH (доказательство не найдено) | `go test -race ./internal/relay/ -run TestDeliver -v` | PASS |
| 10 | Полный набор (race) | `go test -race ./...` | exit 0 |
| 11 | staticcheck | `CGO_ENABLED=0 staticcheck ./...` | exit 0 |
| 12 | lintlits / lintweb / linthosts | `go run ./tools/lint*` | OK |

## NOT DONE (координаторная обвязка + реальный Qwen Code)

| № | Что | Почему |
|---|-----|--------|
| 13 | Координатор: подключение DeliverHooks (CheckIdle/Paste/Enqueue/Dialog/Proof) к узлу WS + планировщику + agentlog | Рантайм-обвязка координатора |
| 14 | Фикстуры диалога: ask_single, ask_multi, ask_multiselect, ask_free_text_active, ask_free_text_multiline | Нужны реальные экраны Qwen Code (источник истины — фикстуры) |
| 15 | 20 ответов (1 строка/много/кириллица/эмодзи/8 КБ/64 КБ) → реальный Qwen Code: доказательство 20/20 | Нужен реальный Qwen Code в /mdisk/myhub/test |
| 16 | ask_single свободный многострочный текст: результат 10/10; вариант 10/10; ask_multi 5/5; ask_multiselect 5/5 | Нужен реальный Qwen Code + фикстуры |
| 17 | Запрос разрешения на инструмент → связь остаётся ARMED, штатное уведомление | Координаторная обвязка |

## Артефакты

- `internal/textnorm/` — нормализация + SHA-256 (общий для relay/agentlog)
- `internal/agentlog/proof.go` — DeliveryProof (раздел 12)
- `internal/node/dialog.go` — dialog_answer (раздел 12.2)
- `internal/relay/deliver.go` — DeliverHooks + доставка (12.1/12.2) + OPERATOR_INPUT
- `profiles/qwen.yaml` — dialog_regex / dialog_selected_regex / dialog_keys
