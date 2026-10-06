# п.3 — «кодер вне очереди»: doctor WARN + UNMANAGED в вебе с командой перезапуска

Дата: 2026-10-07. Хост: 3p14-aaa.

## Задача

Процесс `qwen`, запущенный вручную (вне управляемой сессии runpilot, без
`OPENAI_BASE_URL`), должен:

1. На узле `runpilot doctor` давать **WARN «кодер вне очереди»** с PID и cwd.
2. В веб-интерфейсе показываться как **UNMANAGED** с командой перезапуска
   `runpilot run --dir <cwd>`.

Операционный кейс: на vzu5-claw был `qwen` (PID 212415) без `OPENAI_BASE_URL`
и без `*_SID` — запущен вручную, вне очереди. Раньше такой процесс либо
«вешался» без меток, либо требовал ручного `ps`. Теперь — явный WARN + команда.

## doctor: `coder_out_of_queue` (internal/doctor/doctor.go)

Новая проверка в `DefaultChecks` (после `coder_timeout_env`):

- `Context.Cmdline` — скомпилированный `cmdline_regex` профиля qwen
  (`(^|/)qwen(\s|$)`), загружается в `cmd/runpilot/doctor.go` через
  `profiles.LoadQwen()`.
- Демон строит дерево процессов из `ps -A -o pid=,ppid=,args=` (стандalone,
  без tmux) — `buildProcTree` + `procTree` (введён в п.2).
- Для каждого процесса, совпадающего с `Cmdline`: если в `/proc/<pid>/environ`
  **нет** `OPENAI_BASE_URL` — это «кодер вне очереди» (управляемые сессии
  всегда имеют `OPENAI_BASE_URL` — его ставит spawn-окружение).
- Найден → `WARN "кодер вне очереди: <pid> (cwd=<cwd>), …"` (PID + cwd из
  `readlink /proc/<pid>/cwd`, сортировка по PID). Ничего → `PASS`.
- `Cmdline == nil` (нет профиля) → `PASS` (пропуск), `Cfg == nil` → `WARN` (нет конфига).

Данные `procCwd` вынесены в инжектируемую переменную `procCwd` (как
`readProcEnviron`/`psOutput`), чтобы тест подменял /proc.

### Тесты (internal/doctor/coder_out_of_queue_test.go)

- `TestCoderOutOfQueueWarn` — qwen без `OPENAI_BASE_URL` → WARN, в тексте PID
  `101` и `cwd=/home/user/project`.
- `TestCoderOutOfQueueManagedPass` — qwen с `OPENAI_BASE_URL` → PASS.
- `TestCoderOutOfQueueNone` — qwen-процессов нет → PASS.

```
$ go test -race ./internal/doctor/
ok      runpilot/internal/doctor
```

## cwd-датафлоу (node → coordinator → web)

`cwd` процесса передаётся по всей цепочке:

| Слой | Файл | Изменение |
|------|------|----------|
| proto | `internal/proto/proto.go` | `ExternalProc.CWD` (`cwd,omitempty`) |
| node scan | `internal/node/external.go` | `ScanExternal(…, cwd func(pid int) string)`; `readProcCwd` = `os.Readlink(/proc/<pid>/cwd)` |
| node cycle | `internal/node/node.go` | `ScanExternal(…, readOpenAIBaseURL, readProcCwd)` |
| model | `internal/model/model.go` | `ExternalProcess.CWD` |
| migration | `internal/store/migrations/015_external_process_cwd.sql` | `ALTER TABLE external_process ADD COLUMN cwd TEXT NOT NULL DEFAULT ''` |
| store | `internal/store/external.go` | INSERT + `ON CONFLICT … cwd = excluded.cwd`, SELECT, `scanExternals(&p.CWD)` |
| api view | `internal/api/externals.go` | `externalView.CWD`, `handleExternals`, `applyExternals(CWD: e.CWD)` |

Версия схемы БД: 14 → **15** (обновлено в `store_test.go`).

### Тесты node

`TestScanExternalCron` передаёт 5-й аргумент (cwd-функция) и сверяет
`e.CWD` с `/home/user/project`; `TestScanExternalSystemdAndExclusion` — 5-й
аргумент `""`.

```
$ go test -race ./internal/node/
ok      runpilot/internal/node
```

## Веб: UNMANAGED + команда перезапуска (web/app/views/queue.js)

Компонент `ExternalRow`: для внешнего кодера с `target === 'unknown'`
(нет `OPENAI_BASE_URL`) показывает:

- бейдж **UNMANAGED** («Вне очереди», `tone=warning`);
- строку «Перезапустить под runpilot:» + `runpilot run --dir <cwd>`
  (mono-блок) + кнопку «Копировать» (clipboard, флэш «Скопировано»).

`runpilot run --dir <dir>` — штатная команда создания управляемой сессии в
каталоге кодера (флаг `--dir`, по умолчанию `.`). Копируется в буфер, оператор
вставляет в терминале узла.

I18N (web/app/i18n/ru.js): `unmanaged`, `restart`, `copy`, `copied`.
CSS (web/styles/components.css): `.ext-restart*`.

### Скриншот

`unmanaged-external.png` (полный кадр) и `unmanaged-external-row.png` (кадр по
строке) — веб-интерфейс (экран «Очередь», раздел «Внешние кодеры») с процессом
`target=unknown`: бейдж **Вне очереди** + `Цель: unknown` + команда
`runpilot run --dir /home/user/project` + кнопка «Копировать».

> PNG добавлены через `git add -f`: правило `.gitignore`
> `docs/evidence/**` по умолчанию не хранит изображения, но п.3 явно требует
> скриншот в артефакте.

Съёмка: headless Google Chrome, изолированный экземпляр координатора
(temp-конфиг/temp-БД/свой токен, прод не затрагивался), seed-строка
`external_process` (target=unknown, cwd=/home/user/project, status=ACTIVE).

## Ворота

```
$ go test -race ./...        # все пакеты ok
$ make staticcheck            # CGO_ENABLED=0, 0 замечаний
$ make lint                   # lint-literals / lint-web / lint-hosts — OK
```
