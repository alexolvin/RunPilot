# Закрытие хода — метки, override стенда, нижняя граница (виртуальные часы)

Дата: 2026-10-06. Бинарник runpilot `a8bf04c8…`.

## 1. Override стенда vs `defaults.go` (turn + scheduler + gateway)

`checkTurn` (`internal/scheduler/complete.go`) закрывает ход, только когда
**все 4 предиката** истинны. Стенд переопределяет два параметра, ускоряя
закрытие до 1 с (default 5 с) — это и есть «stand override»:

| Параметр | `defaults.go` | Стенд (`config.yaml`) | Прим. |
|---|---|---|---|
| `turn.done_quiet_sec` | **5** | **1** | **OVERRIDE** |
| `turn.done_stable_sec` | **5** | **1** | **OVERRIDE** |
| `scheduler.tick_ms` | 500 | 500 | = default |
| `scheduler.cooldown_sec` | 2 | 2 | = default |
| `gateway.hold_max_sec` | **3600** (В.8) | 600 | override (для п.5) |
| `servers[0].first_byte_timeout_sec` | — (Servers=nil) | 900 | поле сервера |

Все остальные `turn.*` (`ToolHoldMaxSec=900`, `ApprovalHoldMaxSec=600`,
`UnknownMaxSec=60`, `NodeLostReleaseSec=120`, …) — **default** (не заданы).

## 2. Четыре метки (предикаты `checkTurn`, complete.go:92–107)

Ход закрывается (`closeTurn`), когда одновременно:

| # | Метка | Источник | Предикат |
|---|---|---|---|
| 1 | **last_request_end** | `RequestTick(sid, false)` → `lastReqEnd[sid]=now` | p3: `now − lastEnd ≥ done_quiet_sec` (при `reqCount>0`) |
| 2 | **panel→IDLE transition** | снапшот панели: BUSY→IDLE (prompt готов) | p1: панель в состоянии IDLE |
| 3 | **stable_since** | смена hash содержимого панели → `hashSince=now` | p4: `now − hashSince ≥ done_stable_sec` |
| 4 | **LEASE_RELEASE** | `closeTurn` → `releaseLeaseLocked(reason=TURN_DONE, now)` | событие LEASE_RELEASE с ts=`now` тика (eventAt) |

(Дополнительно p2: `inflight == 0` — нет идущих запросов.)

Все метки в одном тике получают **один `now`** (граница тика, пункт Б):
`closeTurn`, `LEASE_RELEASE` и `GrantedAt/ReleasedAt` используют `now` тика,
не свежий `clk.Now()`.

## 3. Нижняя граница закрытия хода (виртуальные часы)

Ход **НЕ закрывается раньше** (a) `last_request_end + done_quiet_sec`
(при `reqCount>0`) и (b) `stable_since + done_stable_sec`. Каждая граница
проверяется изолированно (одна выполнена, вторая — на грани).

Тесты: `internal/scheduler/turn_close_lowerbound_test.go`.
Конфиг harness: `done_quiet_sec = done_stable_sec = 5` (default).

| Тест | Граница | Сценарий | Результат |
|---|---|---|---|
| `TestTurnCloseQuietLowerBound` | (a) quiet доминирует: `lastEnd=3с`, `hashSince=0с` | на 7с (`lastEnd+4с<5с`) аренда **на месте**; на 8с (`lastEnd+5с`) снята | ✅ PASS (0.19с) |
| `TestTurnCloseStableLowerBound` | (b) stable доминирует: `lastEnd=0с`, `hashSince=3с` | на 6с (`stableSince+3с<5с`) аренда **на месте**; на 8с (`stableSince+5с`) снята | ✅ PASS (0.18с) |

Команда и вывод:
```
$ go test -race -v -run 'TestTurnCloseQuietLowerBound|TestTurnCloseStableLowerBound' ./internal/scheduler/
=== RUN   TestTurnCloseQuietLowerBound
--- PASS: TestTurnCloseQuietLowerBound (0.19s)
=== RUN   TestTurnCloseStableLowerBound
--- PASS: TestTurnCloseStableLowerBound (0.18s)
PASS
ok      runpilot/internal/scheduler     1.388s
```

## Вывод

Закрытие хода — корректное: все 4 предиката, метки из единого `now` тика,
нижняя граница (не раньше `last_request_end+done_quiet_sec` и
`stable_since+done_stable_sec`) доказана виртуальными часами. Стенд
переопределяет `done_quiet_sec`/`done_stable_sec` с 5→1 для быстрых live-замеров.
