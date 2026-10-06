# Этап 0 — live-результаты ДО правок

Дата: 2026-10-06. Стенд: 3p14-aaa, 127.0.0.1, порты 18787/18788/18790/18801.
Конфиг: `HL_PROFILE=env` (QWEN_CODE_API_TIMEOUT_MS=5400000), `hold_max_sec=600`
(п.5: 20), `first_byte_timeout_sec=900`, `cooldown_sec=2`, `tick_ms=500`, 1 слот fakellm.

## П.2 — silent-hold (артефакт: 02-p2-measurement.md)

B sid `52MBZXFHRZ`, A sid `CKVGVTQR25`. Запрос B удерживался, пока A генерировал (6 мин).

| # | Критерий | Измерено | Порог | Статус |
|---|---|---|---|---|
| 1 | held_ms B (1-й запрос) | **379 517 мс** (~379.5 с) | ≥ 300 000 | ✅ PASS |
| 2 | Запросов B с t_start в [Enter(B)=01:51:24.019, grant=01:57:43.536] | **1** | = 1 | ✅ PASS |
| 3 | Других B-запросов с t_start в окне | **0** (4 — после grant) | = 0 | ✅ PASS |
| 4 | LEASE_RELEASE(A)=01:57:43.535151 → grant(B)=01:57:43.536264 | **~1.1 мс** | ≤ 2 500 | ✅ PASS |
| 5 | t_first_byte(B)=01:57:43.538848 − grant(B)=01:57:43.536264 | **~2.6 мс** | ≤ 500 | ✅ PASS |

**П.2: PASS.** Побочное: аренда выдана **gateway self-grant** (не планировщик),
события LEASE_GRANT нет — эмитит только планировщик. Аренда IMPLICIT via
`LeaseCreateIfAbsent`. A-выдачи = 2 (2×6 мин).

## П.3 — контроль без таймаута (артефакт: 03-p3-control-measurement.md)

A=`PAT1PF7SFY` (env, 5 400 000 мс), B=`7NGNX2EBB1` (noenv, дефолт qwen 120 000 мс).

| # | Критерий | Измерено | Статус |
|---|---|---|---|
| 1 | Время первого разрыва клиентом (от Enter) | **~120 с** (enqueued_at 02:22:27.363 → 02:24:27.757) | ✅ (ожидаемо: дефолт qwen 120 с) |
| 2 | Повторных запросов B за 300 с | **1** (QUEUE_SKIP 02:22:27.695, 02:24:28.194) | ✅ (1 повтор в окне) |

**П.3: PASS (контроль).** Клиент без `QWEN_CODE_API_TIMEOUT_MS` не переживает
удержание >120 с. Запрос не завершён (нет строки request).

## П.4 — Esc во время удержания (артефакт: 04-p4-esc-measurement.md)

T_esc = 02:29:40.397755. B удержан (held_request=1, enqueued_at=02:29:20.852).

| # | Критерий (ТЗ) | Фактически | Статус |
|---|---|---|---|
| 1 | Запись очереди удалена | **НЕТ**: запись осталась, held_request 1→0, mode submit→resume | ❌ FINDING |
| 2 | Сессия IDLE | IDLE | ✅ |
| 3 | Событие QUEUE_DEQUEUE {reason:"client_gone"} ≤1 с | **НЕТ события** | ❌ FINDING |

**П.4: FINDING.** Реально: `heldToResume` (запись остаётся, mode=resume, без события).
Журнал: `QUEUE_DEQUEUE` пусто. БД: `queue_entry` не удалена, `enqueued_at` не изменился.

## П.5 — hold_max_sec=20 (артефакт: 05-p5-holdmax20-measurement.md)

A=env (держит слот 360 с), B=noenv.

| # | Критерий (ТЗ) | Фактически | Статус |
|---|---|---|---|
| 1 | 503 + Retry-After(30) + runpilot_no_slot через 20 с | 503, Retry-After=30, code=runpilot_no_slot, time_total=20.002 с | ✅ PASS |
| 2 | enqueued_at сохраняется при повторе | enqueued_at 02:36:01 → 02:36:50 (сброшен на now) | ❌ FINDING |

**П.5: FINDING (частичный).** 503/Retry-After работают. Но enqueued_at сбрасывается
на каждом повторе (`QueueUpsert` перезаписывает `enqueued_at = excluded.enqueued_at`).

## Итого

| Пункт | Статус |
|---|---|
| П.2 | ✅ PASS (5/5 критериев) |
| П.3 | ✅ PASS (контроль) |
| П.4 | ❌ FINDING (запись не удалена, нет события) |
| П.5 | ⚠️ Частичный (503 OK; enqueued_at сбрасывается) |
| П.10 | ❌ Self-grant шлюза (нет LEASE_GRANT) — устранено правками |
