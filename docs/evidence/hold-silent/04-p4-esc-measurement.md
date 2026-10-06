# П.4 — Esc во время удержания (FINDING: реализация расхожа с ТЗ)

Stand: A=env (держит слот 360 c), B=noenv, B удержан (held_request=1, enqueued_at=02:29:20.852,
A RUNNING, слот занят). Esc отправлен в qwen B (отмена запроса).

## Данные (БД, UTC)
```
T_esc (до)  = 2026-10-06T02:29:40.397755
T_after     = 2026-10-06T02:29:40.401496
QUEUE_DEQUEUE B до Esc : (пусто)
QUEUE_DEQUEUE B после  : (пусто) — события нет
queue_entry B после    : sid=7NGNX2EBB1 held_request=0 mode=resume enqueued_at=02:29:20.852 (НЕ удалена)
state B после          : IDLE
B pane                 : «Auto mode» (idle, запрос отменён)
```

## Критерии
| # | Критерий (ТЗ) | Измерено | |
|---|---|---|---|
| 1 | Запись очереди **удалена** | **НЕТ**: запись осталась, `held_request 1→0`, `mode submit→resume` | ❌ |
| 2 | Сессия **IDLE** | IDLE | ✅ |
| 3 | Событие **QUEUE_DEQUEUE {reason:"client_gone"}** ≤1 с после Esc | **НЕТ события вообще** (и payload Dequeue в коде — пустой, без `reason`) | ❌ |

## Вывод (FINDING)
Текущая реализация: `holdPass` при `ctx.Done()` (Esc) вызывает `heldToResume(sid)` →
`QueueSetHeldRequest(sid, false, QueueModeResume)` — запись **остаётся** в очереди с
`held_request=false, mode=resume`, **без удаления** и **без события QUEUE_DEQUEUE**.
Механизм удаления + `QUEUE_DEQUEUE` существует только в `Scheduler.Dequeue` (ручная
операция; payload пустой, без `reason:"client_gone"`), и шлюзом НЕ вызывается.

Расхождение с ТЗ: ТЗ требует при Esc — удаление записи, IDLE и `QUEUE_DEQUEUE
{reason:"client_gone"}` ≤1 с. Реально — heldToResume (запись остаётся, mode=resume),
события нет. → В «реализацию по списку» (после п.10) включить: при отмене клиента во
время удержания — `QueueDelete` + `QUEUE_DEQUEUE {reason:"client_gone"}` + сессия IDLE.

Примечание: `enqueued_at` при heldToResume НЕ меняется (02:29:20.852 сохранён) — сброс
enqueued_at происходит при ПОВТОРЕ (новый holdPass, см. п.3/п.5).
