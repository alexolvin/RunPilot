# Последствия прямой правки БД (W_EZPODRYS, 7FZMCD5GTK)

**Дата:** 2026-10-09

## Как были созданы сессии

Сессии созданы прямым INSERT в БД (не через API/CLI):
- `W_EZPODRYS` (acc-a): INSERT INTO session, затем INSERT INTO lease (ACTIVE), UPDATE session SET state='RUNNING'
- `7FZMCD5GTK` (acc-b): INSERT INTO session

Сid W_EZPODRYS не соответствует формату Crockford base32 (содержит "_").

## Аренды

| sid | server | slot | state | origin | granted_at |
|-----|--------|------|-------|--------|------------|
| 7FZMCD5GTK | vzu5 | 1 | **ACTIVE** | IMPLICIT | 2026-10-09T05:56:39Z |
| W_EZPODRYS | vzu5 | 1 | RELEASED | implicit | 2026-10-09T05:53:02Z |

**Несогласованность:** 7FZMCD5GTK имеет ACTIVE аренду, но сессия GONE (закрыта прямым UPDATE).

## Очередь

Нет записей в queue_entry для обоих sid.

## События

| ts | kind | sid | payload |
|----|------|-----|---------|
| 2026-10-09T05:55:43Z | QUEUE_SKIP | 7FZMCD5GTK | {"reason":"pin_mismatch"} |
| 2026-10-09T05:56:18Z | QUEUE_DEQUEUE | 7FZMCD5GTK | {"reason":"client_gone"} |

Нет событий LEASE_RELEASE для 7FZMCD5GTK.

## Как изменено hold_max_sec

Через SQL: `UPDATE setting SET value='3600' WHERE path='gateway.hold_max_sec'`
(не через API).

## Таблица настроек БД ≠ defaults.go

| ключ | значение в БД | значение по умолчанию | источник |
|------|---------------|----------------------|----------|
| gateway.hold_max_sec | 3600 | 3600 | исправлено SQL (было 60, миграция v1) |
| node.project_roots_default | ["~","/mdisk/myhub"] | ["~"] | миграция v1 (легитимная настройка оператора) |
| profiles.qwen.model_alias | "qwen3.8-27b-fp8" | "qwen3.6-27b" | миграция v1 (легитимная настройка оператора) |

## Как исправить аренду 7FZMCD5GTK

API не позволяет снять аренду GONE-сессии (CmdClose возвращает "уже закрыта").
Требуется SQL: `UPDATE lease SET state='RELEASED' WHERE sid='7FZMCD5GTK' AND state='ACTIVE'`
