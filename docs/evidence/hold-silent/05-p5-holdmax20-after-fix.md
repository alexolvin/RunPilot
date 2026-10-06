# П.5 — hold_max_sec=20, ПОСЛЕ правок (PASS)

Дата: 2026-10-06. Stand: `docs/evidence/hold-silent/stand`.

**Бинарник: `/tmp/hl/runpilot`**
**SHA256 = `52e821f6fd96fee266e6bdf9e425460d4aa1799fc33bce75d2b8cf565f96abe4`**

Конфиг: `HL_HOLD_MAX_SEC=20` → в БД `gateway.hold_max_sec=20` (launcher точечно;
`turn.approval_hold_max_sec=600`, `turn.tool_hold_max_sec=900` — не тронуты).
`firstByteMs=40000`, 1 слот fakellm.
A sid `82X0NJW5XZ` (RUNNING, аренда ACTIVE granted_at=06:57:24.095276762, держит слот),
B sid `0NF0P4CK38` (сырой HTTP-клиент через gateway).

## Замер 1 — 503 + Retry-After + runpilot_no_slot (PASS)

`curl -i POST /s/0NF0P4CK38/v1/chat/completions` (сырой клиент, слот занят A):
```
HTTP/1.1 503 Service Unavailable
Content-Type: application/json
Retry-After: 30
Date: Tue, 06 Oct 2026 06:57:46 GMT
--- curl: time_total=20.001339s http_code=503
```
тело: `{"error":{"message":"runpilot: no free slot, queue position 1","type":"server_error","code":"runpilot_no_slot"}}`

| Критерий | Измерено | Статус |
|---|---|---|
| hold_max_sec=20 → 503 через ~20 с | **time_total=20.001339 с** | ✅ PASS |
| http_code=503 | 503 | ✅ PASS |
| Retry-After | **30** | ✅ PASS |
| тело `runpilot_no_slot` | code=`runpilot_no_slot` | ✅ PASS |

## Замер 2 — enqueued_at при повторе (PASS, был FINDING)

Три последовательных held-запроса B (каждый → `holdPass` → `QueueUpsertHeld`):
```
запрос #1  start=06:57:26.850  (→ 503 через 20 с)
запрос #2  start=06:58:17.981  (held, A ещё держит слот)
запрос #3  start=06:58:19.987  (held, A ещё держит слот)
```
```
enqueued_at после #1 = 2026-10-06T06:57:26.856351951Z
enqueued_at после #2 = 2026-10-06T06:57:26.856351951Z   (не изменился)
enqueued_at после #3 = 2026-10-06T06:57:26.856351951Z   (не изменился)
```
`queue_entry B`: sid=0NF0P4CK38 class=1 held_request=1 attempts=0 mode=submit.

| Критерий | Измерено | Статус |
|---|---|---|
| Повтор (CONFLICT) **не сбрасывает** enqueued_at | 06:57:26.856 сохранён через 3 holdPass (попытки #2 в +51 с, #3 в +53 с) | ✅ PASS |

## Сравнение с ДО правок (05-p5-holdmax20-measurement.md — FINDING)

| Показатель | ДО (FINDING) | ПОСЛЕ |
|---|---|---|
| 503/Retry-After/runpilot_no_slot | работает | работает |
| enqueued_at при повторе | **сброшен** (02:36:01 → 02:36:50) | **сохранён** (MIN) |
| Механизм | `QueueUpsert` (overwrite enqueued_at) | **`QueueUpsertHeld`** (MIN(enqueued_at)) |

## Вывод

**П.5 PASS.** (1) hold_max_sec=20 → 503 + Retry-After(30) + `runpilot_no_slot`
через ровно 20 с (time_total=20.001339 с). (2) FINDING устранён: повтор
(`holdPass` → `QueueUpsertHeld`) **не сбрасывает** `enqueued_at`
(`MIN(enqueued_at)`), исходный момент поступления сохраняется.
