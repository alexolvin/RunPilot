# П.5 — hold_max_sec=20 (FINDING: 503/Retry-After OK; enqueued_at при повторе сбрасывается)

Stand: `HL_HOLD_MAX_SEC=20` (в БД `gateway.hold_max_sec=20`, launcher сдил точечно;
`turn.*_hold_max_sec` не тронуты). A=env (держит слот 360 c), B=noenv, слот занят A.

## Замер 1 — 503 + Retry-After + runpilot_no_slot (PASS)
`curl -i POST /s/H9N3D9CAT0/v1/chat/completions` (сырой HTTP-клиент, слот занят A):
```
HTTP/1.1 503 Service Unavailable
Retry-After: 30
Content-Type: application/json
{"error":{"message":"runpilot: no free slot, queue position 1","type":"server_error","code":"runpilot_no_slot"}}
--- time_total=20.001714 http_code=503
```
Критерий: hold_max_sec=20 → клиент получает **503 + Retry-After(30) + тело `runpilot_no_slot`**
через ровно 20 с. **Соблюдено** (time_total=20.001714, http_code=503, code=runpilot_no_slot).

## Замер 2 — enqueued_at при повторе (FINDING: не сохраняется)
```
enqueued_at ПЕРЕД retry  : 2026-10-06T02:36:01.209526439Z  (исходный, из Замера 1)
retry: POST → http_code=503 time_total=20.002047 (удержание 20с → 503)
enqueued_at ПОСЛЕ retry  : 2026-10-06T02:36:50.314115784Z  (ОБНОВЛЁН, исходный НЕ сохранён)
```
Каждый повтор клиента (новый запрос → `holdPass`) вызывает `QueueUpsert`, который
`ON CONFLICT(sid) DO UPDATE SET enqueued_at = excluded.enqueued_at` — **перезаписывает**
enqueued_at на текущий момент. Исходный enqueued_at (02:36:01) утрачен.

## Критерии
| # | Критерий (ТЗ) | Измерено | |
|---|---|---|---|
| 1 | hold_max_sec=20 → 503 + Retry-After + тело `runpilot_no_slot` | 503, Retry-After=30, code=runpilot_no_slot, 20.002 c | ✅ |
| 2 | Каждый повтор — удержание с **исходным** enqueued_at | enqueued_at 02:36:01 → 02:36:50 (сброшен) | ❌ |

## Вывод (FINDING)
503/Retry-After/runpilot_no_slot — работают по ТЗ. Но **enqueued_at не сохраняется**:
`QueueUpsert` перезаписывает его на каждом повторе (`holdPass`), поэтому позиция
«первоначального поступления» теряется (важно для FIFO/aging и честной очереди).
→ В «реализацию по списку» (после п.10): при повторе (CONFLICT) **не сбрасывать**
enqueued_at (сохранить исходный: `enqueued_at = MIN(queue_entry.enqueued_at,
excluded.enqueued_at)` или не трогать поле при CONFLICT), либо вести отдельное поле
`first_enqueued_at`.

Примечание: механизм — `holdPass` → `QueueUpsert(EnqueuedAt: now)` на каждый вход;
`heldToResume` (`QueueSetHeldRequest`) enqueued_at не трогает, сброс даёт именно
повторный `QueueUpsert`.
