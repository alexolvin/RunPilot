# RunPilot — HANDOFF

Координатор + узлы: tmux-сессии кодера (qwen), очередь, шлюз, веб.

## Идентичность узла = host:os_user (2026-10-13)

Идентичность узла = `<host>:<os_user>` (ос_user — `user.Current()` узла, в hello
поле `os_user`). Два узла с одинаковым host и разными os_user подключаются
одновременно. Сессия привязана к узлу: `session.host` + `session.os_user`
(миграция 023). Во всех списках (Web, TUI, `ls`, API) у сессии показывается
`host:os_user`.

- **spawn** — `POST /api/v1/sessions/spawn` принимает `node` (`host:os_user`);
  поле опущено → узел вызывающего. Ответ узла: `SPAWNED | NAME_IN_USE |
  TMUX_SESSION_EXISTS | DIR_NOT_FOUND | AGENT_NOT_FOUND`.
- **Веб «Новая сессия»** — узел (список `host:os_user`), каталог по умолчанию =
  домашний каталог пользователя узла, агент qwen.
- **doctor** — `node_qwen_settings`: `~/.qwen/settings.json` каждого
  подключённого узла (диспатч `check_settings`).

Подробности: `docs/evidence/node-os-user/README.md`.

## Запуск

```
runpilot serve        # координатор (API + веб + шлюз)
runpilot node         # узел (tmux-скан + команды)
runpilot run <t>      # запустить сессию
runpilot ls           # сессии (host:os_user)
runpilot doctor       # проверки PASS/FAIL/WARN
```

## Версия

`cmd/runpilot/VERSION` (формат `YYYY.N.R`), go:embed. Bump последнего числа
при каждом фиксе.

## Артефакты

`docs/evidence/<задача>/` — README + вывод тестов/проверок.
