# п.5 — выкат координатора 3p14-aaa из HEAD (W11)

Дата: 2026-10-07. Хост: 3p14-aaa (tailnet 100.124.155.106). Сервис `runpilot.service`.

## Ворота выката (на HEAD 5dd21db)

```
$ go test -race ./...      # 21 пакет ok, 0 FAIL
$ make staticcheck         # CGO_ENABLED=0, 0 замечаний (exit 0)
$ make lint                # lint-literals / lint-web / lint-hosts — все OK (exit 0)
```

HEAD чистый: `git status --porcelain` = пусто.

## Бинарник

- Сборка из чистого HEAD: `CGO_ENABLED=0 go build -o … ./cmd/runpilot`.
- `go version -m` → `vcs.revision=5dd21db7575d324891872bf66897f43fba14ae3d`
  (= HEAD), `vcs.modified=false`.
- **SHA256 = `823a2432a9b76bd5001a9ae402b43d046df7caf5d1f049a7484b7b6090714ec1`**
  (этот SHA — в строке оператора п.4).

## Атомарная установка

Активных аренд/сессий нет (state до: sessions=0, servers=0, nodes=0) → установка
безопасна.

1. Бэкап: `cp -a /home/user/.local/bin/runpilot runpilot.bak-20261006T224425Z`
   (старый `9a4c8d7`, SHA `c87f8d86…`).
2. Атомарная замена (ETXTBSY-безопасно): `cp` во временный в том же каталоге
   (`runpilot.new.$$`) → `chmod 0755` → `mv -f` на `runpilot`.
3. `systemctl --user restart runpilot`.

## Подтверждение после выката

- `systemctl --user is-active runpilot` = **active**; MainPID=946261.
- `/proc/<MainPID>/exe` → `vcs.revision=5dd21db…`, `vcs.modified=false`.
- `runpilot doctor` = **PASS** (все PASS + новый WARN `coder_out_of_queue` —
  см. ниже, это не сбой, а работа нового чека п.3).
- Состояние сохранено: `/api/v1/state` до/после — 0/0/0 (сессий/серверов/узлов).
- Миграция `015_external_process_cwd` применена: колонка `cwd` в
  `external_process` (PRAGMA table_info: `cwd TEXT NOT NULL DEFAULT ''`).
- Embedded-узел подключился (`embedded: узел хоста подключен`, host 3p14-aaa) —
  регрессий детекции внешних нет.

## Сторонний эффект нового чека (не регрессия)

`runpilot doctor` теперь выводит **WARN `coder_out_of_queue`** — на 3p14-aaa
найдены реальные `qwen`-процессы без `OPENAI_BASE_URL` (запущены вручную, вне
управляемых сессий):

```
WARN coder_out_of_queue — кодер вне очереди:
  815580 (cwd=/mdisk/myhub/MoxMate),
  1849682 (cwd=/home/user/myhub/SimBridge),
  2003831 (cwd=/mdisk/myhub/RunPilot)
```

Это именно то, что п.3 должен ловить: doctor на узле даёт WARN «кодер вне
очереди» с PID и cwd для процесса qwen без управляемой сессии.

## Откат (не потребовался)

При FAIL doctor после рестарта: `mv -f` бэкапа `runpilot.bak-20261006T224425Z`
на место + `systemctl --user restart runpilot`. doctor = PASS → откат не нужен.
