# W12 Stage 4: HELD scenario с test=true

## Сценарий

1. Созданы две тестовые сессии (test=true, dir=/mdisk/myhub/test):
   - A: `held-test-a` (YQY0P3V2PP)
   - B: `held-test-b` (DWZMDC4J82)
2. Обе поставлены в очередь (enqueue)
3. Отправлен запрос к шлюзу для B (POST /s/DWZMDC4J82/v1/chat/completions)
4. Шлюз удержал запрос (HELD): `held_request=true`, `held_since` задан

## Артефакты

### /api/v1/queue (момент удержания)

```
DWZMDC4J82   held-test-b          held=True held_since=2026-10-09T09:28:23.085083828Z
YQY0P3V2PP   held-test-a          held=False held_since=
```

### /api/v1/state (сессии)

```
YQY0P3V2PP   held-test-a          QUEUED     held_since=
DWZMDC4J82   held-test-b          IDLE       held_since=
```

Примечание: HELD — это состояние записи очереди (held_request=true), не состояние сессии.
Сессия остаётся в IDLE/QUEUED, а запись очереди несёт held=true + held_since.

### tmux status line

```
MoxMate: 1 windows (created Sat Oct  3 05:52:05 2026) (attached)
RunPilot: 1 windows (created Sat Oct  3 01:49:18 2026) (attached)
acc-a: 1 windows (created Fri Oct  9 09:18:11 2026)
acc-b: 1 windows (created Fri Oct  9 09:20:44 2026)
```

### PNG screenshot

Не получен: playwright-core не установлен в окружении, browser-use требует
Chrome extension + node_repl MCP. Данные /api/v1/queue и /api/v1/state
содержат held=true + held_since, что является источником для бейджа HELD
и таймера в веб-UI.

### TURN_END events

Не получен: сессия B не завершала ход (запрос удерживался шлюзом).

## Тест QueueUpsertHeld held_since fix

Тест `TestQueueUpsertHeldMerge` в `internal/scheduler/queueupsert_test.go`
проверяет, что при повторном входе в удержание held_since не сбрасывается
(MIN semantics). Коммит 019386d: `CASE WHEN queue_entry.held_since = '0001-01-01T00:00:00Z'
THEN excluded.held_since ELSE MIN(queue_entry.held_since, excluded.held_since) END`.
