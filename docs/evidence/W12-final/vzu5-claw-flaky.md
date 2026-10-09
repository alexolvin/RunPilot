# W12 п.2 — vzu5-claw (flaky): диагностика

Дата: 2026-10-09.

## Что нестабильно

WS-соединение узла: постоянный UP/DOWN-флэп каждые ~11–15 с. Нестабильны
**соединение узла** (не health, не doctor — doctor PASS при каждом self-update).

## Причина: self-update loop

Цикл:
1. Узел подключается, `hello.RUNPILOTVersion` ≠ версии координатора.
2. Координатор шлёт `triggerNodeUpdate` (`internal/api/server.go:908`:
   `if s.version != "" && hello.RUNPILOTVersion != s.version &&
   hasFeature(hello.Features, "update")`).
3. Узел скачивает бинарник из `coordinator.dist_dir`, `os.Rename` swap,
   `exit 75` (TEMPFAIL) — `internal/node/ops.go:445`, `cmd/runpilot/node.go:74`.
4. systemd `Restart=on-failure` перезапускает узел (counter 5573).
5. Версия после self-update = версия **dist-бинарника**. Если она ≠ версии
   координатора → п.1 повторяется.

До фикса: узлы `d96bf26`, координатор `77e9550`, `dist/runpilot` = `d96bf26`
(stale, `deploy-coordinator.sh` не обновляет dist). Self-update ставил
`d96bf26` (no-op по версии) → mismatch не снимался → **вечный цикл**.

## Переподключения за 24 ч (события NODE_STATE)

| узел | UP (reconnect) | DOWN | период |
|------|---------------:|-----:|--------|
| vzu5-big | 7838 | 7847 | 2026-10-08T00:15 → 2026-10-09T13:50 |
| vzu5-claw | 5584 | 5590 | 2026-10-08T00:15 → 2026-10-09T13:46 |
| 3p14-aaa (embedded) | 0 | 26 | (рестарты координатора) |

Образец паттерна (vzu5-claw, шаг ~2–7 с):
```
13:44:40 UP   13:44:37 DOWN   13:44:32 UP   13:44:29 DOWN
13:44:23 UP   13:44:21 DOWN   13:44:16 UP   13:44:14 DOWN ...
```

Лог узла (vzu5-claw):
```
13:44:31 node: старт
13:44:37 node: self-update завершён, выход кодом 75
13:44:37 runpilot-node.service: Main process exited, status=75/TEMPFAIL
13:44:40 runpilot-node.service: Scheduled restart job, restart counter 5573
```

## Фикс и подтверждение

- Оба узла обновлены до `77e9550` (`upgrade-node.sh`, tailscale ssh).
- `dist/runpilot` обновлён до `77e9550` (SHA `383485c7…` = координатор).
- Версии совпали → координатор не шлёт update → цикл остановился.

Подтверждение: переходов NODE_STATE за последние 3 мин **нет** (последний
vzu5-big 13:50:12 — от тестового self-update, vzu5-claw 13:46:13 — от
upgrade-node.sh). Все узлы `77e9550`, `runpilot-node` active.

## Рекомендации (продукт)

1. `deploy/deploy-coordinator.sh` не обновляет `coordinator.dist_dir` — после
   выката координатора dist остаётся со старой версией → self-update узлов
   разворачивает устаревший бинарник и (при mismatch) приводит к loop.
   Фикс процедуры: копировать актуальный бинарник в `dist_dir/runpilot`.
2. `internal/node/ops.go update()`: узел меняет бинарник и `exit 75` **без
   сравнения текущей и новой версии** — self-update на ту же версию
   всё равно рестартит узел (no-op с перерывом). Добавил бы проверку
   «версия не изменилась → не рестартироваться».
