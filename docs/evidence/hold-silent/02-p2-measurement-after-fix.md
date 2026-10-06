# П.2 — live-замер ПОСЛЕ правок (PASS, финальный)

Дата: 2026-10-06. Стенд: `docs/evidence/hold-silent/stand` (127.0.0.1,
порты 18787/18788/18790/18801).

**Бинарник: `/tmp/hl/runpilot`**
**SHA256 = `a8bf04c80588da68f9334899f6f52b78aeb8419e5ddf92db52185016284eca56`**
**fakellm: `/tmp/hl/fakellm` SHA256 = `a1637f5e023c889dfeba737b705d3d250a86313bf8ae5ceaa95c1dccddd6902e`**
(HEAD: self-grant удалён, QueueUpsertHeld, clientGone, migration 012,
QueueUpsert-refactor; **client_timeout_sec** (A) + **граница тика** (Б);
fakellm: последний SSE-чанк несёт `finish_reason:"stop"`).

Метод (задачный, без отклонений): occupier A с `firstByteMs=360000`
(держит слот 360 с) → `/_set-delays firstByteMs=0` → Enter в B **≤ 30 с**
после старта запроса A. Профиль — env-переменные профиля.

Конфиг: `hold_max_sec=600`, `first_byte_timeout_sec=900`, `cooldown_sec=2`,
`tick_ms=500`, `done_quiet_sec=1`, `done_stable_sec=1` (override стенда),
1 слот fakellm. **`QWEN_CODE_API_TIMEOUT_MS=5400000` выставляет
`runpilot run` из `profiles.qwen.client_timeout_sec=5400`** — а не override
стенда (проверено в `/proc/<pid>/environ`, ниже).

A sid `ECXMJQDPTX`, B sid `YK68XCQYC0`.

## Данные (БД + события + pane, точные ts, UTC)

Запросы B:
```
YK68XCQYC0  t_start=08:57:49.544470043Z  t_end=09:03:52.053695849Z  held_ms=362506  status=200   <- УДЕРЖАН
YK68XCQYC0  t_start=09:03:52.211054765Z  t_end=09:03:52.211982503Z  held_ms=0       status=200   (после grant)
```

LEASE (авторитетные granted_at / released_at):
```
A ECXMJQDPTX  RELEASED  granted_at=08:57:47.528958205Z  released_at=09:03:49.549862391Z  reason=TURN_DONE
B YK68XCQYC0  RELEASED  granted_at=09:03:52.049813819Z  released_at=09:03:53.549912531Z  reason=TURN_DONE
```

События LEASE (точные ts):
```
09:03:49.549862391Z  LEASE_RELEASE  ECXMJQDPTX  {"reason":"TURN_DONE","server":"fakellm","slot":1}
09:03:52.049813819Z  LEASE_GRANT    YK68XCQYC0  {"held":true,"origin":"IMPLICIT","server":"fakellm","slot":1}
09:03:53.549912531Z  LEASE_RELEASE  YK68XCQYC0  {"reason":"TURN_DONE","server":"fakellm","slot":1}
```

Окно удержания B: `[t_start(удерж.) = 08:57:49.544470043Z, GRANT(B) = 09:03:52.049813819Z]`.
Enter B = 08:57:49.544 — это **2.015 с** после старта A (08:57:47.529) → ≤ 30 с ✓.

## Критерии (точные числа)

| # | Критерий | Измерено | Порог | Статус |
|---|---|---|---|---|
| 1 | held_ms B (удержанный запрос) | **362 506 мс** | ≥ 300 000 | ✅ PASS |
| 2 | B-запросов с t_start в окне удержания | **1** (удержанный) | = 1 | ✅ PASS |
| 3 | Других B-запросов с t_start в окне | **0** (2-й — 09:03:52.211, ПОСЛЕ grant) | = 0 | ✅ PASS |
| 4 | LEASE_RELEASE(A)→LEASE_GRANT(B) | **2499.95 мс** (09:03:52.049813819 − 09:03:49.549862391) | ≤ cooldown 2000 + tick 500 = **2500 мс** | ✅ PASS |
| 5 | send B upstream − LEASE_GRANT(B) | **3.882 мс** (t_end 09:03:52.053695849 − grant 09:03:52.049813819) | ≤ tick_ms = **500 мс** | ✅ PASS |
| 6 | LEASE_GRANT издан планировщиком | payload `{"held":true,"origin":"IMPLICIT"}` | — | ✅ PASS |
| 7 | Ошибки/повтор на экране qwen | A: **0**, B: **0** (pane: запрос → `c0`, без `✕`/retry) | = 0 | ✅ PASS |
| 8 | ts событий == ts аренды (Б) | RELEASE(A).ts=released_at **точно**; GRANT(B).ts=granted_at **точно**; RELEASE(B).ts=released_at **точно** | равенство | ✅ PASS |

## `QWEN_CODE_API_TIMEOUT_MS` из конфига координатора (не override стенда)

`/proc/3273139/environ` (qwen pid B):
```
QWEN_CODE_API_TIMEOUT_MS=5400000
OPENAI_MODEL=qwen3.6-27b
OPENAI_BASE_URL=http://127.0.0.1:18787/s/YK68XCQYC0/v1
RUNPILOT_SID=YK68XCQYC0
```
`5400000 мс = 5400 с × 1000` — ровно `profiles.qwen.client_timeout_sec=5400`
(значение по умолчанию). В конфиге стенда **нет** жёстко заданного
`QWEN_CODE_API_TIMEOUT_MS`: `profile.qwen.yaml` его не содержит,
`profile.qwen.noenv.yaml` удалён. Значение поставлено `runpilot run`
(окружение после env-переменных профиля, §A.4).

## Экран qwen B (capture-pane после ответа)
```
> Reply with exactly the word: OK
◆︎ c0
```
Без `✕ [API Error …]`, без «Press Ctrl+Y to retry». `count_errors` A и B = 0.

## Сравнение с ДО правок (02-p2-measurement.md)

| Показатель | ДО | ПОСЛЕ (финал) |
|---|---|---|
| Выдающий | gateway self-grant (нет LEASE_GRANT) | **планировщик** (LEASE_GRANT, held:true) |
| LEASE_RELEASE→GRANT | ~1.1 мс (self-grant по wake) | **2499.95 мс** (cooldown 2000 + tick 500) |
| held_ms | 379 517 мс | **362 506 мс** |
| t_end − GRANT | ~2.6 мс | **3.882 мс** |
| ts события vs ts аренды | событие эмитит только планировщик (self-grant его не давал) | **ровно равны** (eventAt: `now` тика) |
| Ошибки на экране | — | **0** (fakellm: finish_reason:"stop") |

## Вывод

**П.2 PASS (8/8).** Все критерии соблюдены точными числами на бинарнике
`a8bf04c8…` (HEAD: self-grant удалён, client_timeout_sec, граница тика).
Аренду выдал **планировщик** (LEASE_GRANT, held:true, origin:IMPLICIT).
Задержка выдачи = cooldown_sec + tick_ms (2500 мс). ts LEASE-событий
ровно равны ts аренды (доказательство Б: один `now` на тик). `QWEN_CODE_API_TIMEOUT_MS`
выставлен `runpilot run` из `profiles.qwen.client_timeout_sec`.
