# Двойная аренда — восстановление последовательности нарушения (п.1)

Дата: 2026-10-06. Прогон e2e Э4 на **before-бинарнике** — сборка
**до фикса границы тика** (AfterFunc→Tick выключен) **и без инварианта п.1**
(инвариант слотов в `LeaseCreateIfAbsent` отключён). Всё остальное (W9/W10,
пре-п.2 `checkTurn`) — как в рабочем дереве.

**Before-бинарник SHA256 = `9c4035b009b95792…`** (сборка из рабочего дерева с
отключёнными: `checkServerSlots` — no-op, `setCooldown` — без AfterFunc).
Стенд: 2×fakellm `a`(1 слот)/`b`(1 слот), 6×mockcoder ×3 хода, `tick_ms=100`,
`cooldown_sec=1`, `done_quiet/stable_sec=3`, `scan_interval_sec=1`.

## Итог прогона (double-lease воспроизведён)

- `аренд: сэмпл 769, max=2, нарушений=1` — **1-слотовый сервер `a` на одном
  сэмпле держит 2 аренды** (критерий «≤1 на сервер» нарушен).
- `ходов OK: 22/18` — 4 фантомных хода (0 запросов, `started_at==ended_at`).
- 54/54 запроса доставлены.

## Нарушение: сервер `a` (1 слот), 2 одновременные аренды

Две сессии, получившие аренды **на один и тот же `a` slot 1** в один момент:

| sid | имя | lease id | origin | granted_at (UTC) | released_at | reason |
|---|---|---|---|---|---|---|
| `40MTFR2TNX` | s2 | **11** | IMPLICIT | **16:07:34.180571** | 16:07:34.254 | TURN_DONE |
| `HMCCVG26WV` | s3 | **12** | IMPLICIT | **16:07:34.180494** | 16:07:34.254 | TURN_DONE |

Обе аренды активны одновременно (окно ~74 мс) → **2 аренды на 1 слот**.
Сэмплер harness (`GROUP BY server, COUNT(*)`) зафиксировал `max=2` при `t=52.7с`.

## Строки аренд обеих сессий (весь прогон)

```
40MTFR2TNX id  3 srv=a slot=1 DISPATCH  16:07:03.655 → 16:07:08.655 (TURN_DONE) req=3   [раунд 1, норма]
40MTFR2TNX id  9 srv=a slot=1 DISPATCH  16:07:33.655 → 16:07:33.655 (TURN_DONE) req=0   [раунд 2, ФАНТОМ]
40MTFR2TNX id 11 srv=a slot=1 IMPLICIT  16:07:34.180 → 16:07:34.254 (TURN_DONE) req=1   [DOUBLE-LEASE]
40MTFR2TNX id 13 srv=a slot=1 IMPLICIT  16:07:34.682 → 16:07:34.754 (TURN_DONE) req=1
40MTFR2TNX id 21 srv=a slot=1 DISPATCH  16:08:03.655 → 16:08:08.654 (TURN_DONE) req=3   [раунд 3, норма]
HMCCVG26WV id  4 srv=b slot=1 DISPATCH  16:07:03.655 → 16:07:08.655 (TURN_DONE) req=3   [раунд 1, норма]
HMCCVG26WV id 10 srv=b slot=1 DISPATCH  16:07:33.655 → 16:07:33.655 (TURN_DONE) req=0   [раунд 2, ФАНТОМ]
HMCCVG26WV id 12 srv=a slot=1 IMPLICIT  16:07:34.180 → 16:07:34.254 (TURN_DONE) req=1   [DOUBLE-LEASE]
HMCCVG26WV id 14 srv=b slot=1 IMPLICIT  16:07:34.682 → 16:07:34.754 (TURN_DONE) req=1
HMCCVG26WV id 22 srv=b slot=1 DISPATCH  16:08:03.655 → 16:08:08.654 (TURN_DONE) req=3   [раунд 3, норма]
```

## Timeline событий s2+s3 (окно 33.5–34.8 с)

