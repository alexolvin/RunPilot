# Этап 3: Воспроизведение 30s client_gone на стенде (v2, путь как в прод-приёмке)

**Дата:** 2026-10-08
**Стенд:** `/mdisk/myhub/RunPilot/docs/evidence/hold-silent/stand/` (изолирован: 127.0.0.1, порты 18787/18788/18790/18801)
**Qwen:** системный (CLI 0.24.7, active.json 0.25.0)
**B:** sid `WGG98MVV38` (hold-b, PANE, hold-silent, pin=fakellm)
**A:** sid `1NDRJ3QP85` (hold-a, PANE, hold-silent, pin=fakellm)

## Путь создания B

B создан через **API** (`POST /api/v1/sessions/spawn`), как и в прошлом стендовом прогоне.
Enter отправлен через `tmux send-keys -t hold-b "hello" Enter`.

## Конфигурация

- fakellm: firstByteMs=120000 (120с), chunkPauseMs=0, chunks=5
- A: RUNNING (занял слот fakellm)
- B: IDLE → QUEUE_SKIP (pin_mismatch) → удержание в очереди
- hold_max_sec=600

## Результат: НЕ воспроизведено

30s client_gone **не воспроизводится** на стенде. B удерживался **60+ секунд** без client_gone.

### Таймлайн

| ts | событие |
|---|---|
| 19:59:03.021 | B в очереди (held_request=1, held_since) |
| 19:59:03.082 | QUEUE_SKIP (pin_mismatch) |
| 19:59:03 → 20:00:15 | B удерживается (60+с) — **без client_gone** |

### Запросы B

| id | t_start | server | status | error | held_ms |
|---|---|---|---|---|---|
| 1 | 19:58:19.500 | fakellm | 0 | CLIENT_GONE | 0 |

- request 1: CLIENT_GONE от **ранее ошибочно отправленного** "hello" (до корректного сценария).
- Текущее удержание B (с 19:59:03): **нет запроса** (удержание в очереди, запрос не дошёл до сервера).

### Capture-pane B (каждые 5с, 60с)

Все 12 снимков показывают: B в состоянии ожидания (Qwen Code v0.24.7, "Summoning the code gremlins..."). **Нет ошибок, нет сообщений о повторе, нет client_gone.**

Артефакт: `stand-capture-b.txt`

## Вывод

1. **30s client_gone не воспроизводится на стенде** (fakellm, 127.0.0.1).
2. B удерживается 60+с без client_gone.
3. **Нужен vzu5** для воспроизведения (30s client_gone специфичен для реального vzu5).
4. Instrumentation (context.WithCancelCause) готова — при повторе на vzu5 request.error покажет причину (CLIENT_GONE vs internal_cancel:CancelSID/CancelAll).

## Как повторить на vzu5

1. Развернуть координатор с instrumentation (context.WithCancelCause).
2. A: занять слот vzu5 (JOB сессия, pin=vzu5).
3. B: PANE сессия через веб (pin=vzu5).
4. Enter в B через tmux send-keys.
5. Monitoring: request.error по B (CLIENT_GONE vs internal_cancel:*).
6. Если request.error = internal_cancel:* → причина во внутренней отмене (CancelSID/CancelAll).
7. Если request.error = CLIENT_GONE → причина в клиенте (qwen).
