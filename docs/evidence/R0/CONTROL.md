# R0 — Запуск Qwen Code + agentlog + turn_report/read_report_file: CONTROL

Дата: 2026-10-12. Все строки CONTROL выполнены. Ворота: `go test -race ./...` — 0
падений; `staticcheck ./...` — 0; `make lint` (lintlits/lintweb/linthosts) — 0.

Содержание (разделы 2, 3, 6 ТЗ Relay):
- `--session-id <uuid> --chat-recording` при spawn, `--resume <uuid>` при перезапуске
  (вместо `--continue`); `qwen_session_id` (UUIDv4) генерируется координатором и хранится
  в `session` (миграция 021).
- `internal/agentlog` — разбор записи Qwen Code (ход, сообщения, маркер, .md-файлы,
  ask_user_question) и истории терминала (запасной источник).
- Узловые команды `turn_report {sid}` и `read_report_file {sid, path}` (proto-минор:
  новые kind + коды PATH_FORBIDDEN/EXT_FORBIDDEN/TOO_LARGE).
- Конфиг `relay.*` (раздел 16) — все значения в `defaults.go` + схема.

## Строки CONTROL (раздел 18, R0)

| # | Строка CONTROL (ТЗ) | Команда проверки | Артефакт | Результат |
|---|---|---|---|---|
| 1 | Фикстуры записи Qwen Code (маркер, без маркера, write_file/edit .md, thought, сжатие, ask_single/multi/multiselect) — разбор 100%, отчёт и файлы побайто | `go test -count=1 ./internal/agentlog` | `internal/agentlog/agentlog_test.go` | PASS: 11 тестов (WithMarker, NoMarker, ThoughtExcluded, Files, QuestionSingle, QuestionAnswered, Multiselect, AskMulti, CompressionMidTurn + 2 терминала) — отчёт и список файлов побайто как ожидаемые |
| 2 | Фикстуры истории терминала — 100% | `go test -count=1 ./internal/agentlog -run TestTerminal` | `internal/agentlog/agentlog_test.go` | PASS: TestTerminalWithMarker (отчёт от ◆ до input_top), TestTerminalNoMarker |
| 3 | read_report_file: вне каталога (вкл. симлинк) → PATH_FORBIDDEN; не .md → EXT_FORBIDDEN; сверх лимита → TOO_LARGE | `go test -count=1 ./internal/node -run TestReadReportFileGuards` | `internal/node/relay_test.go` | PASS: ok/outside/symlink-outside/not-md/too-large/missing — все коды верные, содержимое корректного файла побайто |
| 4 | Перезапуск сессии запускает `--resume <uuid>` | `go test -count=1 ./internal/node` (wiring) | `internal/node/ops.go`, `internal/api/restart.go` | PASS: respawn добавляет `--resume <uuid>` при непустом QwenSessionID; spawn — `--session-id <uuid> --chat-recording`. Артефакт `/proc/<pid>/cmdline` снимается на живом узле при приёмке R7 |

## Дополнительно (строгие правила, проверки)

| Проверка | Команда | Результат |
|---|---|---|
| `go test -race ./...` | `go test -race ./...` | PASS: все пакеты ok, race чист |
| `staticcheck ./...` | `CGO_ENABLED=0 staticcheck ./...` | PASS: 0 |
| R2 (литералы/цвета/хосты) | `make lint` | PASS: lintlits/lintweb/linthosts 0 |
| Схема покрывает 100% рабочих ключей | `go test ./internal/config` | PASS: relay.* в схеме и configKeys |

## Ключевые технические решения

- **Граница хода** — записи после последней `type: user` без `subtype` (§6.1.2).
  `system/chat_compression` посреди хода не ломает разбор (не user/assistant).
- **Маркер** — снизу вверх, первая непустая строка содержит `relay.report_keywords`
  (подстрока, без учёта регистра); отчёт = маркер + поздние сообщения через пустую строку.
- **Файлы** — write_file/edit `.md` внутри каталога сессии, существуют; дубликаты по
  последнему изменению; лимит `relay.attach_max_files`.
- **read_report_file** — EvalSymlinks + Rel: симлинк вне каталога → PATH_FORBIDDEN.
