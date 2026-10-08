# Этап 4: Воспроизведение 30s client_gone на vzu5 (с instrumentation)

**Дата:** 2026-10-08
**Координатор:** 17adae8 (context.WithCancelCause instrumentation)
**B2:** sid `GZKHRKGE14` (diag-b2, PANE, 3p14-aaa, pin=vzu5)
**A:** sid `VHXN6DE58M` (diag-a, PANE, 3p14-aaa, pin=vzu5)

## Результат: ВОСПРОИЗВЕДЕНО

30s client_gone **воспроизведён** на vzu5.

### Таймлайн B2

| ts | событие | Δ |
|---|---|---|
| 20:22:50.179 | SESSION_STATE: "" → IDLE (создание) | — |
| 20:22:59.449 | QUEUE_SKIP (pin_mismatch) | +9.3с |
| 20:22:59.950 | QUEUE_SKIP (pin_mismatch) | +0.5с |
| **20:23:29.262** | **QUEUE_DEQUEUE (client_gone)** | **+29.3с** |
| 20:24:29.950 | QUEUE_SKIP (pin_mismatch) (ретрай) | +60.7с |

### Запрос B2

| id | t_start | server | status | error | held_ms |
|---|---|---|---|---|---|
| 213 | 20:23:29.262 | — | 0 | **CLIENT_GONE** | 0 |

- **request.error = CLIENT_GONE** (не internal_cancel:CancelSID/CancelAll).
- **Клиент (qwen) закрыл соединение**, не внутренняя отмена.

## Инструментация подтверждена

- `context.WithCancelCause` работает: request.error показывает CLIENT_GONE (клиентский обрыв).
- **Вывод:** 30s client_gone — это **клиентская сторона** (qwen закрывает соединение через 30с).

## QWEN_CODE_API_TIMEOUT_MS

- Процесс qwen B2: `QWEN_CODE_API_TIMEOUT_MS=5400000` (90 минут).
- **SDK таймаут НЕ является причиной** 30s client_gone.

## 30s таймер в бандле qwen

- **Не найден** (см. `qwen-30s-timer-search.md`).
- Все API-таймауты в бандле 0.25.0: 120s+.
- 30s константы: только несопряжённые с API.

## Вывод

1. **30s client_gone воспроизведён на vzu5** (B2: 29.3с, ретрай 60.7с).
2. **Причина: клиент (qwen) закрывает соединение через 30с** (CLIENT_GONE).
3. **QWEN_CODE_API_TIMEOUT_MS=5400000** (90 мин) — не причина.
4. **30s таймер в бандле qwen не найден** — источник требует runtime-отладки.

## Следующие шаги

1. Runtime-отладка qwen с --openai-logging.
2. Или фикс в RunPilot (если 30s таймер не найден в qwen).
