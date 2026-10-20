# RunPilot — HANDOFF

Координатор + узлы: tmux-сессии кодера (qwen), очередь, шлюз, веб.

## Миграция BotBoss: 9-пунктовое задание (2026-10-19/20)

Задание: фикс `listDirs`, SIGTERM узла, `install.sh`, доказательство ожидания в
очереди, окружение кодера, токен узла, commit `4c61616`. **Все 9 пунктов
выполнены.** Сырые выводы: `docs/evidence/botboss-migration-6-items/raw/`.

- **listDirs (п.1–2):** `DirsMaxOrDefault()` (`internal/config/defaults.go`)
  применяет `100` при `DirsMax <= 0`; литерала `100` в `ops.go` нет; ошибка
  `os.ReadDir` логируется на WARN. Юнит-тест `internal/node/ops_test.go`:
  0 каталогов до фикса, 2 после.
- **Корень пустых dirs (п.3):** setting БД `node.project_dir_match="myhub"`
  отфильтровал все каталоги vzu5-big. Очищен через API. `/dirs` отдаёт
  BotBoss + migration (`raw/dirs-item3.txt`).
- **SIGTERM/SIGINT (п.4):** узел завершается корректно,
  `systemctl --user restart runpilot-node` ≤ 5 с.
- **install.sh (п.5):** существующий `config.yaml` не перезаписывается.
- **Ожидание в очереди (п.6):** цепочка `queue_entry` → `QUEUE_SKIP
  no_up_server` → `cooldown` → `LEASE_GRANT origin=DISPATCH`.
  `raw/queue-entry-item6.txt`, `raw/queue-skip-item6.txt`,
  `raw/why-item6.txt`, `raw/lease-at-grant-item6.txt`.
- **Окружение кодера (п.7):** только `RUNPILOT_SID` + gateway `OPENAI_*` +
  `QWEN_CODE_*`; ключей бирж, Vault и `RUNPILOT_NODE_TOKEN` нет.
  `raw/env-item7.txt`.
- **Токен узла (п.8):** `RUNPILOT_NODE_TOKEN` в env кодера — принято владельцем
  как допустимое (2026-10-19), не дефект. Записано в `report.md`.
- **Commit 4c61616 (п.9):** `resolveTailnetHost`/`parseTailnetHost`
  (`internal/api/nodes.go`). `raw/git-show-4c61616-item9.txt`.

## Диагностика stale-аренды + режимы сервера (2026-10-18)

**Найдено (живой координатор 3p14-aaa):**
1. **3p14 «занят» NextLife** — зависшая аренда #531 (`1YHT3GSSYS`, ACTIVE,
   granted 2026-10-15 17:21:31, Origin=IMPLICIT), сессия IDLE. Корень: после
   закрытия хода 232 (`TURN_DONE` 17:21:22, аренда #530 снята) кодер сделал
   запросы 10365–10370 → шлюз удержал (`held_request`) → планировщик веткой
   `HeldRequest` (`dispatch.go:127`) выдал неявную аренду #531, но сессия IDLE →
   `checkTurn` (требует `SessionRunning`) аренду не снимает.
2. **3p14 DRAINING** — `operator_state: 3p14|server_drain` (ручной дренаж,
   переживает рестарты).
3. **Внешний кодер pid 1960695** — живой qwen в tmux «RunPilot» панель %50,
   запущен вручную (нет gateway-URL / `@runpilot_sid`).

**План (одобрено 2026-10-18):**
- **П1 (немедленно):** `lease release 1YHT3GSSYS`, `server undrain 3p14`;
  qwen 1960695 не убивать, пока `ps` не покажет, что процесс не в панели с
  идущим ходом.
- **П2 (код):** HeldRequest-ветка — при неявной аренде атомарно IDLE→RUNNING +
  создать turn; `checkTurn` к любой ACTIVE-аренде без `SessionRunning`; пороги
  только `turn.done_quiet_sec` / `turn.done_stable_sec`. Тест-воспроизведение на
  virtual clock → `docs/evidence/`.
- **П3 (код/UI):** DRAINING не снимает идущие ходы (убрать `serverDownLocked`
  из drain); удалить PAUSED; UI «Вывести из выдачи» / «В выдачу» + тултип.

**Статус (2026-10-18, в работе):**
- **П1 — готово (без действий).** Ситуация с момента снапшота изменилась:
  `1YHT3GSSYS` — это SID сессии NextLife, теперь RUNNING с активным ходом
  (старт 00:25:32Z, панель BUSY); stale-аренда от 2026-10-15 уже снята; 3p14 UP
  (не DRAINING); qwen 1960695 не существует. `lease release` НЕ выполнен —
  отцепил бы идущий ход (подтверждено оператором: «Не снимать»).
- **П2 — готово.** Тест-воспроизведение `TestIdleSessionActiveLease`
  (`internal/scheduler/held_implicit_lease_test.go`, virtual clock): сессия
  IDLE + аренда ACTIVE + открытый ход. До фикса падает (`аренда не снята
  (stale): ACTIVE`), после — проходит. Вывод: `docs/evidence/held-implicit-lease/`.
  Фиксы: (1) `dispatch.go` ветка HeldRequest — `ensureTurn` атомарно с выдачей
  неявной аренды; (2) `complete.go` `tickComplete` — `checkTurn` к любой
  ACTIVE-аренде (не только RUNNING/DETACHED), снятие посреди хода без условия
  `SessionRunning`; `closeTurn` — IDLE-сессия без перехода. Новых порогов и
  release_reason нет (только `turn.done_quiet_sec`/`turn.done_stable_sec`).
  Полный набор `internal/scheduler/` — OK.
- **П3 — готово.** DRAINING не снимает идущие ходы: `serverDownLocked` из drain
  убран (`scheduler.go` `ServerState`: `if st != model.ServerDraining`). Состояние
  PAUSED удалено (дублирует DRAINING): `model.go` `ServerPaused`, `operator_state.go`
  `OperatorStateServerPaused`, `serve.go` `SetServerPause`, `scheduler.go`
  `RestoreOperatorStates`, `api/server.go` `SetServerPause`+маршрут, `api/views.go`
  `handleServerPause`, `operator_state_test.go`. UI (`web/app/views/servers.js` +
  `i18n/ru.js`): кнопка «Вывести из выдачи» / «В выдачу» (undrain), тултип «Новые
  ходы не выдаются, идущие доделываются»; диалог ServerDrain и кнопка паузы убраны.
  `go build ./...`, `go test ./internal/...`, `lint-web` — OK.

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
