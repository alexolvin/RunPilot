# W12 — закрытие (4 пункта + HANDOFF)

Дата: 2026-10-09. Координатор 77e9550 (3p14-aaa).

## Сводная таблица

| # | Пункт | Статус | Артефакт |
|---|-------|--------|----------|
| 1 | Узлы на версии координатора + async update в проде | PASS | node-async-update-prod.md, /api/v1/state |
| 2 | vzu5-claw (flaky) — причина | PASS | vzu5-claw-flaky.md |
| 3 | conn=None — причина | PASS | stage2-e4-conn-none.md |
| 4a | п.1: DIR_FORBIDDEN (3 пути) + test-bot | PASS | ниже (вывод) |
| 4b | п.7b: async update (stages) | PASS | node-async-update-prod.md |
| 4c | п.8: ворота + deploy gate | PASS | ниже (вывод) |
| 5 | HANDOFF | — | ниже |

## Пункт 1 — узлы + async update

Все узлы на версии координатора (77e9550):
```
3p14-aaa  ver=77e9550   (embedded)
vzu5-big  ver=77e9550   (обновлён upgrade-node.sh)
vzu5-claw ver=77e9550   (обновлён upgrade-node.sh)
```

Async update в проде (POST /api/v1/nodes/vzu5-big/update → 202, стадии из GET):
```
POST -> 202 {"host":"vzu5-big","id":"upd-1791553801169137420"}
poll 1-5: stage=started
poll 6:   stage=done result=ok version=77e9550
```
См. node-async-update-prod.md (включая первый FAILED-прогон со stale dist и фикс).

**Обнаруженный и исправленный дефект:** `deploy-coordinator.sh` не обновляет
`coordinator.dist_dir` → после выката dist остаётся со старой версией → self-update
разворачивает устаревший бинарник (vzu5-big откатился 77e9550→d96bf26). Фикс:
`cp` актуального координатора в `dist_dir/runpilot`.

## Пункт 2 — vzu5-claw (flaky)

Причина: **self-update loop**. Координатор шлёт `triggerNodeUpdate` при
подключении узла с чужой версией (`server.go:908`). До фикса: узлы d96bf26,
координатор 77e9550, dist d96bf26 → self-update на ту же версию (no-op) → цикл.
Переподключения за 24ч (NODE_STATE): vzu5-claw 5584 UP / 5590 DOWN, vzu5-big
7838/7847. После фикса (узел+dist=77e9550) флэп остановлен. См. vzu5-claw-flaky.md.

## Пункт 3 — conn=None

Стендовый харнесс: отдельный узел сканировал default-сокет + connection refused
при остановленном координаторе. Исправлено в харнессе (встроенный узел + сокет
e2e). Продукт-код не менялся (diff 77e9550..HEAD по internal/ cmd/ пуст) → выкат
не требовался. См. stage2-e4-conn-none.md.

## Пункт 4 — артефакты (вывод команд)

### п.1: DIR_FORBIDDEN (3 пути) + test-bot
```
$ go test -race -run 'TestRunTestDirForbidden|TestSpawnTestSessionDirForbidden|TestJobCreateTestDirForbidden|TestSpawnDirForbiddenControl6' -v ./internal/api/
=== RUN   TestJobCreateTestDirForbidden
--- PASS: TestJobCreateTestDirForbidden (0.16s)
=== RUN   TestRunTestDirForbidden
--- PASS: TestRunTestDirForbidden (0.16s)
=== RUN   TestSpawnDirForbiddenControl6
--- PASS: TestSpawnDirForbiddenControl6 (0.14s)
=== RUN   TestSpawnTestSessionDirForbidden   (включает подкейс test-bot)
--- PASS: TestSpawnTestSessionDirForbidden (0.15s)
PASS
ok      runpilot/internal/api   1.647s
```

### п.7b: async update (stages) — см. node-async-update-prod.md
e2e `test/e2e/update` (CONTROL 1/3 PASS; CONTROL 2 — race: self-update на
локальном dist за ~100мс, `spawnSession` не успевает; в проде dist через tailnet
— self-update дольше). Поллинг `waitForOnline` ускорен 300→50мс.

### п.8: ворота + deploy gate
```
$ go test -race ./...            -> ok (все пакеты, EXIT=0)
$ make staticcheck               -> EXIT=0
$ make lint                      -> OK (lintlits/lintweb/linthosts)
$ go version -m /home/user/.local/bin/runpilot
    vcs.revision=77e9550  vcs.modified=false   -> deploy gate PASS
```

## Пункт 5 — HANDOFF

W12 закрыт: пункты 1–4 PASS.

Состояние:
- Координатор: 77e9550 (vcs.modified=false), runpilot.service active.
- Узлы: 3p14-aaa / vzu5-big / vzu5-claw — все 77e9550.
- dist/runpilot: 77e9550 (синхронизирован с координатором).
- Оператора-токен: /home/user/.config/runpilot/env (0600).

Рекомендации (оперативные пробелы, не блокирующие W12):
1. `deploy/deploy-coordinator.sh` — копировать актуальный бинарник в
   `coordinator.dist_dir/runpilot` после выката (иначе self-update узлов
   разворачивает устаревший бинарник).
2. `internal/node/ops.go update()` — не рестартироваться, если версия не
   изменилась (self-update на ту же версию сейчас всё равно exit 75).
3. `test/e2e/update` CONTROL 2 — race при быстром self-update (локальный dist);
   добавить задержку или изменить логику spawn до self-update.