```
16:07:33.655 LEASE_GRANT     s2 srv=a  {"origin":"DISPATCH","slot":1}
16:07:33.655 LEASE_GRANT     s3 srv=b  {"origin":"DISPATCH","slot":1}
16:07:33.655 LEASE_RELEASE   s2 srv=a  {"reason":"TURN_DONE","slot":1}      <- granted==released
16:07:33.655 LEASE_RELEASE   s3 srv=b  {"reason":"TURN_DONE","slot":1}      <- granted==released
16:07:33.655 SESSION_STATE   s2 QUEUED→DISPATCHING
16:07:33.656 DISPATCH        s2 srv=a  {"mode":"submit","pane_id":"%1"}
16:07:33.656 SESSION_STATE   s3 QUEUED→DISPATCHING
16:07:33.656 DISPATCH        s3 srv=b  {"mode":"submit","pane_id":"%2"}
16:07:33.677 TURN_END        s2 {"outcome":"OK","requests":0,"turn_id":9}    <- ФАНТОМ (0 запросов)
16:07:33.677 SESSION_STATE   s2 RUNNING→IDLE
16:07:33.678 TURN_END        s3 {"outcome":"OK","requests":0,"turn_id":10}   <- ФАНТОМ
16:07:33.678 SESSION_STATE   s3 RUNNING→IDLE
        ... (имплицитная пере-выдача: аренда id11 s2 + id12 s3 → обе на a slot 1, granted 34.180)
16:07:34.254 LEASE_RELEASE   s2 srv=a  {"reason":"TURN_DONE","slot":1}      <- двойная аренда снята
16:07:34.254 LEASE_RELEASE   s3 srv=a  {"reason":"TURN_DONE","slot":1}      <- (обе на a slot 1)
16:07:34.256 TURN_END        s2 {"outcome":"OK","requests":0,"turn_id":11}
16:07:34.256 SESSION_STATE   s2 RUNNING→IDLE
16:07:34.257 TURN_END        s3 {"outcome":"OK","requests":0,"turn_id":12}
16:07:34.257 SESSION_STATE   s3 RUNNING→IDLE
```

## Механизм нарушения (цепочка)

1. **33.655 — диспатч раунда 2.** s2 и s3 выданы (`a`/`b`), ход открыт. Но
   панель ещё не BUSY (снимок узла `scan_interval_sec=1`), запросов 0, а после
   долгого ожидания в раунде 1 панель IDLE + хеш стабилен.
2. **33.655 — фантомное закрытие (defect 2, п.2).** `checkTurn`:
   `reqCount==0 → doneQuiet не применяется` + IDLE + `hashSince ≥ done_stable`
   → `closeTurn` **в тот же момент** (granted==released, `turn_id=9/10`,
   `requests=0`). Слоты `a` и `b` **преждевременно** освобождены, хотя кодер
   ещё не запускал ход.
3. **34.180 — имплицитная пере-выдача (defect 1, п.1).** Аренда id11 (s2) и
   id12 (s3) **обе на `a` slot 1** (origin IMPLICIT). Проверка «слот свободен»
   в `LeaseCreateIfAbsent` — **не атомарна** с вставкой и **не считает**
   действующих аренд сервера: обе вставки прошли, т.к. в момент проверки
   предыдущая аренда уже была RELEASED. → **2 аренды на 1 слот**.
4. **34.254 — обе сняты TURN_DONE** (ещё один короткий «пустой» цикл).

**Ключ:** дефект 1 (двойная аренда) — следствие дефекта 2 (фантомное закрытие
преждевременно освобождает слот) + **отсутствия атомарного инварианта слотов
в store**. Фикс п.1 закрывает дефект 1 независимо: даже при преждевременном
освобождении вторая аренда будет отклонена.

## Как п.1 (инвариант в store) исключает нарушение

`LeaseCreateIfAbsent` теперь в одной транзакции проверяет:
`COUNT(аренды сервера в PENDING/ACTIVE/RECOVERING) < slots`, иначе `ErrSlotFull`.

На момент 34.180: создание id11 (s2) → count(`a`) 0→1. Создание id12 (s3) →
count(`a`)=1 ≥ slots=1 → **ErrSlotFull → отказ**. Двойная аренда невозможна:
сэмплы всегда ≤ slots. Тест: `TestLeaseCreateIfAbsentSlotFullRejects`.

Raw-дамп: `violation-2026-10-06.txt` (те же строки аренд + timeline).
