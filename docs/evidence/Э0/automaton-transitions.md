# Переходы автомата сессии — маппинг в раздел 4 ТЗ

Код: `internal/model/session_automaton.go`, таблица `sessionTransitions` (31 строка)
плюс начальный переход. Тесты: `internal/model/automaton_test.go`.

## Число переходов: 32 (31 строка таблицы + 1 начальный)

| № | Переход | Источник в разделе 4 |
|---|---|---|
| 0 | ∅ → IDLE | `[*] --> IDLE: aqm run` |
| 1 | IDLE → QUEUED | `IDLE --> QUEUED: enqueue` |
| 2 | QUEUED → IDLE | `QUEUED --> IDLE: dequeue` |
| 3 | QUEUED → DISPATCHING | `QUEUED --> DISPATCHING: аренда выдана` |
| 4 | DISPATCHING → RUNNING | `DISPATCHING --> RUNNING: старт подтверждён` |
| 5 | DISPATCHING → QUEUED | `DISPATCHING --> QUEUED: PANE_CHANGED` |
| 6 | DISPATCHING → HOLD | `DISPATCHING --> HOLD: EMPTY_INPUT` |
| 7 | DISPATCHING → HOLD | `DISPATCHING --> HOLD: …, START_NOT_CONFIRMED` |
| 8 | IDLE → RUNNING | `IDLE --> RUNNING: ручной Enter, слот есть` |
| 9 | IDLE → QUEUED | `IDLE --> QUEUED: ручной Enter, слота нет` |
| 10 | RUNNING → IDLE | `RUNNING --> IDLE: ход завершён OK` |
| 11 | RUNNING → DETACHED | `RUNNING --> DETACHED: аренда снята в ходе` |
| 12 | DETACHED → RUNNING | `DETACHED --> RUNNING: новый запрос хода` |
| 13 | DETACHED → IDLE | `DETACHED --> IDLE: панель IDLE без ошибки` |
| 14 | RUNNING → QUEUED | `RUNNING --> QUEUED: ошибка, attempts ≤ max` |
| 15 | RUNNING → HOLD | `RUNNING --> HOLD: ошибка, attempts > max` |
| 16 | HOLD → QUEUED | `HOLD --> QUEUED: requeue` |
| 17 | HOLD → IDLE | `HOLD --> IDLE: unhold` |
| 18 | QUEUED → HOLD | `QUEUED --> HOLD: PIN_UNAVAILABLE` |
| 19 | QUEUED → HOLD | `QUEUED --> HOLD: …, DEPENDENCY_FAILED` |
| 20 | IDLE → HOLD | `IDLE --> HOLD: NODE_DRAIN` |
| 21 | QUEUED → HOLD | `QUEUED --> HOLD: NODE_DRAIN` |
| 22 | IDLE → GONE | «Из любого состояния … GONE, когда панель исчезает» |
| 23 | QUEUED → GONE | то же |
| 24 | DISPATCHING → GONE | то же |
| 25 | RUNNING → GONE | то же |
| 26 | DETACHED → GONE | то же |
| 27 | HOLD → GONE | то же |
| 28 | DETACHED → QUEUED | «DETACHED при ошибке ведёт себя как RUNNING» (строка 14) |
| 29 | DETACHED → HOLD | «DETACHED при ошибке ведёт себя как RUNNING» (строка 15) |
| 30 | IDLE → HOLD | код `OPERATOR` из таблицы hold_reason («Ручной hold») |
| 31 | QUEUED → HOLD | код `OPERATOR` из таблицы hold_reason («Ручной hold») |

## Проверка 100% покрытия

`TestExhaustiveClassification` перебирает все пары (состояние × событие):
7 состояний × 22 события = 154 пары; каждая пара либо даёт переход из таблицы,
либо `ErrInvalidTransition`. `TestTransitionCount` фиксирует число строк (31 + 1).
Диаграмма раздела 4 содержит 20 стрелок (19 из состояний + начальная); две из них
двухпричинные (строки 6–7 и 18–19) — расписаны по строкам таблицы.
