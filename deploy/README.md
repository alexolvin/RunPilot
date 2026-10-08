# deploy/ — выкат координатора и обновление узлов

## Процедура выката (W12 п.3)

Полный цикл: от изменения кода до актуальных узлов. Порядок фиксирован.

1. **Ворота** — все 0 (иначе выкат запрещён):
   - `go test -race ./...`
   - `make staticcheck`
   - `make lint`
2. **Сборка из чистого HEAD** — `CGO_ENABLED=0 go build -o /tmp/runpilot-new ./cmd/runpilot`.
   Бинарник несёт VCS-stamp (`vcs.revision` = HEAD, `vcs.modified=false`).
   Из грязного дерева (`vcs.modified=true`) координатор НЕ выкатывается —
   `deploy-coordinator.sh` отказывает.
3. **Выкат координатора** — `bash deploy/deploy-coordinator.sh /tmp/runpilot-new`
   (бэкап, атомарная замена, рестарт, проверка активности). После рестарта узлы
   сами переподключаются (≤30с, W12 п.1) и self-update до версии координатора.
4. **Обновление узлов** — для каждого ONLINE-узла (tailscale ssh):
   `upgrade-node.sh <sha> 3p14-aaa` (строка — `make node-upgrade-line`).
   Узлы уже self-update при рестарте, но явная строка — гарантия и для узлов,
   которые не self-updated. **OFFLINE-узлы** — НЕ трогаем; список передаём
   оператору с одной строкой обновления на узел.
5. **Таблица узлов** — до/после рестарта со временем переподключения
   (артефакт в `docs/evidence/W*-accept/node-table.txt`, требование ≤30с).
6. **HANDOFF** — отметить закрытый цикл в `.agent/docs/HANDOFF.md`.

## `deploy-coordinator.sh` — выкат КООРДИНАТОРА

Проверка `vcs.modified`, бэкап, атомарная замена (`mv -f`, ETXTBSY-безопасно),
рестарт, проверка активности. **ОТКАЗЫВАЕТ** при `vcs.modified=true`
(бинарник из грязного дерева) — выкат только из чистого HEAD.

```
deploy-coordinator.sh <NEW_BIN>
```

| env | значение по умолчанию |
|-----|----------------------|
| `COORD_BIN` | `/home/user/.local/bin/runpilot` |
| `COORD_SERVICE` | `runpilot` |
| `RESTART_CMD` / `IS_ACTIVE_CMD` | [test-hook] подмена systemctl (песочница) |

- Тест: `bash deploy/deploy-coordinator.sandbox-test.sh` — 7/7 (отказ при
  `vcs.modified=true`, PASS при `false`).

## `upgrade-node.sh` — обновить бинарник УЗЛА с координатора

Одна команда на машине узла: скачивает бинарник с координатора (3p14-aaa,
tailscale), сверяет SHA256, делает предпроверку doctor, бэкапит, атомарно
заменяет (`mv -f`, ETXTBSY-безопасно), перезапускает `runpilot-node` и
проверяет doctor (при FAIL — атомарный откат `mv -f`).

### Самодостаточная строка оператора (на машине узла, чистая машина)

Скрипт берётся с координатора (`scp`), запуск — с **SHA256** бинарника и
**хостом** координатора. Запускается как есть, дополнительных шагов нет:

```
scp 3p14-aaa:/mdisk/myhub/RunPilot/deploy/upgrade-node.sh /tmp/upgrade-node.sh \
  && bash /tmp/upgrade-node.sh 02b4079000fe6c0334b247b1e31ecae1e69495b07631a76c0b7333684c914c12 3p14-aaa
```

- Строка печатается командой **`make node-upgrade-line`** (на координаторе):
  читает `sha256sum` установленного бинарника координатора, проверяет 64 hex
  (иначе FAIL, строка не печатается). Ручной перенос SHA запрещён.
  `02b40790…` — SHA256 бинарника текущего координатора
  (`/home/user/.local/bin/runpilot` на 3p14-aaa, = HEAD `3ba63ef`,
  `vcs.modified=false`). **Проверить на координаторе:**
  `sha256sum /home/user/.local/bin/runpilot`.
- `3p14-aaa` — хост координатора (по умолчанию скрипта, указан явно).
- Строка `upgrade-node.sh <sha> 3p14-aaa` **без** ведущего `scp … /tmp/…` —
  **дефект**: скрипт ещё не на узле. Полная строка — с `scp`.

Бинарник ставится **ТОЛЬКО при совпадении SHA256** (иначе FAIL, узел не
трогается). Временный файл бинарника — в каталоге целевого (`mktemp -p`), не в
/tmp; сразу после сверки `chmod 0755`; предпроверка запускает именно его; при
PASS тот же файл `mv -f` на место; `trap EXIT` удаляет временные файлы.

### Аргументы и env

```
upgrade-node.sh <EXPECTED_SHA256> [COORD_HOST]
```

| env | значение по умолчанию |
|-----|----------------------|
| `COORD_BIN` | `/home/user/.local/bin/runpilot` (бинарник на координаторе) |
| `NODE_BIN` | `$HOME/.local/bin/runpilot` (бинарник узла) |
| `NODE_SERVICE` | `runpilot-node` |
| `NODE_ENV` | `$HOME/.config/runpilot/env` (doctor берёт конфиг+секреты) |
| `LOCAL_BIN` | [test-hook] cp из локального пути вместо scp (песочница) |
| `RESTART_CMD` / `IS_ACTIVE_CMD` | [test-hook] подмена systemctl (песочница) |

### Проверки

- `shellcheck deploy/upgrade-node.sh` — 0 замечаний.
- `bash deploy/upgrade-node.sandbox-test.sh` — полный проход шагов 1–6 на
  PASS-пути (включая `umask 077`), 16/16.

Артефакты: `docs/evidence/upgrade-node-v3-2026-10-07/upgrade-node.md`
(п.1), `docs/evidence/coordinator-redeploy-execrule-2026-10-07/deploy.md`
(выкат c467ef4, SHA `8717199e…` — текущий), `docs/evidence/coordinator-redeploy-2026-10-07/deploy.md`
(выкат 2bca630, SHA `2d4e09e9…`), `docs/evidence/coordinator-deploy-2026-10-07/deploy.md` (первый выкат 5dd21db).
