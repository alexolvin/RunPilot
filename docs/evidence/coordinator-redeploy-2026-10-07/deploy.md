# Повторный выкат координатора 3p14-aaa из HEAD 2bca630 (редизайн W11 п.3)

Дата: 2026-10-07. Хост: 3p14-aaa (tailnet 100.124.155.106). Сервис
`runpilot.service` (user), бинарник `/home/user/.local/bin/runpilot`, API
:8788, web :8790, gateway :8787.

Причина: редизайн W11 п.3 — единое правило «кодер вне очереди»
`node.CoderOutOfQueue` для doctor и веба (коммит `2bca630`).

## Ворота выката (на HEAD 2bca630, чистое дерево)

```
$ git status --porcelain   # пусто
$ go test -race ./...      # все пакеты ok, 0 FAIL
$ make staticcheck         # CGO_ENABLED=0, 0 замечаний (exit 0)
$ make lint                # lint-literals / lint-web / lint-hosts — все OK (exit 0)
```

## Бинарник

- Сборка из чистого HEAD: `CGO_ENABLED=0 go build -o … ./cmd/runpilot`.
- `go version -m` → `vcs.revision=2bca63081dedbf28112e10c85efb1b90911f5fbc`
  (= HEAD `2bca630`), `vcs.modified=false`.
- **SHA256 = `2d4e09e9a4674a25dc0734a984dd2baffa47501e06da52695e4c6a2002a3954d`**
  (этот SHA — в самодостаточной строке обновления узла, deploy/README + HANDOFF).

## Атомарная установка

State до: `mode=NORMAL`, `sessions=null` (0 активных аренд), nodes=2
(3p14-aaa + vzu5-claw) → установка безопасна.

1. Бэкап: `cp -a runpilot runpilot.bak-20261007T…Z` (старый `5dd21db`,
   SHA `823a2432…`).
2. Атомарная замена (ETXTBSY-безопасно): `cp` во временный в том же каталоге
   (`runpilot.new.$$`) → `chmod 0755` → `mv -f` на `runpilot`.
3. `systemctl --user restart runpilot`.

## Подтверждение после выката

- `systemctl --user is-active runpilot` = **active**; MainPID **1054261**
  (был 946261).
- `/proc/<MainPID>/exe` → `vcs.revision=2bca630…`, `vcs.modified=false`.
- `runpilot doctor` = **PASS**:
  - `PASS coder_timeout_env — нет живых управляемых сессий`;
  - `WARN coder_out_of_queue — кодер вне очереди: 815580 (MoxMate), 1849682
    (SimBridge), 2003831 (RunPilot)` — те же 3 ручных qwen (без управляемой
    сессии), единое правило их по-прежнему ловит (sessionSID="");
  - остальные чеки PASS, FAIL — нет.
- Состояние сохранено: `/api/v1/state` до/после — NORMAL, 0 сессий, 2 узла
  (vzu5-claw реконнектнулся в штатном WS-цикле).
- Embedded-узел 3p14-aaa подключен; дедуп внешних кодеров не сломан.

## Откат (не потребовался)

При FAIL doctor: `mv -f runpilot.bak-20261007T…Z runpilot` +
`systemctl --user restart runpilot`. doctor = PASS → откат не нужен.
