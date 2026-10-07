# Выкат координатора 3p14-aaa — EXEC-правило + ExecPID + node-upgrade-line

**Дата:** 2026-10-07
**Хост:** 3p14-aaa (100.124.155.106), USER-сервис `runpilot.service`,
бинарник `/home/user/.local/bin/runpilot`, API :8788.

## Ворота (все 0) — перед выкатом

| ворота | результат |
|---|---|
| `go test -race ./...` | ok, exit 0 |
| `make staticcheck` (CGO_ENABLED=0) | 0 замечаний, exit 0 |
| `make lint` (literals + web + hosts) | OK ×3, exit 0 |

## Сборка из чистого HEAD

- HEAD: **`c467ef4`** (п.1 e403a11 + п.2 c467ef4), дерево чистое.
- `CGO_ENABLED=0 go build -o runpilot ./cmd/runpilot`.
- `go version -m runpilot`:
  - `vcs.revision=c467ef404e83bdc53346c46cecf6dfea413e25a8`
  - `vcs.modified=false`
- SHA256 бинарника: **`8717199ef8f1c6e422c592d020bdbcb20ed879056512a2e80de2d928a06b7032`**

## Процедура (ETXTBSY-безопасно, бэкап перед заменой)

1. Бэкап текущего: `runpilot.bak-2bca630-20261007-062952`
   (SHA `2d4e09e9a4674a25dc0734a984dd2baffa47501e06da52695e4c6a2002a3954d`,
   vcs.revision=2bca630 — предыдущий выкат).
2. Атомарная замена: `cp -a runpilot /home/user/.local/bin/runpilot.new.$$ &&
   chmod 755 … && mv -f … /home/user/.local/bin/runpilot` (в тот же каталог).
3. `systemctl --user restart runpilot`.

## Верификация после выката

| проверка | результат |
|---|---|
| `systemctl --user is-active runpilot` | `active` |
| MainPID | `1541270` (был 1054261) |
| `go version -m $(readlink -f /proc/1541270/exe)` | vcs.revision=**c467ef4**, vcs.modified=false |
| `runpilot doctor` (новый бинарник) | все PASS, кроме `coder_out_of_queue`=WARN; **exit 0** |
| версия схемы БД (`MAX(version) FROM schema_version`) | **16** (миграция 016 применена) |
| колонка `session.exec_pid` | на месте |
| `GET /api/v1/sessions` (Bearer) | HTTP **200** |

`coder_out_of_queue` = WARN (не FAIL): 3 кодера вне очереди
(815580 MoxMate, 1849682 SimBridge, 2003831 RunPilot) — корректная работа
расширенного правила (ручные/чужие кодеры без URL шлюза живой сессии).

## Строка обновления узла (п.2, `make node-upgrade-line`)

```
scp 3p14-aaa:/mdisk/myhub/RunPilot/deploy/upgrade-node.sh /tmp/upgrade-node.sh \
  && bash /tmp/upgrade-node.sh 8717199ef8f1c6e422c592d020bdbcb20ed879056512a2e80de2d928a06b7032 3p14-aaa
```

SHA256 из 64 hex (= `sha256sum /home/user/.local/bin/runpilot`) + аргумент хоста
`3p14-aaa`. Печатается `make node-upgrade-line` (ручной перенос SHA исключён).
