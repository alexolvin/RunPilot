# Э0 — CONTROL: PASS

Дата: 2026-09-24. Все строки CONTROL выполнены.

| # | Строка CONTROL | Команда проверки | Артефакт | Результат |
|---|---|---|---|---|
| 1 | `go test -race ./...` — 0 падений | `go test -race -count=1 ./...` | `test-race.log` | PASS (rc=0) |
| 2 | Тестами покрыты 100% переходов раздела 4 (число переходов в отчёте = числу в разделе, включая HOLD по PIN_UNAVAILABLE и NODE_DRAIN) | `go test -race -run 'TestSessionTransitionsComplete|TestTransitionCount|TestExhaustiveClassification|TestGoneIsTerminal|TestHoldReasonFor' ./internal/model/` | `automaton-transitions.md` | PASS: 32 перехода (31 строка + начальный), маппинг построчный; PIN_UNAVAILABLE — строка 18, NODE_DRAIN — строки 20–21, OPERATOR — строки 30–31 |
| 3 | Конфиг с неизвестным ключом → код выхода 2 | `aqm serve --config testdata/config.badkey.yaml; echo $?` | `exit-code-unknown-key.log` | PASS: exit_code=2 (serve и doctor) |
| 4 | В defaults.go есть все ключи раздела 13 | `go test -run TestDefaultsCoverSection13 ./internal/config/` | `config_test.go: section13Keys` (90 путей: топ-уровень, вложенные, листы; сверка через рефлексию в обе стороны) | PASS |
| 5 | В коде планировщика/шлюза/детектора нет литералов порогов | `go run ./scripts/check-literals` | `no-literals.log` | PASS: 0 литералов, кроме 0 и 1 (AST-проверка не-тестовых файлов) |

Дополнительно (каркас doctor, разделы 11/13/14):

| Проверка | Команда | Артефакт | Результат |
|---|---|---|---|
| doctor построчно, exit 1 при FAIL | `aqm doctor --config testdata/config.serve.yaml` | `doctor.log` | PASS: 6/6 PASS, exit 0 |
| T22: bind вне allow_cidrs → отказ запуска | `aqm serve --config testdata/config.badbind.yaml; echo $?` | `exit-code-unknown-key.log` (раздел 3) | PASS: отказ, exit 2 |
| CI: vet, staticcheck, test -race, literals | `.github/workflows/ci.yml` | файл | PASS (локально: `go vet`, staticcheck v0.8.1 — 0 замечаний) |
