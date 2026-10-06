# П.10: виртуальные часы + реализация (П.4, П.5, П.10)

Дата: 2026-10-06. Статус: тесты зелёные, реализация применена.

## Тесты на виртуальных часах (hold_virtual_test.go)

Планировщик — единственный выдающий аренду. Три пути освобождения слота —
удержанная запись выдаётся планировщиком (LEASE_GRANT) в ТОМ ЖЕ тике:

| Тест | Путь | Результат |
|---|---|---|
| TestP10ExternalFree | EXTERNAL→FREE (метрики, confirm=3s) | PASS |
| TestP10DownUp | DOWN→UP (ServerState) | PASS |
| TestP10CooldownFree | COOLDOWN→FREE (closeTurn→cooldown→истёк) | PASS |

Каждый тест: ждущий (held_request=1) → слот занят (assertHeld: нет аренды,
нет LEASE_GRANT) → слот освобождается → tick → assertGrantedByScheduler
(аренда ACTIVE/IMPLICIT + LEASE_GRANT + запись очереди удалена).

## Тесты ранга и ёмкости (hold_rank_capacity_test.go)

| Тест | Что проверяет | Результат |
|---|---|---|
| TestHoldRankResumeBeatsHeld | RESUME > HIGH: X получает слот, B остаётся; B получает следующий | PASS |
| TestHoldCapacityOneSlotTenFrees | 1 слот, 3 held + 2 queue, 10 освобождений: активных аренд ≤ 1 | PASS |

## Реализация (lease.go)

### Self-grant удалён (П.10)

`holdPass` больше НЕ вызывает `g.grant` (self-grant). Цикл:
1. `QueueUpsert(held_request=true)` — запись в очередь.
2. `LeaseGet(own sid)` → если есть → проксирование.
3. `select`: `ctx.Done`→`clientGone` / `timer`→`heldToResume` / `wake`.

Аренду выдаёт ТОЛЬКО планировщик (`tickGrant` → `grant` → `LeaseCreateIfAbsent(ACTIVE, IMPLICIT)`).
Шлюз просыпается на `wake` (broadcast) и проверяет `LeaseGet(sid)` — возвращает
только по СВОЕЙ аренде.

### П.4: Esc/отмена во время удержания
- `holdPass` `ctx.Done()` → `clientGone(sid)`:
  - `QueueDelete(sid)` — запись очереди удалена.
  - `EventRecord(QUEUE_DEQUEUE, {reason:"client_gone"})`.
  - `RequestError(sid, "CLIENT_GONE", now)` — request.error = CLIENT_GONE.
  - Сессия QUEUED→IDLE (EvDequeue), если была QUEUED.
- Было: `heldToResume` (запись оставалась: held_request=0, mode=resume,
  без события). Это было FINDING в Этапе 0 (04-p4-esc-measurement.md).

### П.5: preserve enqueued_at / attempts / class при ретраях
- `QueueUpsert` при CONFLICT: `enqueued_at = MIN(old, new)`,
  `attempts = MAX(old, new)`, `class = MIN(old, new)` (меньший = выше приоритет).
- Повтор клиента (503→retry→holdPass) не сбрасывает позицию в очереди.
- Было: `QueueUpsert` с `EnqueuedAt: now` — позиция сбрасывалась на каждый
  ретраи. FINDING в Этапе 0 (05-p5-holdmax20-measurement.md).
- С11 (конец хода) использует `QueueSetEnqueuedAt` (прямой UPDATE), т.к.
  QueueUpsert сохраняет MIN.

## Тесты на исправления (hold_fix_test.go)

| Тест | Что проверяет | Результат |
|---|---|---|
| TestP4ClientGone | Esc→запись удалена + QUEUE_DEQUEUE + IDLE + request.error=CLIENT_GONE | PASS |
| TestP5PreserveEnqueuedAt | ретраи не сбрасывают enqueued_at / attempts / class | PASS |
| TestHoldSlotGranted | шлюз + планировщик: waiter получает аренду после Release | PASS |

## Полная прогонка

`go test ./...` — все пакеты зелёные (0 fall).

## Файлы

- `internal/gateway/hold_virtual_test.go` — 3 теста П.10 (виртуальные часы).
- `internal/gateway/hold_fix_test.go` — 3 теста на исправления.
- `internal/gateway/hold_rank_capacity_test.go` — 2 теста (ранг + ёмкость).
- `internal/gateway/lease.go` — holdPass: clientGone, нет self-grant.
- `internal/store/queue.go` — QueueUpsert MIN/MAX/MIN, QueueSetEnqueuedAt.
- `internal/store/request.go` — RequestError.
- `internal/store/migrations/012_queue_entry_attempts.sql` — колонка attempts.
- `internal/scheduler/complete.go` — S11: QueueSetEnqueuedAt.
- `internal/model/model.go` — QueueEntry.Attempts.
