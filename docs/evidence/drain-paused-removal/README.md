# П3 — DRAINING не снимает ходы + удаление PAUSED (2026-10-18)

## Изменения

1. **DRAINING не снимает идущие ходы** — `internal/scheduler/scheduler.go`
   `ServerState`: `if st != model.ServerDraining { s.serverDownLocked(...) }`.
   Раньше DRAINING снимал идущие ходы (`serverDownLocked`); теперь — нет.
2. **PAUSED удалён** (дублирует DRAINING, в ТЗ отсутствует):
   - `internal/model/model.go` — `ServerPaused`.
   - `internal/store/operator_state.go` — `OperatorStateServerPaused`.
   - `cmd/runpilot/serve.go` — `SetServerPause`.
   - `internal/scheduler/scheduler.go` — `RestoreOperatorStates` (pause).
   - `internal/api/server.go` — `SetServerPause` + маршрут `/pause`.
   - `internal/api/views.go` — `handleServerPause`.
   - `internal/scheduler/operator_state_test.go` — тест pause.
3. **UI** (`web/app/views/servers.js` + `i18n/ru.js`):
   - Кнопка «Вывести из выдачи» (UP) / «В выдачу» (DRAINING).
   - Тултип: «Новые ходы не выдаются, идущие доделываются».
   - Диалог `ServerDrain` и кнопка паузы убраны.

## Проверка

- `go build ./...` — OK.
- `go test ./internal/...` — OK.
- `lint-web` — OK.
- Браузер (headless Chromium, временный координатор `/tmp/rp`):
  - UP/DOWN: кнопка «Вывести из выдачи», тултип «Новые ходы не выдаются,
    идущие доделываются» (см. DOM-выжимку ниже).
  - DRAINING: кнопка «В выдачу» (без тултипа).

### DOM-выжимка (headless)

UP/DOWN (badge «Недоступен»):
```json
{"buttons":[{"label":"Редактировать"},{"label":"Вывести из выдачи","title":"Новые ходы не выдаются, идущие доделываются"},{"label":"Удалить сервер"}]}
```

DRAINING (badge «Обслуживание»):
```json
{"buttons":[{"label":"Редактировать"},{"label":"В выдачу"},{"label":"Удалить сервер"}]}
```

## Скриншот

`server-detail-draining.png` — детальная страница сервера в состоянии DRAINING
(кнопка «В выдачу»).
