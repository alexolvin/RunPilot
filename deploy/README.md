# deploy/ — обновление узлов

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
  && bash /tmp/upgrade-node.sh 823a2432a9b76bd5001a9ae402b43d046df7caf5d1f049a7484b7b6090714ec1 3p14-aaa
```

- `823a2432…` — SHA256 бинарника текущего координатора
  (`/home/user/.local/bin/runpilot` на 3p14-aaa, = HEAD `5dd21db`,
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
(п.1), `docs/evidence/coordinator-deploy-2026-10-07/deploy.md` (п.5, SHA).
