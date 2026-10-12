# R2 CONTROL — Relay: автомат отправки/ожидания + fakeorch

## PASS

| № | Проверка | Команда | Результат |
|---|----------|---------|-----------|
| 1 | Извлечение всех форм ответа (10 тестов) | `go test -race ./internal/relay/ -run TestExtract -v` | PASS |
| 2 | FUSE_CYCLES на виртуальных часах | `go test -race ./internal/relay/ -run TestFusedCycles -v` | PASS |
| 3 | Нормализация + хеш | `go test -race ./internal/relay/ -run TestNextState -v` | PASS |
| 4 | waitMinSendInterval на виртуальных часах | `go test -race ./internal/relay/ -run TestWaitMinSendIntervalVirtual -v` | PASS |
| 5 | Полный набор тестов (race) | `go test -race ./...` | exit 0 |
| 6 | staticcheck | `CGO_ENABLED=0 staticcheck ./...` | exit 0 |
| 7 | lintlits (числовые литералы) | `go run ./tools/lintlits ./...` | OK |
| 8 | lintweb (цвета/кириллица/числа) | `go run ./tools/lintweb ./...` | OK |
| 9 | linthosts (хосты/CGNAT) | `go run ./tools/linthosts ./...` | OK |

## BLOCKED (браузер: CDP Page.navigate HTTP-подвисание)

| № | Проверка | Причина |
|---|----------|---------|
| 10 | 100 отправок: хеш совпадает с отправленным | page.Navigate(HTTP) висит в CDP (about:blank работает) |
| 11 | 2 .md вложения: побайтово идентичны на /_received | то же — навигация к чату |
| 12 | NEEDS_LOGIN: logged_out_selector виден | то же |
| 13 | ORCH_RATE_LIMIT: hold по тексту | то же |
| 14 | kill -9 координатора → восстановление из SQLite | то же |

Интеграционный тест за бейд-тагом `//go:build integration`:
`go test -race -tags=integration ./internal/relay/`

## NOT DONE (входит в координаторную обвязку, R2-следующий PR)

| № | Что |
|---|-----|
| 15 | Координатор: вызов Relay из turn_report/read_report_file |
| 16 | Координатор: запись relay_link/relay_cycle в store (runtime) |
| 17 | SSE события RELAY_* |
| 18 | Уникальный маркер в БД/журнале |
| 19 | Временный каталог вложений (пустой после цикла) |

## Артефакты

- `internal/orchsite/` — профиль + извлечение
- `internal/relay/` — автомат (send/wait/fuses/extract)
- `internal/testutil/fakeorch/` — двойник сайта
- `orchestrators/fakeorch.yaml` + `fakeorch.js` — профиль
- `cmd/runpilot-fakeorch/` — standalone-запуск двойника
- `internal/store/migrations/022_relay.sql` — схема
