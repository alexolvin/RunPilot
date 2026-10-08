# Этап 2: Разбор прод-прогона (приёмка 2026-10-08 18:05)

**Дата разбора:** 2026-10-08
**B:** sid `93FF114HJJ` (hold-b2, PANE, 3p14-aaa, pin=vzu5, created 18:05:22.280)
**A:** sid `CE4GMXVG12` (job-CE4GMXVG12, JOB, 3p14-aaa, pin=vzu5, created 18:04:54.487)

## Таймлайн событий по B

| ts | kind | payload |
|---|---|---|
| 18:05:22.280 | SESSION_STATE | "" → IDLE (создание) |
| 18:05:53.884 | QUEUE_SKIP | reason=pin_mismatch |
| **18:06:23.412** | **QUEUE_DEQUEUE** | **reason=client_gone** |
| 18:07:23.884 | QUEUE_SKIP | reason=pin_mismatch (ретрай) |
| 18:10:13.884 | LEASE_RELEASE | vzu5, reason=TURN_DONE |
| 18:10:13.891 | TURN_END | outcome=OK, requests=3, turn_id=11 |
| 18:10:13.891 | SESSION_STATE | RUNNING → IDLE |
| 18:14:23.112 | SESSION_STATE | IDLE → GONE |

**Ключевое:** client_gone в 18:06:23.412 = **29.5с после QUEUE_SKIP** (18:05:53.884).

## Запросы по B

| id | t_start | server | status | error | held_ms |
|---|---|---|---|---|---|
| 184 | 18:06:23.412 | — | 0 | **CLIENT_GONE** | 0 |
| 196 | 18:08:53.636 | vzu5 | 200 | — | 0 |
| 197 | 18:09:09.168 | vzu5 | 200 | — | 0 |
| 198 | 18:09:42.154 | vzu5 | 200 | — | 0 |
| 199 | 18:09:42.236 | vzu5 | 200 | — | 0 |

- request 184: client_gone, **без server и path** (запрос не дошёл до сервера — удержание в очереди).
- request 196-199: успешные (после ретраи, pin_mismatch разрешён).

## Turn и lease

- **turn 11:** 18:08:53.883 → 18:10:13.883, vzu5, OK, requests=3.
- **lease 179:** vzu5, slot=1, IMPLICIT, 18:08:53.636 → 18:10:13.883, TURN_DONE.

## pin_mismatch — причина

Код: `internal/scheduler/eligibility.go:185-201`
```go
// pin: сервер DOWN/DRAINING — pin_down; UP, но слотов нет — pin_mismatch.
if e.Constraint.Kind == model.ConstraintPin {
    pv, ok := s.viewOf(e.Constraint.Server)
    if ok && pv.State != model.ServerUp {
        return model.ReasonPinDown, ...  // pin_down
    }
    if !ok || !contains(free, e.Constraint.Server) {
        return model.ReasonPinMismatch, ...  // pin_mismatch
    }
}
```

**pin_mismatch = vzu5 UP, но слотов нет (занят A).** Не pin_down (vzu5 не был DOWN в момент 18:05:53).

## vzu5 — конфиг сервера

```json
{"Name":"vzu5","Priority":5,"Slots":1,"Accept":["resume","high","normal","low"],
 "FirstByteTimeoutSec":0,"RequireDirect":true,
 "Upstreams":{"OpenAI":{"URL":"http://100.126.222.23:8004","Model":"qwen3.8-27b-fp8"}}}
```

- Accept: все классы (resume, high, normal, low) — accept не причина.
- FirstByteTimeoutSec: 0 (не задан).
- Slots: 1 (один слот, занят A).

## vzu5-big — флаппинг DOWN/UP

**vzu5-big узел флаппит DOWN/UP каждые ~5-7 секунд** на всём интервале 18:00-18:07:

```
18:05:16.236 DOWN → 18:05:18.564 UP (2.3с)
18:05:24.737 DOWN → 18:05:27.059 UP (2.3с)
18:05:32.043 DOWN → 18:05:34.308 UP (2.3с)
18:05:39.629 DOWN → 18:05:41.805 UP (2.2с)
18:05:46.242 DOWN → 18:05:48.563 UP (2.3с)
18:05:53.884 QUEUE_SKIP (pin_mismatch)
18:05:53.911 DOWN → 18:05:56.054 UP (2.1с)
18:06:01.206 DOWN → 18:06:03.557 UP (2.4с)
18:06:08.328 DOWN → 18:06:10.553 UP (2.2с)
18:06:15.860 DOWN → 18:06:18.056 UP (2.2с)
18:06:22.879 DOWN
18:06:23.412 QUEUE_DEQUEUE (client_gone)
18:06:25.056 UP
...
```

**Вывод:** vzu5-big нестабилен (флаппинг). Но pin_mismatch в 18:05:53 — не из-за DOWN (vzu5 был UP), а из-за занятости слота A.

## Конфиг: 30s значения

Прод-конфиг не задаёт `gateway.retry_after_sec` и `scheduler.resume_backoff_sec` → defaults:
- `scheduler.resume_backoff_sec: 30` (defaults.go:36)
- `gateway.retry_after_sec: 30` (defaults.go:63)

**Вопрос:** может ли `resume_backoff_sec=30` отменить удержанный запрос? Требует проверки кода.

## Рестарты служб

- Текущий координатор: pid 2496473, запущен **21:03** (после приёмки 18:05).
- Логи приёмки (18:05) **недоступны** (координатор перезапущен).
- Рестарты в интервале 18:05:30–18:07:00: **не подтверждены** (нет логов).

## Что произошло ровно в 18:06:23

- 18:06:22.879 — vzu5-big DOWN.
- 18:06:23.412 — **client_gone** (B): request контекст завершён, запись очереди удалена, request.error=CLIENT_GONE.
- 18:06:25.056 — vzu5-big UP.

**Причина client_gone:** контекст запроса B завершён через 29.5с после удержания. Кто завершил (клиент qwen или внутренняя отмена) — **не определено** (нет логов, нет instrumentation).

## Вывод

1. **pin_mismatch** — vzu5 UP, слот занят A. Ожидаемое поведение.
2. **vzu5-big флаппит** DOWN/UP каждые ~5-7с — отдельная проблема (нестабильный узел).
3. **client_gone через 29.5с** — причина не определена. Требуется:
   - Instrumentation (context.WithCancelCause) для distinction клиент/внутренняя отмена.
   - Проверка `resume_backoff_sec=30` — может ли отменить удержанный запрос.
   - Воспроизведение на стенде.
