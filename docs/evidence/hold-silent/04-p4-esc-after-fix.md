# П.4 — Esc во время удержания, ПОСЛЕ правок (PASS)

Дата: 2026-10-06. Stand: `docs/evidence/hold-silent/stand`.

**Бинарник: `/tmp/hl/runpilot`**
**SHA256 = `52e821f6fd96fee266e6bdf9e425460d4aa1799fc33bce75d2b8cf565f96abe4`**

Конфиг: `HL_PROFILE=env`, `hold_max_sec=600`, `firstByteMs=8000`,
1 слот fakellm. A sid `AM0W4CNS7S` (держит слот, RUNNING), B sid
`PKB025JHFW` (удержан: held_request=1, enqueued_at=06:51:17.929505217).

Сценарий: A RUNNING (аренда ACTIVE granted_at=06:51:14.625493931),
B удержан (holdPass, held_request=1). Esc (cancel_keys) отправлен в qwen B.

## Данные (БД, UTC, точные)

```
T_esc (shell)         = 2026-10-06T06:51:31.354Z
QUEUE_DEQUEUE B       = 2026-10-06T06:51:31.359300607Z  payload={"reason":"client_gone"}
request B (CLIENT_GONE)= t_start=2026-10-06T06:51:31.359301707Z  status=0  error=CLIENT_GONE
queue_entry B после    : УДАЛЕНА (пусто)
state B после          : IDLE
```

## Критерии (ТЗ, после правок)

| # | Критерий (ТЗ) | Измерено | Статус |
|---|---|---|---|
| 1 | Запись очереди **удалена** | **УДАЛЕНА** (queue_entry пустой) | ✅ PASS |
| 2 | Сессия **IDLE** | IDLE | ✅ PASS |
| 3 | **QUEUE_DEQUEUE {reason:"client_gone"}** ≤ 1 с после Esc | **5.3 мс** (06:51:31.359300607 − 06:51:31.354), payload `{"reason":"client_gone"}` | ✅ PASS |

Доп.: запрос B завершён `error=CLIENT_GONE, status=0` (не 200) — клиент
отменил; строка request с ошибкой CLIENT_GONE записана.

## Сравнение с ДО правок (04-p4-esc-measurement.md — FINDING)

| # | ДО (FINDING) | ПОСЛЕ |
|---|---|---|
| Запись очереди | остаётся (held_request 1→0, mode=resume) | **удалена** |
| Событие QUEUE_DEQUEUE | **нет** | **есть**, `{"reason":"client_gone"}` |
| Механизм | `heldToResume` | **`clientGone`** (QueueDelete + QUEUE_DEQUEUE + RequestError + IDLE) |

## Вывод

**П.4 PASS.** Расхождение с ТЗ устранено: при Esc во время удержания шлюз
вызывает `clientGone` → `QueueDelete` + `QUEUE_DEQUEUE {reason:"client_gone"}`
+ `RequestError(CLIENT_GONE)` + сессия IDLE. Событие ≤ 1 с (5.3 мс).
