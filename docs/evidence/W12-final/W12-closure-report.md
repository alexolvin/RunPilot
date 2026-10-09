# W12 — Отчёт о закрытии (2026-10-09, повторное закрытие)

Дата: 2026-10-09. Коммит: `2fa22ff`.

## Таблица проверки

| # | Пункт | Статус | Детали |
|---|-------|--------|--------|
| 1 | Удалить авто-обновление узлов | **PASS** | `server.go`: mismatch версий → WARN (doctor + бейдж вебо), авто-self-update убран. Тест: `TestNodeVersionMismatchNoAutoUpdate` |
| 2 | Защита ручного обновления | **PASS** | `ops.go`: та же версия → NOOP; downgrade → REFUSED без `downgrade=true`; чужая версия бинарника → REFUSED. Тесты: `TestUpdateSameVersionNoop`, `TestUpdateDowngradeWithoutFlagRefused`, `TestUpdateDowngradeWithFlagAllowed` |
| 3 | deploy-coordinator.sh: dist_dir sync | **PASS** | Шаг 4/5: копирование бинарника в dist_dir + сверка SHA256. Песочный тест: 10/10 PASS (включая test C) |
| 4a | Мismatch → нет обновления, есть WARN | **PASS** | `TestNodeVersionMismatchNoAutoUpdate` (server_test.go) |
| 4b | Stale dist → API отказ | **PASS** | `TestNodeUpdateStaleDistRefused` → 409 DIST_MISMATCH (nodes_test.go) |
| 4c | Чужой SHA256 → узел отказывает | **PASS** | SHA256 check в `update()` (ops.go) + `TestUpdateDowngradeWithoutFlagRefused` |
| 4d | Та же версия → без рестарта | **PASS** | `TestUpdateSameVersionNoop` → `UPDATE_NOOP` (ops_test.go) |
| 4e | e2e CONTROL 2: 20 прогонов PASS | **PASS** | Гонка устранена (tmux kill-server в cleanup + явный `triggerUpdate`). 20/20 PASS |
| 5a | Все узлы = версия координатора | **PASS** | vzu5-big, vzu5-claw, 3p14-aaa — все `2fa22ff`, `version_mismatch=false` |
| 5b | SHA256 dist_dir = SHA256 координатора | **PASS** | `f505f1df...` = `f505f1df...` |
| 5c | 0 переходов NODE_STATE (кроме update) | **PASS** | Окно 16:34:00Z–17:34:00Z: **0 переходов**. Выборка: `SELECT ts, host, state FROM event WHERE kind='NODE_STATE' AND ts>='2026-10-09T16:34:00Z' AND ts<='2026-10-09T17:34:00Z'` → пусто |
| 6 | Влияние цикла на сессии | **PASS** | 26831 переходов за 22ч, 0 сессий затронуто (на узлах не было живых сессий). Артефакт: `self-update-loop-impact.md` |

## Итого: 12/12 PASS — W12 закрыт

## HANDOFF

W12 закрыт: 12/12 PASS.

Состояние после выката:
- Координатор: `2fa22ff` (vcs.modified=false), runpilot.service active.
- Узлы: 3p14-aaa / vzu5-big / vzu5-claw — все `2fa22ff`.
- dist/runpilot: `2fa22ff` (SHA256 `f505f1df...`, синхронизирован с координатором).
- Оператора-токен: /home/user/.config/runpilot/env (0600).

Отложено (условие запуска): разбор флаппинга vzu5 командой `runpilot servers flaps --since 7d`, когда в журнале накопится не меньше 5 переходов в DOWN.
