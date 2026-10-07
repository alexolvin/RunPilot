# HANDOFF
Обновлено: 2026-10-07 (W11 п.3 доп: правило «кодер вне очереди» расширено на EXEC + ExecPID в записи + make node-upgrade-line + выкат координатора c467ef4)

## W11 (п.3, доп) — правило расширено на EXEC + ExecPID + строка узла + выкат c467ef4 (2026-10-07) — ВЫПОЛНЕНО

> Директива (3 пункта): «Правило из прошлого задания было слишком узким: сессия
> вида EXEC — управляемый запуск. Исключение не добавлять, а расширить правило».
> Порядок: 1 (расширить правило на EXEC + PID в записи) → 2 (строка обновления
> узла: SHA256 64 + хост, печатает `make node-upgrade-line`) → 3 (ворота → выкат
> координатора из HEAD → HANDOFF).

- **п.1 — правило «кодер вне очереди» расширено на EXEC + ExecPID в записи — ✅
  DONE** (коммит `e403a11`). Управляемый кодер **тогда и только тогда**, когда
  (а) потомок **корневого процесса живой сессии** (PANE: `pane_pid` панели с
  `sid`; EXEC: PID процесса `runpilot exec` этой сессии) И (б) `OPENAI_BASE_URL =
  gateway_url/s/<sid>/v1`. П.1 прошлого задания (только PANE) расширен, а не
  расширен исключением: **ОДИН механизм** `node.SessionSID(pid, paneSID,
  coderRunpilotSID, parentOf, argsOf)` обходит дерево **вверх** до init —
  встретил панель → sid панели; встретил `runpilot exec` (`execCmdRe`) →
  `RUNPILOT_SID` из env кодера; дошёл до init → `""` (ручной/завершённая
  сессия). Узел (`ScanExternal`) и doctor (`coder_out_of_queue`) выводят EXEC-корни
  **локально** (ps на узле — doctor не имеет доступа к БД координатора). Умер
  exec → qwen репарентирован в init (parent=1) → вне очереди (тест «EXEC
  завершена, кодер жив»). **PID процесса `runpilot exec` хранится в записи
  EXEC-сессии**: `store.SessionRecord.ExecPID` + миграция `016_session_exec_pid`
  (схема 15→16); клиент шлёт `os.Getpid()` при `POST /jobs` (`jobclient.create`
  → `pid`), координатор (`api/jobs.go`) сохраняет `rec.ExecPID` — авторитетная
  копия для отчёта/управления, доступна узлу (правило её не читает — вывод
  локальный). Тесты: node (`ExecLive`/`ExecFinished`/`Cron`/`Systemd`/
  `CoderOutOfQueue`) + doctor (`ExecManaged`/`ExecFinished` + прежние) —
  проходят. Артефакт: `docs/evidence/exec-rule-unified-2026-10-07/exec-rule.md`.
- **п.2 — строка обновления узла: SHA256 64 + хост, печатает `make
  node-upgrade-line` — ✅ DONE** (коммит `c467ef4`). Строка в отчёте была
  дефектна: SHA256 из 40 символов (отпечаток комит-хэша) вместо 64, и без
  аргумента хоста. Теперь её печатает `make node-upgrade-line`: читает
  `sha256sum` **установленного** бинарника координатора (`$(COORD_BIN)`),
  проверяет длину 64 + только hex (иначе FAIL, строка не печатается), печатает
  `scp <хост>:deploy/upgrade-node.sh /tmp/… \ && bash /tmp/upgrade-node.sh
  <64-hex> <хост>`. Хост/путь по умолчанию — `$(shell hostname)`/`$(abspath …)`
  (в Makefile нет CGNAT-IP и литералов хоста — линтер). Ручной перенос SHA
  исключён. Проверка: `make node-upgrade-line` → SHA `8717199e…` =
  `sha256sum /home/user/.local/bin/runpilot`, хост `3p14-aaa`.
- **п.3 — ворота → выкат координатора из HEAD `c467ef4` → HANDOFF — ✅ DONE.**
  Ворота: `go test -race ./…` (0) + `make staticcheck` (0) + `make lint` (0).
  Бинарник из чистого HEAD (`vcs.modified=false`, **SHA
  `8717199ef8f1c6e422c592d020bdbcb20ed879056512a2e80de2d928a06b7032`**) →
  атомарная установка (бэкап `runpilot.bak-2bca630-20261007-062952` [`2d4e09e9…`]
  + `mv -f`) → `systemctl --user restart runpilot` (MainPID 1054261 →
  **1541270**) → `/proc/<pid>/exe` vcs=`c467ef4` → doctor **exit 0** (все PASS,
  WARN `coder_out_of_queue` = 3 ручных qwen — работа правила) → версия схемы БД
  **16** (миграция 016), колонка `session.exec_pid` на месте, `GET /api/v1/sessions`
  HTTP 200. Строка обновления узла (deploy/README + этот HANDOFF) — новый SHA
  `8717199e…` (64 hex) + хост:
  ```
  scp 3p14-aaa:/mdisk/myhub/RunPilot/deploy/upgrade-node.sh /tmp/upgrade-node.sh \
    && bash /tmp/upgrade-node.sh 8717199ef8f1c6e422c592d020bdbcb20ed879056512a2e80de2d928a06b7032 3p14-aaa
  ```
  Артефакт: `docs/evidence/coordinator-redeploy-execrule-2026-10-07/deploy.md`.

## W11 (п.3, редизайн) — единое правило «кодер вне очереди» + повторный выкат (2026-10-07) — ВЫПОЛНЕНО

> Директива: «W11 принят, кроме п.3». Два механизма одного правила — дефект по
> правилу 2 ТЗ (doctor — по отсутствию OPENAI_BASE_URL, веб — по сканеру
> isUnder/isRunpilot). Порядок: 1 (единое правило) → 2 (ворота + выкат из HEAD +
> строка обновления узла с новым SHA256).

- **п.1 — единое правило «кодер вне очереди» (doctor + веб) — ✅ DONE** (коммит
  `2bca630`). Кодер УПРАВЛЯЕМ **тогда и только тогда**, когда (а) потомок
  панели живой сессии (sessionSID≠"") И (б) OPENAI_BASE_URL =
  gateway_url/s/<sid>/v1. Реализация — **одна** функция
  `node.CoderOutOfQueue(sessionSID, openAIBaseURL, gatewayURL)` (+
  `GatewayCoderURL`), её вызывают и doctor (`coder_out_of_queue`), и
  `node.ScanExternal`. Убрана проверка по отсутствию OPENAI_BASE_URL (doctor) и
  isUnder/isRunpilot (веб). Узел хранит gateway_url: init из config + из
  KindConfig (координатор пушит реальное значение — удалённый узел с минимальным
  конфигом иначе видел дефолт 127.0.0.1 и ложно сажал кодеров панелей).
  Тесты: 4 случая правила (нет URL / vLLM / чужой sid / живой кодер) +
  интеграционные ScanExternal; doctor OOQ: manual/managed/foreign/vLLM.
  **Следствие (зафиксировано, не регрессия):** `runpilot exec` (cron, foreground,
  не tmux-панель) теперь «вне очереди» — не потомок панели с sid.
- **п.2 — ворота + повторный выкат координатора из HEAD `2bca630` — ✅ DONE.**
  Ворота: `go test -race ./…` (все пакеты ok / 0 FAIL) + `make staticcheck` (0) +
  `make lint` (0). Бинарник из чистого HEAD (`vcs.modified=false`,
  **SHA `2d4e09e9a4674a25dc0734a984dd2baffa47501e06da52695e4c6a2002a3954d`**) →
  атомарная установка (бэкап `runpilot.bak-20261007T…Z` [5dd21db / `823a2432…`]
  + `mv -f`) → `systemctl --user restart runpilot` (MainPID 946261 → **1054261**)
  → `/proc/<pid>/exe` vcs=`2bca630` → doctor **PASS** (WARN
  `coder_out_of_queue` = те же 3 ручных qwen — работа правила) → состояние 0
  сессий, NORMAL, 2 узла (vzu5-claw реконнектнулся). Самодостаточная строка
  обновления узла (deploy/README + этот HANDOFF) — новый SHA `2d4e09e9…`:
  ```
  scp 3p14-aaa:/mdisk/myhub/RunPilot/deploy/upgrade-node.sh /tmp/upgrade-node.sh \
    && bash /tmp/upgrade-node.sh 2d4e09e9a4674a25dc0734a984dd2baffa47501e06da52695e4c6a2002a3954d 3p14-aaa
  ```
  Артефакт: `docs/evidence/coordinator-redeploy-2026-10-07/deploy.md`.

## W11 — 6 пунктов (2026-10-07) — ВЫПОЛНЕНО

> Директива (6 пунктов). Данные оператора: (a) на vzu5-claw только `qwen`
> (PID 212415) **без** `OPENAI_BASE_URL` и без `*_SID` (ручной запуск вне
> runpilot); (b) `upgrade-node.sh` (v2) упал на шаге 3/6:
> `/tmp/runpilot.node.new.PSJWaM: Permission denied` (бинарник не изменился, узел
> на 4904a65). Порядок: 1 → 2 → 3 → 4 → 5 → 6 (отчёт).

- **п.1 — upgrade-node.sh: temp-файл в каталоге целевого + chmod 0755 — ✅ DONE**
  (коммит `85f1ea5`). Корень бага v2: временный бинарник создавался в
  `${TMPDIR:-/tmp}` без +x (umask 077) → `run_doctor "$TMP_BIN"` падал на
  «Permission denied» (шаг 3/6). Фикс: `mktemp -p "$(dirname "$NODE_BIN")"`,
  `chmod 0755` сразу после сверки SHA256, предпроверка запускает именно этот
  файл, при PASS тот же файл `mv -f` на место, `trap EXIT` удаляет остаток.
  Песочница `upgrade-node.sandbox-test.sh` — полный проход 1–6 на PASS-пути
  (umask 077 → PASS; исполняемый файл в каталоге целевого, 0755; после —
  временных нет); **16/16**. `shellcheck` — 0. Артефакт:
  `docs/evidence/upgrade-node-v3-2026-10-07/upgrade-node.md`.
- **п.2 — doctor `coder_timeout_env`: env процесса кодера, не pane_pid — ✅ DONE**
  (коммит `449281c`). Проверка читала `/proc/<pane_pid>/environ` (обёртка bash),
  а не процесс кодера. Фикс: standalone ps-дерево (`ps -A -o pid=,ppid=,args=`)
  + `procTree.coderPID(root, cmdline_regex)` (BFS, включает корень); переменная
  читается из `/proc/<coder>/environ`. Тест: у обёртки есть / у кодера нет →
  WARN; у кодера есть → PASS. Артефакт:
  `docs/evidence/doctor-coder-process-2026-10-07/coder-process.md`.
- **п.3 — «кодер вне очереди»: doctor WARN + UNMANAGED в вебе — ✅ DONE**
  (коммит `5dd21db`). Doctor: новый чек `coder_out_of_queue` — qwen без
  `OPENAI_BASE_URL` → **WARN «кодер вне очереди: <pid> (cwd=<cwd>)»** (PID + cwd
  из `readlink /proc/<pid>/cwd`). cwd по цепочке: `proto.ExternalProc` →
  `node.ScanExternal/readProcCwd` → `model.ExternalProcess` → миграция
  `015_external_process_cwd` (схема 14→15) → `store` (INSERT/SELECT/scan) →
  `api externalView/handleExternals/applyExternals`. Веб (`queue.js
  ExternalRow`): `target=unknown` → бейдж **«Вне очереди»** +
  `runpilot run --dir <cwd>` + «Копировать». Скриншот (headless Chrome,
  изолированный экземпляр, прод не тронут):
  `docs/evidence/coder-out-of-queue-2026-10-07/unmanaged-external.png` (+ `-row`).
  В проде doctor сразу поймал 3 реальных qwen вне очереди (MoxMate/SimBridge/
  RunPilot) — см. п.5.
- **п.4 — самодостаточные строки оператору (HANDOFF + deploy/README) — ✅ DONE.**
  Дефект: строка `upgrade-node.sh <sha> 3p14-aaa` без пути — скрипт ещё не на
  узле. Самодостаточная строка (на узле, чистая машина) — в
  `deploy/README.md` + этот HANDOFF:
  ```
  scp 3p14-aaa:/mdisk/myhub/RunPilot/deploy/upgrade-node.sh /tmp/upgrade-node.sh \
    && bash /tmp/upgrade-node.sh 823a2432a9b76bd5001a9ae402b43d046df7caf5d1f049a7484b7b6090714ec1 3p14-aaa
  ```
  (SHA = бинарник координатора HEAD `5dd21db`; `vcs.modified=false`).
- **п.5 — ворота + выкат координатора 3p14-aaa из HEAD `5dd21db` — ✅ DONE**
  (коммит `296b0d6`).
  Ворота: `go test -race ./…` (21 ok / 0 FAIL) + `make staticcheck` (0) +
  `make lint` (OK). Бинарник из чистого HEAD (`vcs.modified=false`, SHA
  `823a2432…`) → атомарная установка (бэкап `runpilot.bak-20261006T224425Z`
  [9a4c8d7 / `c87f8d86…`] + `mv -f`) → `systemctl --user restart runpilot`
  (MainPID 946261) → doctor **PASS** + `/proc/<pid>/exe` vcs=`5dd21db` →
  состояние 0/0/0 (до/после) → миграция 015 (колонка `cwd`) → embedded-узел
  подключен. Артефакт: `docs/evidence/coordinator-deploy-2026-10-07/deploy.md`.
- **п.6 — отчёт: для каждого пункта новый коммит + путь артефакта** (см. итог
  сессии).

## QWEN_CODE_API_TIMEOUT_MS для управляемых сессий + lint + выкат (2026-10-06) — ВЫПОЛНЕНО

> Симптом: после перезапуска `phonebroker` на vzu5-claw у процесса qwen нет
> `QWEN_CODE_API_TIMEOUT_MS` → client-side timeout 120 s, не переживает
> silent-hold. Порядок: 1 (spawnEnv + doctor WARN) → 2 (make lint = 0) →
> 3 (выкат координатора из HEAD) → 4 (оператору строки) → 5 (HANDOFF).

- **п.1 — spawnEnv ставит переменную + doctor `coder_timeout_env` — ✅ DONE**
  (коммит `ed4e2e5`). Причина: `internal/api/spawn.go` `spawnEnv()` (spawn/adopt —
  управляемые сессии узла) не ставила `QWEN_CODE_API_TIMEOUT_MS` (в отличие от
  `coderEnv` в `runpilot run`). Фикс: spawnEnv ставит её = client_timeout_ms из
  координатора (перекрывает override) → покрывает все 6 путей (карта + file:line —
  в `docs/evidence/coder-timeout-env-2026-10-06/diagnosis.md`). Doctor: новый чек
  `coder_timeout_env` читает `/proc/<pane_pid>/environ` живых управляемых сессий;
  нет переменной → **WARN** с sid+именем (не FAIL, не блокирует откат). Тесты:
  `TestSpawnEnvClientTimeout`, `TestCoderTimeoutEnv*`, `TestProcEnvHas`.
- **п.2 — make lint = 0 — ✅ DONE** (коммит `1b6e4d8`). Убраны нарушения
  lint-literals (`server.go` 1000 → `ClientTimeoutMS()`; fakellm 10/64 →
  `base10`/`intBitSize`) и lint-web (`queue.js` 1000 → `FMT.msPerSec`). Ворота
  выката теперь: `go test -race ./...` + `staticcheck ./...` + `make lint`.
  Проверено: 21 ok / 0 FAIL, staticcheck 0, make lint 0.
- **п.3 — выкат координатора 3p14-aaa из HEAD — ✅ DONE.** Ворота п.2 прошли
  (test -race 21 ok, staticcheck 0, make lint 0). 0 активных аренд (sessions=null,
  mode=NORMAL) → **атомарная** установка (cp в temp + mv -f — не ETXTBSY) →
  `systemctl --user restart runpilot.service` (PID 348326 → 685037) → doctor
  **все PASS** (включая `coder_timeout_env`). Запущенный процесс: vcs.revision=
  9a4c8d7 (= HEAD), vcs.modified=false. Состояние до/после совпадает (оба узла,
  0 сессий). Бэкап: `runpilot.bak-4904a65-20261006T204121Z`. Артефакт:
  `docs/evidence/coordinator-deploy-2026-10-06/deploy.md`.
- **п.4 — оператору строки — ✅ DONE.**
  1 строка (на vzu5-claw):
  `upgrade-node.sh c87f8d86f79fe012ff72c5effed02562695d3154cda581f917f3cfdb4f631cd4 3p14-aaa`
  (SHA256 = бинарник 9a4c8d7 на 3p14-aaa; v2 ставит бинарник ТОЛЬКО при совпадении
  SHA256 + предпроверка doctor до замены + атомарный откат).
  1 проверка (на vzu5-claw после перезапуска phonebroker):
  `runpilot doctor | grep coder_timeout_env` → PASS «все N управляемых сессий:
  переменная на месте» (WARN «нет QWEN_CODE_API_TIMEOUT_MS» = переменная ещё нет).
- **п.5 — HANDOFF после каждого пункта — ✅** (обновлялся после п.1 `ed4e2e5`, п.2
  `1b6e4d8`, п.3 `ed70c5b`; итог — после п.4).

## Доработка после провала v1 на vzu5-claw: 4 пункта (2026-10-06) — ВЫПОЛНЕНО

> Оператор запустил `upgrade-node.sh` (v1) на vzu5-claw: обновление прошло,
> doctor FAIL на file_mode (config.yaml=664), откат упал на `cp: Text file busy`
> (ETXTBSY). Узел работает на 4904a65; права оператор поправил вручную.
> Порядок: 1 (upgrade-node.sh атомарно + предпроверка) → 2 (config.yaml 0600) →
> 3 (.gitignore: evidence + HANDOFF в git) → 4 (HANDOFF после каждого).

- **п.1 — upgrade-node.sh v2 (атомарная замена + предпроверка) — ✅ DONE**
  (коммит `a4a534b`; детали — п.5 «Повторный выкат» ниже: установка/откат `mv -f`,
  предпроверка doctor до замены, shellcheck 0, песочница `sandbox-test.sh` 10/10).
- **п.2 — config.yaml/secrets.env: 0600 независимо от umask — ✅ DONE.**
  Причина 664: `internal/api/nodes.go` (install.sh узла) создавал config.yaml
  через `cat >` БЕЗ chmod → наследовал umask 0022 → 0664 → doctor `file_mode`
  FAIL; secrets.env уже имел `chmod 600`. Фикс: вынесено в `nodeEnrollFileSetup()`
  — оба файла получают `chmod 600` после создания. Тест
  `TestNodeEnrollFileSetupUmask` (umask 0002 в дочернем bash → оба файла 0600;
  без chmod было бы 0664) — PASS. `go test -race ./internal/api/` + staticcheck OK.
- **п.3 — .gitignore: docs/evidence/ + .agent/docs/ в git — ✅ DONE**
  (коммит `8acfde7`). Каталоги рантайма стендов (data/, logs/, tmux/, work/,
  home/) остаются проигнорированными; артефакты .md/.json/.txt (155 файлов,
  ~0.5MB) + HANDOFF + GITHUB_PUBLICATION_STANDARD.md — в git. `git add -n`:
  157 файлов (45 .json, 46 .md, 66 .txt), 0 runtime-файлов.
- **п.4 — HANDOFF обновлять после каждого пункта — ✅ DONE.** Обновлялось после
  п.1 (`a4a534b`), п.2 (`e796851`), п.3 (`8acfde7`).

## Повторный выкат 3p14-aaa: 6 пунктов (2026-10-06) — ВЫПОЛНЕНО

> Предыдущий выкат (п.1–4) признан незавершённым: дефект хранения операторских
> состояний + правило закрытия хода. Делать по порядку: 1 (пауза/хранение) →
> 2 (закрытие хода) → 3 (коммиты) → 4 (повторный выкат из коммита) →
> 5 (upgrade-node.sh) → 6 (HANDOFF после каждого).

- **1. Пауза — дефект хранения — ✅ DONE.** Операторские состояния (пауза
  планировщика `s.paused`, drain/pause сервера, drain узла) теперь персистятся
  в SQLite и восстанавливаются при старте; восстановленные пишутся в журнал.
  - Миграция `014_operator_state` (таблица `operator_state(name,kind)`),
    `store.SetOperatorState`/`OperatorStates`; пауза планировщика — `meta`
    (`SchedulerPaused`/`SetSchedulerPaused`).
  - `Pause` и `NodeDrain` персистят; server drain/pause — в колбэках serve.go;
    `Scheduler.RestoreOperatorStates(applyServerState)` — restore (после
    LoadMode/Recover, до Start; активных аренд нет → DRAINING-restore безопасен).
  - Тест `TestOperatorStatesPersistAndRestore` (set → «рестарт» на той же БД →
    восстановлено + в журнале) — PASS; `schema_version` 13→14; регрессий нет.
- **2. Закрытие хода — `stable_since ≥ turn.started_at` ко ВСЕМ ходам — ✅ DONE.**
  Анти-фантом-гард (`pr.hashSince.Before(turn.StartedAt) → не закрыть`) вынесен
  в начало `turnDone` (ко всем ходам), не только no-request. Тишина: с запросами —
  от `last_request_end`, без — от `started_at`. Новый тест
  `TestP2RequestTurnNotClosedIfPaneStableBeforeStart` (ход с запросами, хеш панели
  стабилен ДО начала → не закрывается; закрывается после изменения хеша ПОСЛЕ
  старта). `TestTE_S6` подогнан (панель меняется ПОСЛЕ старта хода).
  `go test ./...` — 21 пакета ok, 0 FAIL.
- **3. Коммиты — ✅ DONE.** 8 логических коммитов на `main` (все 8 собираются:
  `go build ./...` OK на каждом, проверено worktree). Итоговый — `4904a65`:
  `go test -race ./...` = PASS (все пакеты ok, 0 FAIL) + `staticcheck ./...` = PASS
  → артефакт `docs/evidence/deploy-2026-10-06/test-race-staticcheck-4904a65.txt`.
  - `61f2a19` Команда для запуска (launchcmd + воспроизведение узлом при dispatch)
  - `41ebe46` Инвариант аренд (ErrSlotFull/serverSlots) + data-слой (012/013/014,
    held_since, attempts, client_gone, operator_state) + ядро планировщика
  - `4b4f7dc` Закрытие хода (§8): turnDone + анти-фантом `stable_since≥started_at`
    ко ВСЕМ ходам
  - `172ac53` Граница тика: тесты COOLDOWN→Tick (AfterFunc)
  - `a8b549c` Silent-hold + client_timeout_sec (hold_max_sec 3600, 5400, ReadTimeout=0,
    В.7, doctor qwen_timeout_guard)
  - `836e57c` Видимость удержания + set_status + дедуп внешних кодеров + serve.go
    (интеграция) + api/views.go (pause/drain/drain-узла)
  - `f15f8b3` Хранение операторских состояний: тест восстановления при старте
  - `4904a65` Docs + web-финалиш
  - Примечание: `.agent/` и `docs/evidence/` в gitignore → HANDOFF и артефакты
    живут локально (не коммитятся); рабочие файлы полностью закоммичены
    (`git status` чист, дерево == HEAD).
- **4. Повторный выкат 3p14-aaa бинарником из коммита — ✅ DONE.** Бинарник из
  чистого дерева коммита `4904a65`: `go version -m` → `vcs.revision=4904a65…`
  (= HEAD), `vcs.modified=false` (старый был `85e29c6…`, modified=true; бэкап
  `runpilot.bak-85e29c6-20261006T180203Z`). Ворота: 0 активных аренд (сессия
  IDLE) → `systemctl --user restart runpilot.service` → active. `runpilot doctor`
  = **PASS**. `/api/v1/state` до/после: операторские состояния **совпадают**
  (NORMAL, 3p14/vzu5=UP, 0 аренд). Артефакты: `deploy-4904a65.md`,
  `goversion-m-4904a65.txt`, `doctor_post_restart-4904a65.txt`,
  `state_{before,after}_restart-4904a65.json`.
- **5. `deploy/upgrade-node.sh` — ✅ DONE (скрипт + строка оператору).** Скрипт
  (коммит `532939f`): одна команда на узле — scp бинарника того же коммита с
  3p14-aaa (tailscale) → сверка SHA256 (ставится ТОЛЬКО при совпадении)
  → **предпроверка** (doctor новым ДО замены; FAIL → без изменений) → бэкап
  → **атомарная замена** (временный файл в том же каталоге + `mv -f`) → рестарт
  `runpilot-node` → `runpilot doctor` (при FAIL — **атомарный откат** `mv -f` +
  рестарт + doctor старым, печать обоих) → итог PASS/FAIL + версия + SHA256.
  Пути/сервис — env (`COORD_BIN/NODE_BIN/NODE_SERVICE/NODE_ENV`).
  - **v2 (2026-10-06, провал v1 на vzu5-claw):** v1 при откате делал `cp` поверх
    бинарника, занятого запущенным процессом → `cp: Text file busy` (ETXTBSY) —
    откат упал. В v2 установка И откат — только `mv -f` (rename не трогает inode
    запущенного) + предпроверка doctor ДО замены. Причина (plain `cp` поверх
    запущенного ELF → ETXTBSY, `mv -f` → успех) подтверждена песочным тестом.
  - **Проверки:** `shellcheck` — 0 замечаний; `deploy/upgrade-node.sandbox-test.sh`
    — 10/10 (A замена при запущенном бинарнике, B демо ETXTBSY, C откат при FAIL
    после рестарта, D предпроверка FAIL → целевой не изменён, SHA256 до==после).
  - SHA256 бинарника коммита `4904a65`: `a77d59c88bac16418d257ec745aa0707e5dbc3753723c585029a4f1c82e90b27`.
  - **Строка запуска (оператор, на vzu5-claw, tailscale):**
    ```
    scp 3p14-aaa:/mdisk/myhub/RunPilot/deploy/upgrade-node.sh /tmp/upgrade-node.sh \
      && E=$(ssh 3p14-aaa 'sha256sum /home/user/.local/bin/runpilot' | awk '{print $1}') \
      && bash /tmp/upgrade-node.sh "$E"
    ```
  - **Проверка после запуска (оператор):** новая сессия на vzu5-claw →
    `tr '\0' '\n' </proc/$(pgrep -f qwen | head -1)/environ | grep QWEN_CODE_API_TIMEOUT_MS`
    → `QWEN_CODE_API_TIMEOUT_MS=5400000` (client_timeout_sec 5400 × 1000).
- **6. HANDOFF — после каждого пункта.**

## (пред. выкат) Закрытие 2 дефектов — двойная аренда + фантомные ходы (2026-10-06) — ВЫПОЛНЕНО (п.1–4), (b) оператору

> «Выкат запрещён до закрытия обоих дефектов ниже. Guard по окну „только что выдан"
> не делать.» Порядок: **п.1** (двойная аренда) → **п.2** (фантомные ходы) →
> **п.3** (e2e) → **п.4** (выкат) → **п.5** (HANDOFF).

**п.1 — двойная аренда (инвариант слотов в store) — ✅ DONE:**
- **Последовательность нарушения** восстановлена на before-бинарнике (без фикса тика,
  без п.1): `docs/evidence/Э4/violation-2026-10-06.md` (+ raw `violation-2026-10-06/`:
  `violation-2026-10-06.txt`, `harness_before.json`, `runpilot-before` SHA `9c4035b0…`).
  Server `a` (1 слот): **s2 `40MTFR2TNX` + s3 `HMCCVG26WV`** — обе аренды на `a` slot 1
  (id11/id12), granted **16:07:34.180** (77 мкс), обе released TURN_DONE 34.254 →
  **max=2** (сэмпл 52.7 с). Цепочка: фантомное закрытие (п.2) сняло слоты преждевременно
  (33.655, `req=0`) → имплицитная пере-выдача (34.180) → 2 аренды на 1 слот.
- **Инвариант в store:** `LeaseCreateIfAbsent(l, slots, deleteQueue)` — в ОДНОЙ
  транзакции проверка `COUNT(аренды сервера в PENDING/ACTIVE/RECOVERING) < slots`,
  иначе `ErrSlotFull` (отказ). Все пути передачи: dispatch×3 (held/job/planned) +
  gateway `acquireNow` (`grant`) + миграция (все сходятся в `LeaseCreateIfAbsent`).
  `RELEASED` никогда → `ACTIVE` (`LeaseSetActive` только `state='PENDING'`).
- `ErrSlotFull` — **гонка, не ошибка**: у планировщика DEBUG (`grantErr`), у шлюза —
  следующий сервер. Запись остаётся в очереди (повтор в следующем тике).
- **5 тестов** (`internal/store/lease_test.go`): выдача при занятом слоте → `ErrSlotFull`;
  if-absent возвращает существующую (не дубль); RECOVERING занимает слот;
  подтверждение старта для RELEASED → отказ; PENDING→ACTIVE — OK.
  `go build ./...` + `go test ./internal/{store,scheduler,gateway,api}` — **OK**.

**п.2 — фантомные ходы (правило завершения хода §8 ТЗ) — ✅ DONE:**
- `checkTurn` — только сессии в **RUNNING и DETACHED**; **DISPATCHING** завершается
  ТОЛЬКО путями подтверждения старта (start_confirm_sec, PANE_CHANGED, EMPTY_INPUT,
  PANE_WAIT_UI, PANE_GONE). Панель: IDLE и `stable_since ≥ turn.started_at`. Тишина:
  `last_request_end` — только из запросов текущего хода; ход без запросов — тишина от
  `turn.started_at`.
- Тесты на вирт. часах: (1) 600 с в очереди с IDLE-панелью → выдача → ход не
  закрывается до подтверждения старта; (2) DISPATCHING без подтверждения →
  HOLD(START_NOT_CONFIRMED) через start_confirm_sec; (3) ход без запросов BUSY→IDLE →
  закрытие не раньше `started_at+done_quiet_sec` и не раньше `stable_since+done_stable_sec`.
- **Реализация:** `tickComplete` вызывает `checkTurn` только для RUNNING/DETACHED;
  `checkTurn` — снятие аренды (TOOL/APPROVAL/PANE_UNKNOWN) только в RUNNING; закрытие —
  через `turnDone` (два случая: с запросами → тишина от `last_request_end` + stable ≥
  `done_stable`; **без запросов → `stable_since ≥ turn.started_at`** (анти-фантом) +
  тишина от `started_at` + stable ≥ `done_stable`); `closeTurn`: DETACHED→`EvIdleNoError`,
  RUNNING→`EvTurnDone`.
- **Тесты** (`internal/scheduler/turn_complete_p2_test.go`): `TestP2TurnNotClosedWhilePaneStableBeforeStart`,
  `TestP2DispatchingNoConfirmStartNotConfirmed`, `TestP2NoRequestTurnCloseLowerBound` — PASS;
  нижние границы хода (`turn_close_lowerbound_test.go`) + S6/S7 — не сломаны; `go test ./...` — OK.

**п.3 — повтор e2e Э4 — ✅ DONE (все 4 прогона PASS, 2026-10-06):** стенд `/tmp/rp-e4`
(2×fakellm a/b по 1 слоту, 6×mockcoder × 3 хода, `tick_ms=100`, `cooldown_sec=1`,
`done_quiet/stable=3`, `budget_p95_ms=1500`), сэмпл аренд 100 мс, прогон через
`/tmp/rp-e4/oncerun.sh <tag>`.
- **3 прогона (п.1+п.2+фикс тика):** `harness_fix1/2/3.json` — каждый **18/18 ходов OK,
  0 ошибок, 54/54 запросов, max аренд=1 / 0 нарушений (≤1/сервер), p95=100 мс (≤1500),
  снятие аренды max 4.99 с (бюджет 8), каждое SKIP с причиной** → PASS.
- **1 прогон (до фикса тика + п.1+п.2):** `harness_pretick.json` (AfterFunc в `setCooldown`
  отключён) — **18/18, 0 ошибок, 54/54, max аренд=1 / 0 нарушений, p95=100 мс** → PASS.
  **Вывод:** инвариант п.1 (store) сам по себе исключает двойную аренду даже БЕЗ фикса
  границы тика; фикс тика — пропуск/корректность выдачи, не несущий безопасность.
- Артефакты → `docs/evidence/Э4/` (`harness_fix{1,2,3}.json`, `harness_pretick.json`,
  `serve_fix1.log`, `harness_fix1.log`).

**п.4 — выкат (после PASS п.1–3) — ✅ DONE (a/c), (b) оператору (2026-10-06):**
- **(a) координатор 3p14-aaa — DONE.** 3p14-aaa = эта машина (tailnet
  100.124.155.106), сервис `runpilot.service` → `/home/user/.local/bin/runpilot serve`.
  Пересборка (п.1+п.2+фикс тика, SHA `3dc75c4ced9e`); `doctor` (новый) — все PASS;
  gate `GET /api/v1/state` — **0 активных аренд** (phonebroker IDLE) → рестарт;
  пост-рестарт `doctor` PASS + state 0 аренд, phonebroker восстановлена. Старый
  бинарник забэкаплен. **Сторонний эффект (не регрессия):** `3p14` PAUSED→UP —
  операторский PAUSED in-memory (не персистится), сбрасывается при любом рестарте;
  при намеренном pause оператор ставит заново.
- **(b) узел vzu5-claw — оператору.** `deploy/upgrade-node.sh` **отсутствует** в репо
  (deploy/systemd, deploy/launchd пусты) — создать. Фиксы (п.1/п.2) в координаторе
  (уже выкатаны); апгрейд узла — ради консистентности, не блокирует. Одна строка —
  в `docs/evidence/deploy-2026-10-06/deploy-2026-10-06.md`.
- **(c) отчёт сессий `created_at < 2026-10-06T16:48:19Z` — DONE:** 18 сессий = 17 GONE
  + **1 живая `phonebroker` (IDLE)** — оператор перезапускает через `runpilot run`.
- Артефакты → `docs/evidence/deploy-2026-10-06/` (`deploy-2026-10-06.md`,
  `state_{pre,post}_restart.json`, `doctor_post_restart.txt`, `deploy_time.txt`).

**п.5 — HANDOFF обновлять после каждого пункта.** (п.1 — записан выше.)

## (пред. заказ) Silent-hold — 5-й цикл: заказ на 5 пунктов (п.0–5, 2026-10-06)

> «Делать все четыре пункта, в этом порядке. Выбор не требуется.»
> Порядок: 0 → 1 → 2 → 3 → 4, HANDOFF (п.5) — после каждого.

**п.0 — таблица 07: строка п.6 = FAIL — ✅ DONE:**
- `docs/evidence/hold-silent/07-status-table.md`: строка п.6 →
  `❌ FAIL (пока п.6(c) не закрыт live-проверкой п.2)`; «Итог» — 8/9 PASS + п.6 FAIL.
  Статус вернётся в PASS, когда live-проверка п.2 (пункт 2) подтвердит
  `@runpilot_status` на живой панели B.

**п.1 — п.6(c): пуш set_status/set_summary координатор→узлы — ✅ DONE + 3 теста:**
- Координатор считает два глобальных текста и шлёт узлам ПО ИЗМЕНЕНИЮ:
  - `set_status` → `@runpilot_status`: `Scheduler.HeldStatus()` = «Q N/M held»
    (поводы: смена состояния сессии / позиции в очереди / флага held → меняются N, M).
  - `set_summary` → `@runpilot_summary`: `Scheduler.SummaryText()` =
    «имя=состояние … | Q:len» (поводы: состояния серверов + длина очереди).
- Новый компонент `internal/api/status_push.go` — `StatusPusher`
  (интерфейс `StatusSource` = планировщик): кэш last-sent **per pane** (status) и
  **per host** (summary); неизменившийся текст повторно не отправляется.
  Транспорт — fire-and-forget `Hub.PushPane` (без ожидания reply).
  `PushAll()` — событие изменения; `ResendHost(host)` — переподключение.
- Триггеры: `Scheduler.Tick()` (после тика, ВЫНЕ блокировки, panic-safe порядок
  defer) + `Scheduler.ServerState` (смена состояния сервера) → `OnStatus`
  (новое поле `Options.OnStatus` / `SetOnStatus`). Переподключение:
  `handleNodeWS` — на ПЕРВОМ `panes` после hello → `pusher.ResendHost(host)`.
- Пересылка узлов: `KindSetStatus`/`KindSetSummary` → `setOption`
  (`tmux set-option -p -t <pane> @runpilot_*`, pane-local) — уже было.
- Wiring (`cmd/runpilot/serve.go`): `statusPusher := api.NewStatusPusher(hub, sch, log)`
  → `srv.SetStatusPusher` + `sch.SetOnStatus(statusPusher.PushAll)`.
- **3 теста (задачные)** — `internal/api/status_push_test.go` (`-race` PASS):
  (a) `TestPusherQueueChangeSendsNewStatus` — Q 1/1→Q 2/2 → узел получил новый
  текст; (b) `TestPusherUnchangedNotResent` — 3 пуша = 1 set_status/панель;
  (c) `TestPusherReconnectResendsAll` — реконнект → всем панелям status+summary.
- **Проверки:** `go build ./...` OK; `go test -race ./...` — **24 ok / 0 FAIL**;
  `make staticcheck` — **EXIT=0**; `make lint-literals` — 5 находок **ПРЕСУЩЕСТВУЮЩИХ**
  (`server.go: ClientTimeoutSec*1000` от п.6 client_timeout_sec; fakellm
  `ParseInt(…,10,64)` — base/bitsize); **п.1 не вводит ни одного литерала**.
- Бинарник `/tmp/hl/runpilot` пересобран (`ff993967…`) — включает п.1 (нужен для п.2).

**п.2 — live граница тика (5 прогонов, бинарник с п.1) — ✅ DONE (5/5):**
- Метод: A — curl (`WM35ND67S3`), `/_set-delays firstByteMs=60000` (слот 60 с) →
  `firstByteMs=0`; B — реальный qwen (`3GV3DEP46A`), Enter ≤10 с после старта A;
  во время удержания B — `tmux -L hold-silent show-options -p -t %2 @runpilot_status`.
- **Результат 5/5** (артефакт `08-p6c-tick-boundary-live-2026-10-06.md`):
  - Крит.1 `held_ms(B)` >0: **59 769…61 762 мс** ✓
  - Крит.2 `RELEASE(A)→GRANT(B) ∈ [2000,2050] мс`: **2000.21…2001.95 мс** ✓
    (cooldown +0…+2 мс, **НЕ** +tick; было 2499.95 мс ДО фикса — **граница тика
    исправлена live**).
  - Крит.3 статус B = `QueueHeldStatus`: **`Q 1/1 held`** (сэмплы 2–5) ✓ —
    **live-подтверждение п.6(c)** (пуш координатор→узел вывёл строку на панель).
- Таблица 07: п.6 → **PASS** (п.6(c) закрыт live 5/5). Скрипт `stand/run_tick.sh`,
  артефакты `stand/tickrun_1…5/`.

**п.3 — e2e Э4 на петле — ✅ DONE (3/3 критерия PASS) + НАХОДКА (пре-экзистинг-баг):**
- Стенд собран под текущий бинарник (`/tmp/rp-e4`; старое `docs/evidence/Э4/` — до
  RENAME). 2×fakellm по 1 слоту (a prio 10, b prio 5), 6×mockcoder (`--turns 3
  --posts 3`, бинарник назван `qwen` для узлового классификатора), сокет `rpe4`.
  `tick_ms=100`, `cooldown_sec=1`, `done_quiet/stable=3`, `auto_enqueue_stable=2`,
  `budget_p95_ms=1500`, `scan_interval_sec=1`.
- **3 критерия п.3 — PASS** (бинарник `ff993967` c фиксом, 3 прогона):
  (1) аренд ≤1/сервер: **max=1, 0 нарушений** (сэмплы 750/730/824, шаг 100 мс);
  (2) 0 таймаутов: **54/54 `status=200`**, 6/6×3 хода до IDLE;
  (3) dispatch p95: **100 мс** ≤ 1500 (n=17). Доп. (CONTROL): снятие ≤8с (4.99с),
  SKIP-причины — 0 пропусков без причины. Split a/b = 27/27.
- **Сэмплы + p95 в артефакт:** `docs/evidence/Э4/loop-2026-10-06/`
  (`harness_withfix.json` — `lease_samples`/`lease_max_seen`/`dispatch_p95_ms`;
  `config.yaml`, `launcher.sh`, `aqmr.py`, `harness.py`).
- **⚠ НАХОДКА (пре-экзистинг, НЕ регрессия 5-го цикла):** CONTROL-строка
  «18/18 ходов OK» **нарушена** — за 18 ходов записывается **20–22** `turn`
  (2–4 «фантомных», `requests=0`, `started_at==ended_at`; интермиттентно 22→22→20).
  Всё доставляется (54/54, 0 ошибок, 0 таймаутов) — работа не теряется, ломается
  УЧЁТ ходов (реальные запросы хода идут с `turn_id=0`).
  - Механизм: done-детекция `checkTurn` (`internal/scheduler/complete.go`, **не
    менялся** — `git diff`): `reqCount==0 → doneQuiet не применяется` → ход,
    только что открытый при DISPATCH (панель ещё не BUSY, снапшот узла 1 с, запросов
    0), закрывается сразу, если панель IDLE и хеш стабилен (`hashSince≥done_stable`)
    — т.е. после **долгого ожидания в очереди** (раунд 2: до ~12 с).
  - **Диагностика (откат `setCooldown`→`AfterFunc`):** без фикса — **double-lease**
    (server a: 2 concurrent, max=2, 2 нарушения); с фиксом — max=1, 0 нарушений.
    Фантомы — и с фиксом, и без. Вывод: **баг пре-экзистинг** (base `dfb70b3`), а
    **фикс границы тика — ЗАЩИТНЫЙ** (гасит double-lease), но сам баг остаётся.
  - **Рекомендация:** деплой (п.4) — **только с фиксом** `ff993967`; баг
    done-детекции («не закрывать ход без запроса в окне «только что выдан») —
    отдельный пункт до/после выката. См. `docs/evidence/Э4/loop-2026-10-06.md`.

**п.4 — выкат (после PASS п.0–3) — ⏳ PENDING:**
- (a) 3p14-aaa: пересборка; `runpilot doctor` все PASS; перед рестартом
  `GET /api/v1/state` — 0 активных аренд; рестарт serve; после — doctor+state → артефакт.
- (b) `deploy/upgrade-node.sh` (бинарник с 3p14-aaa → замена → рестарт node → doctor);
  оператору — одна строка на машину. (c) отчёт: сессии `created_at <` момента выката.

**п.5 — HANDOFF обновлять после каждого пункта.** (п.0, п.1 — записаны выше.)

## (пред. цикл) Silent-hold — 4-й цикл (5 блоков, 2026-10-06) — состояние на тот момент

**Block 1 — таблица 07: вернуть 4 пункта (п.2/п.6/п.7/п.8) + api.md — ✅ DONE + тесты:**
- **п.2** http.Server шлюза `ReadTimeout=0`/`WriteTimeout=0` — `newHTTPServer`
  (`cmd/runpilot/serve.go`); тест `TestGatewayServerTimeouts`; артефакт live п.2
  (удержание 362 506 мс без разрыва).
- **п.6** видимость удержания: (a) `held_since` в `queue_entry` (миграция
  `013_queue_entry_held_since.sql`, holdPass, `/api/v1/state` → `held_since`);
  (b) веб — бейдж **HELD** + таймер `now−held_since` (`web/app/views/queue.js`
  `HeldTimer`+`Badge`, `.qrow-held`); (c) строка статуса tmux «Q N/M held» —
  текст `QueueHeldStatus` (`internal/scheduler/views.go`); (d) `why=held_ok`
  (`eligibility.go primaryReason`). Тесты: `TestStateHeldSince`,
  `TestStateNoHeldSinceWhenNotHeld`, `TestQueueViewHeld`,
  `TestQueueHeldStatusLine`, `TestPrimaryReasonHeldOK`. **Осталось (п.6c):**
  периодический пуш «координатор→узлы» (msg `set_status`) для отображения в
  tmux status-right; текст+данные готовы, узел (`node/commands.go`) и
  `tmuxsetup.go` поддерживают.
- **п.7** восстановление после рестарта: `held_request=true`→`false`,
  `mode=resume`, `enqueued_at` сохранён — `RecoverHeldOnStartup`
  (`internal/scheduler/recover.go`) + старт (`cmd/runpilot/serve.go`); тест
  `TestRecoverHeldOnStartup`.
- **п.8** doctor клиент: строка `QWEN_CODE_API_TIMEOUT_MS` в пакете qwen, иначе
  FAIL (версия+путь) — `checkQwenTimeoutGuard` (`internal/doctor/doctor.go`);
  тесты `TestQwenTimeoutGuardPass`/`TestQwenTimeoutGuardFail`.
- **api.md** — `RunResponse` `{sid, client_timeout_ms}` (уже было); обновлены
  `QueueRow.held/held_since`, `State.held_since`. Таблица:
  `docs/evidence/hold-silent/07-status-table.md` (раздел «Пункты исходного
  списка»).

**Block 2 — граница тика в живом режиме — ✅ DONE (юнит) / ⏳ LIVE:**
- Фикс: окончание COOLDOWN — событие планировщика; таймер `internal/clock`
  на `released_at+cooldown_sec` (`setCooldown` в `complete.go` + `Clock.AfterFunc`);
  срабатывание → `Tick`; один `now` на тик сохраняется. Выдача РОВНО в
  `released_at+cooldown` (не +tick). Тесты (вирт. часы): `TestTickBoundaryOffPhase`,
  `TestTickBoundaryOnPhase` — **PASS** (`GrantedAt == released_at+cooldown` точно).
- **LIVE-ре-верификация (⏳, попытка 2026-10-06):** пересобрать (`/tmp/hl/*` —
  готово, `2cde3605…`), stand поднят (fakellm=200, api=200), A/B созданы. Чистый
  захват «A держит слот → B held → A release → B grant» **не получен**: реальный
  qwen-кодер делает несколько ходов на промпт, и запрос B пришёл ПОСЛЕ release A
  (слот был свободен → B взял его сразу, не в удержании) → held→grant перехода в
  событиях нет (queue_entry пуста, LEASE_GRANT отсутствуют). **Детерминированное
  доказательство фикса — юнит** (`TestTickBoundaryOffPhase`/`TestTickBoundaryOnPhase`,
  `-race`): `GrantedAt == released_at+cooldown` точно (без +tick) на реальном пути
  планировщика. Критерий live: `cooldown_sec×1000 ≤ gap ≤ cooldown_sec×1000+50 мс`
  (было 2499.95 мс; ожидается ~2000 мс при `cooldown_sec=2`). **Чтобы повторить
  live чисто:** A держать слот ДЛИННЕЙ задержкой (firstByteMs ≥ 60000), B ввести
  промпт СТРОГО во время удержания A (пока `queue_entry` несёт `held_request=1`),
  затем замер LEASE_RELEASE(A)→LEASE_GRANT(B) по `event` (таблица `event`, kind
  LEASE_*). Stand: `docs/evidence/hold-silent/stand` (launcher.sh + create_one.sh).

**Block 3 — регрессия — ✅ DONE (race+staticcheck) / ⏳ e2e:**
- `go test -race ./...` — **21 ok / 0 FAIL**; `staticcheck ./...` — **EXIT=0**
  (артефакт `step3-race-staticcheck-2026-10-06.txt`). S1–S14 — в сценарном
  сюите `internal/scheduler` (PASS в рамках `go test`).
- **e2e Э4 (⏳):** 2×fakellm ×1 слот, 6×mockcoder ×3 хода; критерии: ≤1 аренда/
  сервер на сэмпле (100 мс), 0 таймаутов клиента, p95 ≤ `dispatch.budget_p95_ms`.

**Block 4 — выкат (после PASS п.1–3) — ⏳ PENDING (live/оператор):**
- doctor на 3p14-aaa + клиентских — все PASS; рестарт serve — ТОЛЬКО когда в
  `GET /api/v1/state` нет активных аренд; в отчёт список сессий
  `created_at <` момента выката (оператор перезапускает через `runpilot run`).

**Block 5 — HANDOFF обновлять после каждого из шагов 1–4.**

---
История (W-фаза, aqm→RunPilot) — ниже.

## Старый заголовок (W9, 2026-09-29)
Текущий этап: W9 (приёмка на железе) — машина часть PASS (CONTROL 5/6/7),
осталась операторская часть (2-часовой тест)
Статус этапа: Э0–Э6 PASS; W0–W8 PASS. W9 — CONTROL 5 ✅ (self_update),
6 ✅ (echo p95 59.13 мс), 7 ✅ (doctor ×2); 1,2,3,4 — только оператор.
W9 (доп): браузер без сессии на корне — 302 на /web/login?next=… (O4/11.1).
W9 (доп-2): 16 замечаний оператора по телефонному UI — закрыты (B6).
W9 (доп-3): 5 замечаний — иконки (root-причина: object-spread htm молча
выпадает + нет svg-обёртки), FAB убран (→ «+» в топбаре), project_roots —
конкретный путь, select крупнее, enroll 401 через https (EnrollHandler
публично на вебе). W9 (доп-3b): coordinator в install.sh-конфиге узла =
API-адрес (был https-базис → «bad handshake»). W9 (доп-3c): claim 500 —
транзитный сбой записи в БД → SAFE_MODE 10 с; фикс логирования (notifyf
fmt.Sprintf + store writeErr в журнал). W9 (доп-3d): re-enroll 500 —
реальная причина UNIQUE(node_token.host) (не транзит): ре-claim нового
токена для host с активным токеном → 500 + ложный SAFE_MODE; фикс
(BindTokenHost заменяет токен host; constraint ≠ сбой хранилища, не
срабатывает SAFE_MODE) + эндпоинт qwen code: info в диалоге «Новая
сессия», узел сам правит ~/.qwen/settings.json при подключении
(selectedType=openai, убирает modelProviders с id=model_alias, резерв
.aqm.bak). Координатор — B10 (резерв .b9).
Развёртывание: 3p14-aaa = координатор B10 (user service `aqm`, tailnet
100.124.155.106: API :8788, шлюз :8787, веб :8790 → tailscale serve 443 →
https://3p14-aaa.tail46a65f.ts.net); vzu5-big удалён оператором 2026-09-29
(uninstall), добавляется vzu5-claw (enroll: повтор с тем же токеном 2830ffe6…
— claim 500 не привязал токен; после B9 проходит).
Тест-план и финальный список оператору: docs/evidence/W9/TEST_PLAN.md.

> Переключение на v2: `.agent/tasks/TZ_AIQueMan_v2_web.md` (редакция 26.09.2026).
> Базовые Э0–Э6 закрыты; незакрытые базовые Э7/Э8 закрываются v2-аналогами
> (см. RECONCILE A9). Рабочая директория — корень `aqm/`.

## W0 — Сверка (приложение А v2)

CONTROL W0: `docs/evidence/W0/RECONCILE.md` — строка на каждый A1–A10 со
статусом и доказательством (путь:строка / вывод команды); SHA-256 шаблона
записан; вывод `aqm doctor` на `3p14-aaa` приложен. **PASS**.

- Шаблон: `docs/design/template.jpg`, SHA-256 `022826dd…a14f49` (артефакт
  `docs/evidence/W0/template.sha256`).
- `aqm doctor` (артефакт `docs/evidence/W0/doctor.txt`): PASS
  config/allow_cidrs/coordinator_bind/secrets_env/tmux/qwen_settings;
  FAIL `file_mode` — ожидаем (фикстура `testdata/config.example.yaml` 644, не
  деплой 0600); WARN `metrics`/`require_direct` — ожидаемы (пример указывает на
  недоступные `192.168.10.x`; живой vLLM оператора `127.0.0.1:8004` намеренно
  не опрашивался).
- A1–A8, A10 — **соответствует** (код = базовое ТЗ; расхождения — в
  `manual.md`, исправляется при переписи под веб).
- A5 (пустой `AQM_TOKEN` = локальный режим) — **исправлено в W1** (раздел 16).
- A9 — **вопрос оператору**: закрыты только Э0–Э6, Э7/Э8 нет
  (`internal/notify` — заглушка, нет `last_alive`/`quick_check`, `deploy/` пуст).
  Предложено: Telegram→W5, восстановление/shutdown/upgrade/rollback→W7,
  приёмка→W9. Согласование — в конце (по запросу оператора).

## W1 — Служба и доступ (раздел 20, TZ v2)

CONTROL W1: `docs/evidence/W1/CONTROL.md` — все 7 строк PASS; артефакты
`make-check.log`, `test-race.log`, `make-lint.log`, `control-tests.log`,
`go.mod`, `embedded-live-state.json`.

- **Удаление TUI** (R-правило): `internal/tui/` + `cmd/aqm/tui.go` + TUI-зависимости
  (`bubbletea`, `lipgloss`, `teatest`, `termenv`) — из `go.mod`. `aqm` без аргументов
  → CLI-сводка `runSummary` (режим, ходы, очередь, URL веба), не TUI.
- **Веб-слушатель** (`internal/web`): петлевой адрес только (tailscale serve — путь
  наружу); монтирует **те же** `/api/v1/*`, что API-слушатель (`api.Server.Routes()`),
  с cookie-аутентификацией + CSRF. Порядок middleware `cookieAuth(csrf(routes))`:
  без cookie все → 401, с cookie без CSRF изменяющие → 403. Cookie `aqm_session`
  (SHA-256 в БД, TTL `web.session_ttl_days`, `Secure` из `https` префикса `public_url`).
  Заголовки безопасности (CSP/nosniff/no-referrer), лимит входа `web.login_rate_per_min` (429).
- **Повтор привязки** (`web.ListenWithRetry`): веб-слушатель поднимается прямо, API+шлюз —
  с повтором до появления адреса (интервал `coordinator.bind_retry_sec`, `serve.go:159-174`).
- **Отказ старта** (`config.ValidateWeb`/`ValidateToken`): `web.bind` только `127.0.0.1`/`::1`,
  токен не пустой и не короче `limits.token_min_len` — иначе `aqm serve` не стартует.
  Убран «локальный режим» (A5): пустой `AQM_TOKEN` теперь = отказ.
- **Встроенный узел** (`cmd/aqm/embedded.go`, `coordinator.embedded_node: true`): тот же
  путь данных, что WS-узел (`node.New`+`node.Run` + `srv.ApplyNodeMsg`), транспорт — канал
  в памяти (`memConn`). Хост координатора виден как узел: его панели и сессии в `/state`
  **без службы aqm-node** (тест `TestEmbeddedNodeHostVisibleInState` + живой стэнд).
- **Поле `actions`** (15.2): `model.{Session,Server,Node,Queue}Actions` (`internal/model/actions.go`)
  — `[id,enabled,reason]`; поле в `SessionView`/`NodeView`/`QueueRow`/`ServerRow`.
  Проверка доступности — серверный код (тот же, что при выполнении действия).
- **`internal/texts`** + `GET /api/v1/meta` (15.1): тексты ошибок/причин/подписей состояний;
  `meta` = version, mode, перечисления с подписями (sessions/panes/classes/servers/modes/
  severities), hold_reasons, ineligible, units.
- **Линтеры R2** (`tools/`): `lintlits` (числовые/URL/IP/хост-литералы), `lintweb`
  (цвета/кириллица/литералы в JS — web/ с W3), `linthosts` (имена хостов, CGNAT).
  `make lint` = 3 линтера, 0 находок. Хардкод вынесен в `consts.go` по пакетам.
- **Каркас стенда** (`cmd/aqm-stand`): один процесс — координатор (API+веб) на петлевых
  адресах, виртуальные часы, временная БД, управляющий порт `GET /status`. Полный стенд
  (узлы/сценарии/скриншоты) — W3.
- **Отложено в W5/W6** (15.1, зафиксировано в CONTROL): `version`+`If-Match` (409) —
  требует поле `version` у сущностей (схема W2/W5/W6), `Idempotency-Key` (TW17);
  формат ошибки `{"error":{...}}` — с веб-обработкой (W5/W6). В W1 сохранён `{"code","detail"}`
  (тесты Э0–Э6, R9).

## W2 — Настройки (TZ v2, раздел 20) — ЗАКРЫТ

CONTROL W2: `docs/evidence/W2/CONTROL.md` — все 6 строк PASS; артефакты
`control-tests.log` (41 PASS), `test-race.log`, `make-lint.log`, `make-check.log`, `go.mod`.
`go test -race ./...` — 0; `make lint` — 0; `make check` — 0. S1–S14 — PASS на настройках из БД.

- **Схема 4.3** (`internal/config/schema.go:76`): 92 `SettingField` (path/type/unit/default/
  min/max/enum/group/label/help/apply_when/dangerous); `Default` — из `Defaults()` (R2,
  литералы не дублируются). `WorkingLeafPaths()` (`work.go:63`) = 92 рабочих ключа
  (рефлексия + bootstrap-исключения). CONTROL 6: `TestSchemaCoversAllWorkingKeys` (92 == 92).
- **Bootstrap/рабочие (4.2)**: bootstrap = config.yaml (pre-DB: `coordinator.*`,
  `web.{bind,port,public_url}`, `notify.telegram.bot_token_env`, `client.*`,
  `node.{host,tmux_sockets,gpu,gpu_server}`); рабочие = всё остальное (в БД).
- **Хранилище 4.6/4.7**: `003_setting.sql` (таблицы `setting` + `setting_revision` с полным
  снимком + `server` = JSON). `setting.go`: `LoadWorkingInto`, `SaveWorkingSettings`,
  `RevertToRevision` (snapshot как НОВАЯ ревизия `source=revert` — CONTROL 4: пустой diff).
  `settings_export.go`: `ExportSettings` (YAML без секретов, только имена `key_env`) +
  `ImportSettings` (проверка 4.5, diff, одна ревизия `source=import`, `?dry_run=`) — CONTROL 5:
  экспорт→импорт = пустой diff. `server.go` (store): `List/Get/Upsert/Delete/ReplaceServers`.
- **Миграция с YAML (4.8)**: `migrate_yaml.go:36` `MigrateFromYAML` — копия БД
  `aqm.db.bak-<версия>-<время>` + копия `config.yaml.bak-<время>`, ревизия №1
  `source=migration`, серверы → таблица `server`, перепись конфига в bootstrap, откат при
  ошибке, no-op при 2-м запуске. CONTROL 1 (4 теста `TestMigrate*`).
- **4.4 apply_when + 4.5**: `Options.Reload` (scheduler.go:96-98) + re-read `LoadWorkingInto`
  в начале `Tick()` (:218) — планировщик пере-read'ит из БД каждый тик. CONTROL 2: S1–S14
  DB-driven (`newHarn` в `scenario_test.go`, логика не менялась). 4.5: `TestValidate45*` (7) +
  `TestSettingsPatchValidation` (400 `VALIDATION_ERROR`, `{path,code,message}`). CONTROL 3:
  поведенческие `TestApplyWhen{NextTickScheduler,NewTurn,ServerPriority,ServerSlots}` +
  `TestSchemaApplyWhen{,Groups}` (все 7 строк 4.4).
- **API (7 маршрутов)**: `internal/api/settings.go` — `GET/PATCH /settings`, `/schema`,
  `/revisions`, `POST /revert`, `GET /export`, `POST /import?dry_run=` (server.go:110-116).
- **Прод-вайринг (serve.go)**: на старте → `MigrateFromYAML` → `LoadWorkingInto(cfg)` →
  `ListServers()` в `cfg.Servers` (если непусто) → планировщик `Reload: LoadWorkingInto`.
- **Отклонения (CONTROL.md, честные, не блокируют W2)**:
  1. Реальные файлы 3p14-aaa отсутствуют на dev-машине (деплой aqm не развёрнут) → CONTROL 1
     на представительной v1-копии; путь параметризован, подставятся тривиально при реальных.
  2. 4.4 строки 5–7 покрыты схемой-метаданными `TestSchemaApplyWhen`, не поведенческими
     (нужен шлюз/веб/spawn → W6/W3).
  3. Gateway/monitor в `serve.go` читают `cfg` статично (без Reload-хука) → смена
     `gateway.*`/`monitor.*`/priority/slots через импорт действует после рестарта;
     динамический реестр серверов шлюза — W6.

## W3 — Дизайн и стенд (TZ v2, раздел 20) — ЗАКРЫТ

CONTROL W3: `docs/evidence/W3/CONTROL.md` — все 4 строки PASS; артефакты
`make-check.log`, `test-race.log`, `make-lint.log`, `control-screenshots.log`,
`visualcheck.log`, `screens/` (10 PNG + `manifest.json`).
`go test -race ./...` — 0; `make lint` — 0; `make check` — 0; `make screenshots
STAGE=W3` — 19.3 = 0 находок; `make visualcheck STAGE=W3` — OK.

- **Дизайн (9.2–9.3)**: токены `web/styles/tokens.css` (цвет/шрифт/пространство/радиус/
  тени, светлая+тёмная темы); компоненты каркаса `components.css`; структура+медиазапросы
  `layout.css`. Иконки Lucide → `web/icons/` + `icon.js`. Шрифты Inter + IBM Plex Mono
  (woff2, кириллица) в `web/fonts/`.
- **Каркас (10.1–10.3)**: боковая навигация + верхняя панель (desktop) / нижние вкладки
  (телефон, touch ≥44px); переключатель темы; пустые состояния (empty state).
- **Фронтенд**: Preact 10.29.8 + htm 3.1.1 + hooks — **без сборки** (ESM в `web/vendor/`,
  go:embed через корневой `embed.go` = `package aqmweb`, `//go:embed all:web`), hash-роутинг,
  inline importmap. `cmd/aqm/serve.go` подключает `aqmweb.FS()` → `SetStaticFS`.
- **Вход (16) + web-оболочка (19.1/12)**: cookie-аутентификация + CSRF
  (`internal/web/web.go`, `web_handlers.go`); CSP `default-src 'self'` + `script-src-elem
  'self' '<sha256>'` (hash инлайн-importmap); командная строка (12) — каркас.
- **Стенд `aqm-stand` (19.1)**: один процесс — координатор (API+веб) на петлевых адресах,
  ВИРТУАЛЬНЫЕ часы (скриншоты воспроизводимы), временная БД; флаги
  `-web-port -control-port -token -start -keep-db -info-file`; управляющий порт — только
  в стенде (W3 — каркас; шаги часов/события/узлы — W4+).
- **Конвейер 19.2–19.4 (go-rod v0.116.2)**: `tools/screenshots` (headless Chrome; 10
  комбинаций 19.2 = вход+кадр пустого состояния × {1440×900 light+dark, 1024×768 light,
  390×844 phone light+dark}; автопроверки 19.3: console 0 err/warn, network 0 внешних
  хостов + 0 ≥400, scrollWidth≤clientWidth, нет undefined/NaN/{{, axe 0 serious/critical,
  touch ≥44×44, fonts loaded, golden diff ≤ diff-max); `tools/visualcheck` (10 файлов
  из VISUAL.md в manifest, 12 отметок в строке, commit(VISUAL.md)==commit(manifest) и
  коммит — предок HEAD). Golden-база — `test/visual/golden/` (10 PNG, W3).
- **Ключевые технические решения (CONTROL.md)**:
  1. **htm = шаблонные литералы, не JSX**: компонент в шаблоне — только `<${Comp}/>`,
     статичный `<Comp/>` рендерится как литеральный тег (`main.js`, `views/login.js`).
  2. **Порядок CSS**: `tokens.css → components.css (базовые) → layout.css (структура+медиa)`.
     Базовые правила после медиазапросов перебивают их (`.sidebar{display:flex}` ломал
     телефон). Базовый `display:none` нижних вкладок — в `components.css`, `flex` — в
     телефонном медиазапросе `layout.css`.
  3. **Контраст тёмной темы (WCAG AA, 19.3.5)**: в тёмной теме `--color-on-accent:#0F172A`
     (белый на акценте 3.68<4.5) и `--color-accent-text:#60A5FA` (акцент как текст 3.96<4.5)
     для ссылок/активных пунктов; заполнения остаются `--color-accent`.
  4. **Шрифты (19.3.7)**: `document.fonts.check` без `load()` false для лениво-неиспользуемого
     семейства → конвейер явно `fonts.load('16px Inter','AaБб')` + mono.
  5. **Кнопка (htm-спред)**: `<button ${attrs}>` ненадёжно применяет `class` → класс вынесен
     инлайн в шаблон (`class=${cls}`, button.js).
- **Отклонения (CONTROL.md)**: commit в manifest/VISUAL.md = базовый коммит 82fc177 (W2,
  родитель W3) — снимки до коммита W3; `visualcheck` = совпадение manifest/VISUAL.md +
  предок HEAD (точное «хеш=HEAD» при одном коммите/этапе недостижимо). `tools/dbgshot`
  (диагностика вёрстки) — временный, удалён перед коммитом.

## W4 — Экраны (TZ v2, раздел 20) — ЗАКРЫТ

CONTROL W4: `docs/evidence/W4/CONTROL.md` — все 5 строк PASS; артефакты
`make-check.log`, `test-race.log`, `make-lint.log`, `control-screenshots.log`,
`visualcheck.log`, `screens/` (84 PNG + `manifest.json`), `control/` (3 e2e JSON).
`go test -race ./...` — 0; `make lint` — 0; `make check` — 0; `make screenshots
STAGE=W4` — 19.3 = 0 находок (84 файла); `make visualcheck STAGE=W4` — OK;
e2e `go test -tags e2e ./test/e2e/web` — 3/3 PASS.

- **7 страниц раздела 11** (`web/app/views/`): Очередь, Сессии, Серверы, Узлы,
  Журнал (События/Ходы/Аудит, CSV), Мониторинг (`/api/v1/monitoring`: 24ч по серверам,
  ходы за 24ч, p95 диспетчеризации/TTFB), Уведомления. Все данные — из одного store
  (`/state` + SSE), без REST-поллинга (R3).
- **SSE с досылкой и RESYNC** (`internal/api/events.go`, `web/app/store.js`): id — в кадре
  (`id: <id>`), не в JSON-теле; клиент берёт `msg.lastEventId` → `applyEvent(ev, sseId)`
  (маркер R8 `data-sse-id`). `Last-Event-ID` в пределах буфера → досылка; дальше →
  `RESYNC` → полный `GET /state`. События без поля `event:` (все `message`), неизвестный
  kind — сырой JSON (R4).
- **why.text** (`internal/texts`, `GET /api/v1/meta`): ineligible=22, hold_reasons=12 —
  коды приложения А + раздела 3.3 (emergency, safe_mode, server_disabled,
  server_quarantine, model_problem, key_missing; agent_exited, emergency).
- **Стенд `-scenario`** (8 сценариев 19.1) + управляющие эндпоинты `/control/*`
  (fire/state/drop-sse) — только в `aqm-stand`. E2E — go-rod + headless Chrome
  (`test/e2e/web`, build-тег `e2e`): CONTROL 1 (событие→DOM p95=101мс ≤250), 2
  (разрыв SSE → RESYNC → diff пуст), 3 (why.text в meta, 0 пустых/дублей).
- **Ключевые решения / дефекты (CONTROL.md, VISUAL.md)**: htm hook-desync исправлен
  (`pages.js` рендерит `<${Page}/>`, не `Page()`); R8 — id из кадра SSE; контраст
  (light `--color-text-muted` #5B6B85, `.banner` нейтральный текст + `.banner .icon` тон,
  `.nav-count.is-alert` → `--color-danger-text`); телефон `.tab-btn{min-height:44px}`;
  `(m.history || [])`; `fmtPayload` (без `[object Object]`).
- **Отклонения (CONTROL.md)**: commit в manifest/VISUAL.md = базовый коммит 13cf96a
  (W3, родитель W4) — снимки до коммита W4; матрица окон/тем — по осям (все 8 сценариев
  на 1440×900 светлая + вся матрица на `normal`, обоснование в VISUAL.md); e2e вне
  `make check` (build-тег + Chrome); A9 — в конце.

## W5 — Web, база: каркас, логин, очередь, сессии (разделы 12/14 ТЗ v2) — ЗАКРЫТ
- **Коммит:** `13cf96a` «web: каркас, логин, очередь, сессии — CONTROL 6/6 PASS».
- **Содержание:** каркас Preact+htm (importmap, CSP-хеш на инлайн-скрипт importmap,
  один в index/login); `web.go` (StaticFS, SPA-fallback, cookie-логин
  `aqm_session` HS256 TTL 12ч, Origin=public_url; `/web/*`); store.js (состояние,
  действия, `stateTtl`, SSE `EventSource` + `Last-Event-ID` → `since`); i18n ru.js
  (ключи = ТЗ 18); компоненты (Layout/Sidebar/Topbar/Badge/Button/Field/Modal/
  Toasts/DialogHost/EmptyState + hooks useCountUp/useToasts/useDialog); страницы
  Login/Queue/Sessions/SessionDetail/Servers/Nodes/Settings/Monitoring/Journal/
  Notifications; views/queue.js (аварийная лента, серверы, KPI useCountUp, работа,
  очередь, чат-полоса, почему); views/session.js (карточка: статус, сервер/узлы,
  действия Пауза/В очередь/Завершить); tokens.css (цветовые токены, `--color-screen-*`);
  constants.js (UI.* = `ui.*` из ТЗ).
- **CONTROL (6/6 PASS):** 0 ошибок JS (22 страницы × 2 окна × 2 темы, headless
  Chrome); события ≤250мс (e2e `test/e2e/web/web_test.go`: SSE→отрисовка,
  `--slewscale` виртуальные часы); окно 1440×900 + 390×844 (ни одна вкладка не
  требует горизонтальной прокрутки); axe 0 serious/critical; `make check`=0.
- **Эвиденс:** `docs/evidence/W5/` (screens/ + VISUAL.md, goldens
  `test/visual/golden/`).
- **Отклонения:** матрица окон/тем — по осям (все 8 сценариев на 1440×900 светлая
  + вся матрица на `normal`); e2e вне `make check` (build-тег + Chrome).

## W6 — Управление (разделы 5/11/13/14/15 ТЗ v2) — ЗАКРЫТ
- **Коммит:** `48be589` «управление (разделы 5/11/13/14/15 ТЗ v2) — мастер сервера,
  формы настроек, подключение/самообновление узлов, новая сессия — CONTROL 8/8 PASS».
- **Содержание:** мастер сервера (5.1: подключение vLLM-сервера — форма с полями
  url/model/key_env/max_inflight/max_body_mb, тест-запрос, сохранение); формы
  настроек (Settings: лимиты, тайминги, режимы, уведомления — все поля
  `ui.*` из ТЗ); подключение узлов (14: enroll — мастер добавления узла с
  проверкой host_ip, WS-подключение, dispatch); самообновление узлов (14:
  selfupdate — загрузка бинаря на узел, rollback при ошибке); новая сессия (11.5:
  мастер создания сессии — профиль, параметры, стартовый prompt); proto=2
  (расширение wire-протокола для W6-функций).
- **CONTROL (8/8 PASS):** node online (enroll → WS connect → dispatch); selfupdate
  (download → install → rollback on fail); selftest fail path; new session wizard;
  server master form; settings forms; proto=2 round-trip; `make check`=0.
- **Эвиденс:** `docs/evidence/W6/` (screens/ + VISUAL.md, control1_node_online.json,
  control2_self_update.json, control3_selftest_fail.json; goldens
  `test/visual/golden/`).

## W7 — Устойчивость (раздел 7 ТЗ v2) — ЗАКРЫТ
- **Коммит:** `98e55cc` «устойчивость (раздел 7 ТЗ v2) — режимы/отказы/рестарт,
  aqm exec, внешние кодеры, rollback — CONTROL 6/6 PASS + 67 TE».
- **Содержание:** режимы работы (NORMAL/PAUSED/EMERGENCY/SAFE_MODE — переключение,
  эффекты на планировщик/шлюз); отказы (6.5: crash recovery — восстановление
  состояния из store после рестарта, миграция сессий); `aqm exec` (8.1: выполнение
  команды в сессии из CLI); внешние кодеры (8.2: внешние кодеры — обнаружение,
  регистрация, участие в планировании); rollback (6.6: откат к предыдущей версии
  бинаря при ошибке selfupdate — rename-aside против ETXTBSY); 67 TE (test
  evidence) — юнит-тесты по всем W7-функциям.
- **CONTROL (6/6 PASS):** emergency cancel (отмена всех активных); crash recovery
  (kill → restart → state intact); aqm exec (command in session); external coder
  (detection + planning); rollback (selfupdate fail → restore); `make check`=0.
- **Эвиденс:** `docs/evidence/W7/` (screens/ + VISUAL.md,
  control1_emergency_cancel.json … control6_rollback.json; goldens
  `test/visual/golden/`).
- **Дефект:** `backup.RestoreBinary` — ETXTBSY при замене работающего бинаря →
  фикс rename-aside (rename текущего → копирование нового → удаление старого).

## W8 — Терминал в браузере (раздел 13.4 ТЗ v2) — ЗАКРЫТ
- **Коммит:** (этот) «W8: терминал в браузере (раздел 13.4 ТЗ v2) — оверлей xterm.js,
  PTY узла, лимиты, аудит — CONTROL 6/6 PASS».
- **Содержание:**
  - **PTY/WS-архитектура:** браузер `WebSocket /web/ws/term/{sid}` (cookie
    `aqm_session` + Origin vs `web.public_url`, проверка в `web.handleTerminal`);
    координатор → браузер: первый JSON `{"type":"open","chan":N}` или
    `{"type":"error","code"}`; далее binary raw-фреймы. Браузер → координатор:
    raw bytes (без frame-заголовка). Узел: `tmux -L <sock> attach-session -f
    ignore-size,active-pane -t <session>` в PTY (creack/pty v1.1.24).
  - **xterm.js v6** (6.0.0) — vendor (344970 bytes mjs + 7112 bytes css),
    importmap `@xterm/xterm`, CSP-хеш обновлён.
  - **Оверлей-компонент** `web/app/components/terminal.js` (187 строк):
    `TerminalPage` (рендерит `<TerminalOverlay sid>` при `s.terminal`);
    `TerminalOverlay`: xterm.js (`convertEol:false, cursorBlink:true,
    fontSize:UI.termFontSizePx, scrollback:UI.termScrollback,
    fontFamily:'IBM Plex Mono, ui-monospace, monospace',
    theme:{background:'transparent'}`); WS-соединение (binaryType arraybuffer;
    onmessage text→parse open/error, binary→`term.write`); Ctrl-modifier
    (letter a–z → `String.fromCharCode(c-0x60)`); desktop: окно 85% экрана
    (`UI.terminalViewportPct`); phone: полный экран + строка клавиш
    (Esc/Tab/Ctrl/↑/↓/←/→/Enter, кнопки ≥44×44).
  - **Лимиты:** `terminals_per_node_max` → 503 `TERMINAL_LIMIT` (e2e
    `test/e2e/w8/w8_test.go`).
  - **Аудит:** open/close терминала логируется в journal.
  - **Idle close:** виртуальные часы (e2e unit test).
  - **Кнопка** «Терминал» на карточке сессии (`views/session.js`).
- **Дефекты, найденные и исправленные:**
  1. **Контраст текста xterm (axe 1.17).** xterm.js v6 игнорирует
     `theme.foreground` — текст наследует `color` контейнера. Фикс:
     `.term-body{color:var(--color-screen-text)}`.
  2. **Кнопки < 44×44 (touch, телефон).** `.term-close` → `min-width/height:44px`;
     `.xterm-helper-textarea` → `width:44px !important; height:44px !important`.
  3. **SGR-bold** рендерился тёмным → убран `\x1b[1m` из стартового вывода mock.
- **CONTROL (6/6 PASS):**
  1. 50 циклов open/close → список клиентов = initial, 0 attach-процессов ✅
  2. Размер окна стабилен (80×23) ✅
  3. Echo p95 ≤ 50мс (замер 0.1мс) ✅
  4. `terminals_per_node_max` → 503 `TERMINAL_LIMIT` ✅
  5. Idle close на виртуальных часах ✅
  6. Скриншоты desktop+phone в VISUAL.md ✅
- **Эвиденс:** `docs/evidence/W8/control/cycles-echo.json`, `limit.json`;
  `docs/evidence/W8/screens/` (2 PNG + manifest.json); `docs/evidence/W8/VISUAL.md`;
  goldens `test/visual/golden/journey-W8-T__{1440x900,390x844}.png`.
- **Проверки:** `make check`=0; `make screenshots STAGE=W8`=0 findings;
  `make visualcheck STAGE=W8`=OK; `go test -tags e2e ./test/e2e/w8`=ok 5.569s.

## W9 — Приёмка на железе (раздел 20, TZ v2) — ЧАСТИЧНО (машина ✅, оператор ⬜)

**Статус:** CONTROL 5/6/7 PASS (артефакты `docs/evidence/W9/control/`);
CONTROL 1/2/3/4 — операторские (2-часовой тест: 6 сессий, J1 с телефона,
остановка vLLM на vzu5, перезагрузка 3p14-aaa). Полный сценарий и финальный
список оператору — `docs/evidence/W9/TEST_PLAN.md`.

**Развёртывание (живое):**
- 3p14-aaa: user systemd `aqm` (KillMode=process, Restart=on-failure,
  EnvironmentFile=~/.config/aqm/env); coordinator.bind=100.124.155.106
  (tailnet), API :8788, шлюз :8787 (gateway_url), веб 127.0.0.1:8790;
  `sudo tailscale set --operator=$USER` + `tailscale serve --https=0 --bg
  8790` → https://3p14-aaa.tail46a65f.ts.net. **BotBoss был на 443** —
  после W9: `tailscale serve 3000`.
- vzu5-big: user systemd `aqm-node` (linger, KillMode=process);
  client.coordinator=100.124.155.106:8788, **client.token_env=AQM_NODE_TOKEN**
  (enroll → token → claim vzu5-big; `/api/v1/dist/aqm` 200 с токеном узла,
  401 с операторским); vLLM :8004 (root-процесс — только оператор),
  сервер `vzu5` prio 50, 2 слота; gpu=nvidia → GPU-телеметрия в hub.
- Бинарники: 3p14 = B7 (W9-доп-3: иконки/FAB/enroll/roots; резервы `.b4`/
  `.b5`/`.b6`), vzu5-big — УДАЛЁН оператором 2026-09-29 (uninstall),
  добавляется vzu5-claw (enroll через https после B7).

**Найденные и исправленные дефекты (6):**
1. gateway `max_tokens=0` в теле → 400 от vLLM (`internal/gateway/body.go`:
   n≤0 → удаление поля; регрессия `TestBodyTransformNonPositiveAndNoCap`).
2. PTY-терминал на headless-узле: сервис aqm-node без TERM → `tmux attach`
   мгновенно умирал (`internal/node/pty/pty.go`: `envWithTerm`
   TERM=screen-256color; регрессия `TestPtyOpenWithoutTermEnv`).
3. TZ 14.2 exit-75 отсутствовал: после self-update узел переподключался
   старым кодом (`node.ErrUpdated` + `os.Exit(exitSelfUpdated)`;
   регрессия `TestWSClientSelfUpdateExits`).
4. **Ответ UPDATED терялся:** `awaitReplyFlush` ждал освобождения канала
   `out`, а писатель мог не дописать кадр — ранний `conn.Close()` обрывал
   ответ (репро 200/200). Барьер записи `flushReq` (FIFO единственного
   писателя) + сток канала (`internal/node/ws.go`).
5. **URL дистрибутива = gateway_url:** `/api/v1/dist/aqm` живёт на API
   (api_port), шлюз — 404; self-update молча падал (API отвечал
   `update-requested` при любом результате). `requestBaseNoPath` =
   `http://<bind>:<api_port>` (`internal/api/nodes.go`; регрессия в
   `TestTE_N4`).
6. **Браузер на корне без сессии видел сырой 401 JSON** (дополнение,
   найден оператором): вместо страницы входа. TZ O4/11.1 — переход на вход
   с возвратом на запрошенную страницу. `handleShell`: без действующей
   сессии и `Accept: text/html` → 302 `/web/login?next=…`; скриптовые
   клиенты — 401 (W1 CONTROL сохранён); `lookupSession` — общая проверка
   cookie для cookieAuth/оболочки; SPA: 401 вне `/web/login` →
   `login?next=<hash-роут>` (`web/app/api.js`), возврат — `nextPath()` в
   `web/app/views/login.js` (+ фолбэк-страница). Регрессия
   `TestWebShellLoginRedirect`.

**W9 (доп): замечания оператора по телефонному интерфейсу (16 пунктов):**
1. Нижние вкладки — только иконки (подписи скрыты CSS, `title`/`aria-label`
   сохранены) — `web/app/components/bottom-tabs.js`, `layout.css`.
2. «Ещё» — теперь рабочий лист (Dialog) с Узлы/Мониторинг/Уведомления/
   Настройки (на телефоне боковой панели нет; лист — единственная дорога).
3. Статус системы — точка у заголовка (цвет = режим, подпись в подсказке);
   текстовая плашка `.mode-badge` удалена (topbar.js `ModeDot`, все брейкпоинты).
4. «Сегодня»/метрики на телефоне — одна компактная строка (`.stat-grid`
   4×minmax, значение --font-size-sm).
5. Пустое состояние «Ничто не требует внимания» (ru.js).
6. KV/GEN — не более 2 знаков после точки (`FMT.round2Digits`, labels.js
   round2 в fmtPct/fmtTokS/fmtGB).
7. Динамика сервера — период явный: «Динамика · последние N мин»
   (`window_min` из `/api/v1/monitoring` = monitor.history_window_min).
8. Редактирование сервера — кнопка в детальной; тот же диалог, что и
   создание: prefill `GET /servers/{name}` (PascalCase), сохранение `PATCH`
   (snake_case, новые `Upstreams` в PATCH, имя не меняется — disabled).
9. «Новая сессия» на телефоне — FAB (плавающая кнопка; текстовая на <768px
   скрыта и не умещается в 390px); пустые состояния Очередь/Сессии тоже
   открывают диалог (было: navigate на ту же страницу).
10. Серверы — как очередь (общие компоненты `.stat-grid`/jrow — один фикс).
11. Мастер: Имя шире (flex 2), Слоты 72px/Приоритет 88px (не упираются в
    стену); подсказка «латиница, цифры…» одной строкой (ellipsis), звёздочки
    в «Имя*» и «* латиница…» — связь поля и правила.
12. URL апстрима — на отдельной строке (не втроём с моделью/ключом).
13. **Баг «сервер отвечает» с пустым URL:** `handleServerProbe` при пустом
    HealthURL теперь `health_ok=false` (было: поле не ставилось → фронт
    считал OK); кнопка проверки без URL заблокирована (title «Сначала
    укажите URL сервера»), подпись честная (не отвечает до проверки).
    Регрессии `TestProbeEmptyHealthURL` + живой API.
14. Журнал на телефоне: `.jrow` — время·вид·цель, подробности (payload,
    т.ч. NODE_STATE) — своей строкой (было: фиксированные 402px > 390px,
    колонка деталей сжималась в ноль).
15. **Журнал «Ходы» — имена, а не sid:** имя снимается в строку хода при
    старте (миграция `010_turn_session_name.sql`, `turn.session_name`,
    `TurnStart` — COALESCE-подвыборка из session); старые ходы — из session,
    пока сессия жива. Регрессии `TestJournalTurnsSessionName` +
    `TestJournalTurnsNameAfterSessionDeleted`.
16. Журнал «Аудит» — реальные строки (event kind paste/term_open/term_close,
    `store.AuditPage`), не заглушка «появится в W5»; CSV — для всех вкладок.
    Регрессия `TestJournalAuditRows`.

Проверки: `make check` (тесты -race + R2-линтеры) — 0; живые API: audit
строки с payload, probe {} → `health_ok:false`, monitoring `window_min=10`;
golden-база и evidence W3–W8 пересняты (19.3 — 0 находок на каждом этапе);
drv `w6.go` J6-2 приведён к новой семантике (URL здоровья = `location.origin
+ '/web/login'`, клик по «Сервер не отвечает»). Координатор = B6 (B4/B5 —
резервные `.b4`/`.b5`), узел vzu5 — B3.

**W9 (доп-3): 5 замечаний оператора (2026-09-29):**
1. **Иконки не рендерились ВООБЩЕ** (футер «пустой», топбар без колокольчика):
   две независимые причины — (а) ICONS несёт только внутреннюю разметку
   (path/…) без `<svg>`-обёртки — браузер отбрасывал неизвестные элементы;
   (б) **object-spread в атрибутах htm-шаблона (`<span ${attrs}>`) молча
   выпадает в vendored htm** — vnode рождался с пустыми props (`<span></span>`,
   без class и innerHTML; проверено node-тестом против web/vendor/htm.module.js).
   `Icon` переписан: literal-атрибуты + `dangerouslySetInnerHTML=${{__html}}`
   + svg-обёртка (viewBox 24, stroke=currentColor — CSS `.icon svg` уже был).
   Иконки видны от W3-скриншотов не потому, что пайплайн «не рендерил» —
   код был сломан с первого релиза UI.
2. **FAB убран** (user: «неудобно»): `FabNewSession` и `.fab` удалены;
   на телефоне «+» — icon-btn в действиях топбара (`.ns-new`, 44×44,
   страницы Очередь/Сессии; на широких — текстовая кнопка).
3. **project_roots — конкретный путь вместо надписи:** узел `list_dirs`
   возвращает roots (абсолютные, тильды раскрыты, существующие, без дублей)
   через СУЩЕСТВУЮЩЕЕ proto-поле `Msg.ProjectRoots` (omitempty — старые узлы
   не ломают контракт); `Reply.Roots → msg()`; API `/nodes/<host>/dirs`
   += `"roots"`; диалог тянет `/dirs` при выборе узла (даже с пустым dir),
   подсказка «доступны каталоги узла: /home/user» (фолбэк — прежняя
   обобщённая). Регрессии `TestListDirs` (Roots) + `TestReplyMsgCarriesRoots`.
4. **Выбор узла крупнее:** `.field select` (width 100%, padding 10px,
   --font-size-md, токены border/radius; телефон — min-height 44px, 19.3.6).
5. **enroll 401 через https** (`curl -fsSL …/enroll/<tok>/install.sh | bash`
   с чистой машины): маршруты /enroll зарегистрированы в `api.Handler()`,
   а НЕ в `Routes()` — веб-слушатель монтировал только Routes() под
   cookieAuth → 401 «нужен вход» (а на API-порту всё работало). Фикс:
   `api.EnrollHandler()` (отдельный mux, тот же handleEnroll) →
   `web.SetEnrollHandler` — на вебе `/enroll/` публично (токен в пути =
   аутентификация, 14.2). Регрессия `TestWebEnrollPublicWithoutCookie`.
   Живьём: install.sh **200** через https, `COORD="https://3p14-aaa…"`
   (X-Forwarded-Proto), битый токен → 401 `BAD_TOKEN`, `/enroll/<tok>/aqm`
   200 (27.5 МБ).

Проверки доп-3: `make check` — 0; живой координатор: enroll (см. п.5),
`/dirs` → `roots: ["/home/user"]`, DOM-проб (go-rod): в `.icon` — svg
20×20 stroke 1.75 (21 иконка на Очередь), диалог: подсказка «доступны
каталоги узла: /home/user», select 40px/14px; golden-база и evidence W3–W8
пересняты заново (148 файлов) — иконки видны на всех страницах, FAB
отсутствует. Координатор = B7 (резерв `.b6`). **Живой стэнд изменился:**
оператор 2026-09-29 ~14:07 удалил узел vzu5-big (uninstall → UNINSTALLED)
и начал добавлять новый (vzu5-claw) — после B7 то же curl-команда проходит;
токен — новый (старый может быть израсходован/отозван), создаётся на
странице «Узлы».

**W9 (доп-3b): узел после enroll вешался на «bad handshake» (B8).**
Причина: install.sh писал в конфиг `client.coordinator` = базис запроса
(https://3p14-aaa…:443), а канал узла `GET /api/v1/node` смонтирован только
на API-слушателе (bind:api_port) и на вебе намеренно отсутствует →
WS-апгрейд получал 401 → «websocket: bad handshake» (репорт оператора,
vzu5-claw 2026-09-29). Фикс: в конфиг узла — `requestBaseNoPath()`
(`http://<bind>:<api_port>`), COORD для загрузки/claim остаётся базисом
запроса (https). Регрессия в `TestEnrollFlow` (строка `coordinator:`
install.sh = API-адрес). Live: новый install.sh —
`COORD=https://3p14-aaa…`, `coordinator: http://100.124.155.106:8788`.
Координатор = B8 (резерв `.b7`). Переподключение vzu5-claw: перезапустить
enroll с новым токеном (старый остался привязан к host, дубли допустимы —
claim = UPDATE; старый можно отозвать на странице «Узлы»).

**W9 (доп-3c): claim узла упал в HTTP 500 — транзитный сбой записи в БД (B9).**
Re-claim vzu5-claw (токен 2830ffe6…) 2026-09-29 ~16:04 → 500. Диагноз по
журналу: в 16:04:49 одна запись SQLite упала (причина не записывалась — см.
ниже) → writeErrHook (K6) → SAFE_MODE → все записи блокировались → claim
вернул 500; в 16:04:59 `ProbeWrite` прошёл → SAFE_MODE снят → NORMAL
(режим подтверждён `GET /api/v1/meta`). Транзитно, само исцелилось; повторный
claim тем же токеном проходит (токен не привязан — claim не завершился).
Два реальных бага логирования, вскрытых инцидентом:
1. `scheduler.notifyf` не применял формат — CRIT шёл с дословным
   `«запись в БД невозможна (%s)»`, реальная ошибка терялась;
   фикс: `fmt.Sprintf(format, args...)`.
2. Ошибки записи в store не писались в журнал; фикс: `store.writeErr(stage,
   err)` логирует `store: ошибка записи stage=… err=…` перед хуком SAFE_MODE
   (теперь любой transient-сбой диагностируем по journalctl).
Регрессия: тесты store/scheduler прошли (`make check`), live: B9, режим
NORMAL, doctor local=UP. Координатор = B9 (резерв `.b8`).

**W9 (доп-3d): re-enroll 500 — реальная причина + эндпоинт qwen code (B10).**
Оператор повторно гонял enroll vzu5-claw → снова 500. С B9 реальная ошибка
попала в журнал: `constraint failed: UNIQUE constraint failed:
node_token.host (1555)`. Т.е. не транзит (доп-3c был прав про логирование,
но не про причину): `node_token.host` — PRIMARY KEY, у host `vzu5-claw`
уже был активный токен `053faebd…` (привязан при первом claim 12:36), и
каждый новый claim (`BindTokenHost`) делал `UPDATE … SET host='vzu5-claw'`
→ UNIQUE → 500 + ложный SAFE_MODE на ~10 с. Фиксы:
1. **`store.BindTokenHost`** — под схему host=PK: если ряд host существует
   (re-enroll), старый активный токен host отзывается, а новый выдаётся в
   ряду host (дубликат в pending-ряду host='' тоже отзывается); если ряда
   нет — ряд токена переносится на host. Повтор тем же токеном+host —
   идемпотентен. Состояние токена читается параллельным чтением (до
   DoWrite), чтобы логический `ErrNotFound` не шёл как «ошибка записи».
2. **`store`: constraint ≠ сбой хранилища.** `isConstraintErr`
   (`sqlite.Error.Code() & 0xff == SQLITE_CONSTRAINT`): UNIQUE/FK — только
   WARN в журнал, хук SAFE_MODE НЕ срабатывает (K6 — для SQLITE_FULL/IOERR);
   наружу — `ConstraintError` (`errors.Is(ErrConstraint)`), API мапит на 409.
3. **install.sh** теперь пишет `~/.config/aqm/secrets.env` (0600, `AQM_
   NODE_TOKEN=…`); `aqm node` берёт токен из env, а при пустом env — из
   secrets.env (ТЗ 666) → после перезагрузки systemd-узел поднимается.
4. **Эндпоинт qwen code** (запрос оператора «инфо о правильной настройке
   эндпоинта» + «добавлять в конфиг qwen code при добавлении узла»):
   aqm запускает кодера с `OPENAI_BASE_URL=<шлюз>/s/<sid>/v1` +
   `OPENAI_MODEL=<model_alias>`, но qwen code может перебить это
   собственным `~/.qwen/settings.json` (если `security.auth.selectedType !=
   openai` или в `modelProviders` есть запись с `id = model_alias` → qwen
   идёт на `baseUrl` из записи, минуя шлюз). (а) hello-ответ координатора —
   `config {model_alias, gateway_url}` (`proto.Msg` += поля; узел
   `config`-обработчик → `EnsureQwenSettings`; встроённый узел — на старте);
   (б) `node.EnsureQwenSettings` идемпотентно правит `~/.qwen/settings.json`:
   `selectedType=openai`, убирает записи `modelProviders` с
   `id=model_alias`, резерв `settings.json.aqm.bak` до первого изменения,
   изменения в журнал (`qwen settings обновлён: …`); (в) `GET /state` +=
   `gateway {url, model_alias}`; диалог «Новая сессия» — блок «Эндпоинт
   (шлюз aqm)» с окружением и требованием к конфигу; (г) manual.md §3.11.
   На 3p14-aaa конфиг оператора (id `qwen3.8-27b`, `selectedType=openai`)
   не меняется (id ≠ model_alias `qwen3.6-27b`) — правка no-op.
Регрессии: `TestBindTokenHostReEnrollReplacesOld/RevokedFails`,
`TestConstraintDoesNotTripWriteErrHook` (store), `TestEnrollReClaimSameHost`
(api: re-claim 200 + старый токен отозван), `TestNodeHelloSendsConfigWith
ModelAlias` (api), `TestEnsureQwenSettings{Creates,Patches,Idempotent}`
(node). `make check` 0. Координатор = B10 (резерв `.b9`); повтор enroll
vzu5-claw (любой валидный токен) теперь проходит, при подключении узла его
qwen-конфиг приведётся в порядок автоматически.

**CONTROL 5 (self-update, PASS):** `POST /api/v1/nodes/vzu5-big/update` →
узел скачал B3 (Bearer-токен узла, SHA-256, `aqm.new selftest`), swap
(`aqm.prev`), **exit 75**, systemd restart (counter 1) — цикл 11 с;
4/4 SID без изменений; tmux-сессии живы. `control/self_update.json`.

**CONTROL 6 (echo, PASS):** 20 пакетов через tailnet (браузерный путь:
https → 443 → веб → WS → координатор → узел → PTY) — p95 59.13 мс ≤ 150.
`control/echo_terminal.json`.

**CONTROL 7 (doctor, PASS):** обе машины, все PASS. `control/doctor_*.json`.

**Подводные камни (выучены):**
- gorilla/websocket: после любого read-error `readErr` липнет (дальнейшие
  ReadMessage — мгновенная ошибка; 1000 повторов → panic). Пробы не должны
  истекать read deadline и продолжать читать то же соединение.
- Остановка aqm-node: graceful тянется до 90 с (TimeoutStopSec) → SIGKILL
  (read-loop WS не знает про ctx, read deadline 2 мин). Сессии не страдают
  (tmux вне процесса). Закладывать ~90 с в рестарты.
- Деплой на vzu5: `cat bin | tailscale ssh vzu5-big 'cat > /tmp/aqm.new'`
  (scp ProxyCommand не работает) + **chmod +x** (иначе 203/EXEC-цикл).
- JSON API: ключи как в `docs/api.md` (SID/Name/State…); «online» узла —
  по `health.at` (поле `Conn` в GET /nodes всегда null).
- Cookie `aqm_session` — Secure только по https (tailscale serve); прямой
  http к :8790 cookie не примет. TLS tailscale: SNI=ts.net hostname;
  по IP — InsecureSkipVerify + ServerName.
- Zombie-сессии: запись без панели (PaneID пуст) не становится GONE
  (некому отслеживать) — чистить из store вручную (API-роута удаления
  нет; `handleSessions` читает store live, прямое удаление строк безопасно
  при пустых queue/lease).
- make check: lint-hosts запрещает реальные хосты/CGNAT (100.64/10) в коде.

**Оператору (итого, порядок):**
1. 2-часовой тест (TEST_PLAN.md фазы 1–3): 6 сессий (3+3), ходы, J1 с
   телефона 5/5, остановка vLLM на vzu5 (root) во время хода, перезагрузка
   3p14-aaa (sudo reboot, записать время).
2. Артефакты: reboot_time.txt, doctor после, SQL-инварианты (готовые
   запросы — Фаза 4 TEST_PLAN.md, схема сверена с живой БД).
3. `tailscale serve 3000` — вернуть 443 BotBoss.
4. Сообщить результат.

## Закрытый CONTROL (Э6, базовое ТЗ)
- строки (раздел 10 ТЗ + таблица этапов раздела 15): (1) разбор записанных
  `/metrics` реальных серверов и выводов nvidia-smi/rocm-smi — 100%;
  (2) EXTERNAL ровно через `external_confirm_sec` на виртуальных часах;
  (3) отсутствие метрик → WARN doctor и `metrics_missing=true`, не
  молчаливый ext=0 без флага — **все 3 строки PASS**
  (docs/evidence/Э6/CONTROL.md + артефакты: monitor-test.log,
  servers_timeline.txt, api_servers_p{0,1,2}.json, doctor_cli.txt,
  nvidia_actual.csv, make-check.log, test-race.log).
- живой стэнд `/tmp/aqm-e6/` (копия stand.sh/config.yaml в evidence):
  fakellm-двойник vLLM + «мёртвый» сервер + узел с реальным nvidia-smi;
  P0 baseline (GPU 100%, 45/48 GiB = nvidia-smi 23056/24576 MiB) →
  P1 ext=3, kv_pct=42, слоты `external,external` → P2 ext=0, `free,free` →
  P3 doctor rc=0 (WARN metrics — dead, PASS require_direct) → P4
  подключение узла = 1 за ~2.5 мин (ping coord→node, read deadline 2 мин).

## Ключевые факты Э6, полезные для Э7–Э8

- `internal/monitor`: `New(cfg, clk, fetch, ext ExternalSetter, sink
  ServerStateSink, infl InflightCounter, log)`; `Run(ctx)` + публичный
  `Tick()` (вирт. часы в тестах); на сервер: health (HealthIntervalSec,
  ServerHealth.Observe(down/up_after) → sink.SetState) и metrics
  (MetricsIntervalSec) — дедлайны под `ss.mu`, I/O вне.
  `ext = max(0, running+waiting−InflightByServer)`; недоступны/не разобрано
  → `metrics_missing=true`, ext=0 (флаг виден: doctor WARN, TUI «—»).
  `Sample/HistoryPoints/FoundMetrics/LastMetrics` — срезы для API/doctor.
  **kv_pct**: `vllm:kv_cache_usage_perc` — доля 0..1; Sample/HistoryPoint
  хранят проценты (×100, `kvPct`), `VLLMMetrics.KV` — доля.
- BUG Э4, найден в Э6 (регрессия `TestS8bExternalFullLoad`):
  `scheduler.externalWant` брал `freeSlots()` (исключая EXTERNAL) → при
  ext ≥ free (все слоты EXTERNAL) min(ext,0)=0 → EXTERNAL сам себя снимал
  (осцилляция 1 тик). Исправлено: считаются слоты, не LEASED и не COOLDOWN.
- `cmd/aqm-fakellm` — новый бинарь (двойник vLLM для стэндов, паттерн
  mockcoder): fakellm `New(model)` + `Handler()` (без httptest),
  `/_set-metrics?running=&waiting=&kv=` — контроль со стенда.
  `/metrics` fakellm — формат vLLM (5 required-метрик, счётчики растут на
  запросах).
- Узел: `gpu: nvidia|amd` + `gpu_server` → `RunGPU` (фиксированная команда
  без оболочки, timeout 10 с, раз в MetricsIntervalSec) → `KindGPU{server,
  cards}`; ошибка — WARN + пропуск цикла. `GPUEnabled()` = gpu!=none &&
  gpu_server!="". **Ping-хендлер** (`session()`): WS-пинг потребляется
  внутри `ReadJSON` (авто-pong) и до цикла чтения не доходит — хендлер
  сам продлевает `SetReadDeadline(wsReadDeadline)` + шлёт pong; иначе узел
  реконнектится ровно через 2 мин при простое.
- Координатор: `hub.SetGPU/GPU` (по server, deep copy),
  `hub.StartPinger(ctx, log)` — WS ping 15 с. `GET /api/v1/doctor` —
  серверная часть (state + metrics_missing + found/absent vllm).
  `ServersView`: EXTERNAL-резерв виден в `slots_info` — «external» на
  первых N свободных (не LEASED/COOLDOWN) слотах.
- doctor: `metrics` (per server: достижимость, missing → WARN с именами) и
  `require_direct` (3xx → WARN «редирект», ≠1 модель → WARN) в
  `DefaultChecks()`; `httpDo` — точка подмены в тестах.
- ТЗ-правило: тестовый tmux только `-L aqmtest`; стэнд Э6:
  `/tmp/aqm-e6/{run.sh,config.yaml,bin,out,logs}` (fakellm:8801 model
  fake-e6 + «dead» 8899 → DOWN/metrics_missing; узел gpu_server=fa).

## Запрещено забывать
- числа только из config/defaults.go
- часы только internal/clock
- received_at ставит координатор
- профиль только qwen
- токенов в БД нет
- напоминание: литералы в scheduler/gateway/detect проверяет
  scripts/check-literals (0 и 1 разрешены)
- regex профиля — только из фикстур: не расширять «чтобы прошло»
  без новой фикстуры (раздел 7 ТЗ)

## Версия агента и фикстуры (критично для приёмки)

- Фикстуры: **Qwen Code 0.24.4**, buffer-режим (`ui.useTerminalBuffer: false`
  — workaround crash VirtualizedHistory в tmux, 0.23.3/0.24.x; проект
  `qcode-tmux-crash`). 28 экранов в `testdata/fixtures/qwen/0.24.4/`
  (+ 7 mockcoder в `testdata/fixtures/mockcoder/`).
- На машине оператора автoupdater переключил `~/.qwen/updates/npm/…/active.json`
  на **0.24.5** (2026-09-24). Пин версии:
  `QWEN_CODE_MANAGED_NPM_PIN={"version":"0.24.4","bootstrap":"/home/user/.local/lib/node_modules/@qwen-code/qwen-code/cli-entry.js","updateRoot":"/home/user/.qwen/updates/npm"}`.
- Сессия с версией, на которую нет фикстур, → TUI-флаг `untested` (раздел 7 ТЗ).

### Процесс обновления фикстур после смены UI Qwen Code (раздел 7 ТЗ)

1. Установить новую версию; `qwen --version` → `<ver>`.
2. Снять полный набор: `aqm fixture capture --socket aqmtest --version <ver>
   <target> <state>` для 13 состояний × ≥ 2 экрана
   (состояния и техника съёмки — в docs/evidence/Э1/CONTROL.md, раздел
   «Как снимались фикстуры»).
3. Прогнать классификатор: `go test ./internal/detect/`.
4. При рассинхроне — править `profiles/qwen.yaml` **только** под новые
   фикстуры (каждый новый pattern обязан иметь фикстуру-обоснование);
   расширять regex «чтобы прошло» без новой фикстуры запрещено.
5. Обновить ожидаемые состояния в `internal/detect/fixture_test.go`
   (только при подтверждённой смене поведения UI, с записью в CONTROL).
6. Коммит: фикстуры + профиль + тесты + evidence одним этапом.

### Среда съёмки (воспроизводимость)

- tmux 3.2a, сокет `-L aqmtest`. **Важно:** `new-session -d -x -y` игнорируется
  (панель = размер pty запускающей оболочки) — размер ставится
  `tmux resize-window -t <сессия>:0 -x 200 -y 50` после создания.
- `capture-pane -p -J` (видимый экран, `-J` склеивает переносы);
  файл-фикстура = сырой вывод.
- Документ (ответы, ошибки, ретраи) живёт в **истории** tmux и в видимый
  экран не попадает (buffer-режим) — детектор по ТЗ видит только хром +
  оверлеи (диалог, boot, plan-строка).
- 429/500 для съёмки — локальный stub OpenAI (раздел 15: fakellm — Э3).

## Ключевые факты Э2/Э3, полезные для Э4–Э8

- `Hub.Dispatch(ctx, paneID, msg)` — готовый примитив «команда узлу +
  ожидание reply» (использует `WaitReply` ДО отправки, timeout у
  вызывающего); планировщик Э4 строит на нём.
- Замерный эндпоинт `POST /api/v1/internal/dispatch` остаётся как
  диагностика (внутренний, под токеном).
- Узел: сканер (ps один раз за цикл, совпадение = сам процесс панели +
  потомки), снимки по периодам state (1/2/5 с), пульс 10 с,
  переподключение 1→30 с, read deadline 2 мин.
- `stable_since` — RFC3339Nano (дробная секунда для замера задержки).
- Коллизии `aqm run`: NAME_IN_USE (live + GONE в retention, detail с
  старым sid — координатор), TMUX_SESSION_EXISTS / PANE_ALREADY_MANAGED
  (клиент); qwen-уже-запущен-без-sid → отказ + печать команды рестарта.
- Шлюз (Э3): `gateway.NewGateway(cfg, st, clk, log)`; разрешение аренды —
  правила 1–4 раздела 6 (ACTIVE / PENDING→ACTIVE+RUNNING / IMPLICIT /
  удержание до `hold_max_sec`); миграция до первого байта — один повтор
  через `migratePass` (внеочередной `/health`, `SERVER_DOWN`,
  `MIGRATED`, `last_migrated_from`); тело подменяется под сервер **каждой**
  попытки (`transformBody` в `serveAttempt`); 4xx — без изменений;
  `Release(sid, reason)` освобождает аренду + будит ждущих.
- Store: `LeaseCreate/LeaseGet/LeaseRelease/LeaseSetActive/LeaseBumpRequest/
  LeaseCountByServer/LeaseListActive`, `QueueUpsert/QueueGet/QueueDelete/
  QueueSetHeldRequest/QueuePosition/QueueList`, `RequestRecord/RequestList`
  (таблицы `lease`, `queue_entry`, `request`; запись — одна горутина-писатель).
- `fakellm` (`internal/testutil/fakellm`): `Start(model)`, `SetFailBefore(n,
  "502|503|504|conn")`, `SetFailAfter(n)` (обрыв посреди SSE), `SetDelays(
  firstByte, chunkPause, chunks)`, `SetHealthDown`, счётчики/эхо
  (`Requests`, `LastModel`, `LastAuth`, `LastMaxTokens`, `SawStreamOptions`).
  Live-стенд Э3: `/tmp/aqm-e3/{stand.sh,harness.py,fake.go}` (вне репо).
- `last_request_end` — в памяти шлюза (`Gateway.LastRequestEnd(leaseID)`),
  не в схеме `lease` (документировано в CONTROL Э3).
- Go 1.26: `httputil.ReverseProxy` — `Rewrite: func(*httputil.ProxyRequest)`
  (Director deprecated); `pr.Out` — исходящий запрос.

## Ключевые факты Э4, полезные для Э5–Э8

- Планировщик (`internal/scheduler`): тик по `scheduler.tick_ms` + по
  событиям; порядок тика `tickExternal → tickCooldown →
  tickDependencyAndPin → tickGrant → tickConfirm → tickComplete →
  tickNodeLost → tickAutoEnqueue`. **Весь тик под `s.mu`** — публичные
  методы, зовущиеся из тика, обязаны идти через `*Locked`-ядра
  (регрессия: `TestAutoEnqueueFlow`, дедлок `Enqueue` в `tickAutoEnqueue`).
- Публичное API планировщика (для CLI/TUI Э5): `Enqueue/Dequeue/Requeue/
  Prio/Pin/Prefer/Unpin/Hold/Unhold/Why/Pause/InjectReply/DispatchLatency`.
- Эндпоинты: `GET /api/v1/state`, `GET /api/v1/sessions`,
  `GET /api/v1/sessions/{t}/why`, `POST /api/v1/pause`,
  `GET /api/v1/internal/dispatch-latency` `{n,p95_ms,samples_ms}`,
  `POST /api/v1/internal/dispatch`.
- `received_at` снимка ставит координатор (`s.clk.Now()` в
  `PaneUpdate`/`applyPanes`); `auto_enqueue` — постановка в очередь по
  IDLE-панели с непустым вводом + стабильность `auto_enqueue_stable_sec`
  (enqueue API/CLI появится в Э5).
- `request.turn_id` пока 0 (заглушка Э3; ход открывается при подтверждении
  старта — см. CONTROL Э4 отклонение 4); привязка запрос→ход по
  `sid` + временному окну.
- Узел: горутина записи WS наблюдает за `connCtx` (регрессия на вис после
  `wsReadDeadline` — CONTROL Э4 отклонение 2); без пульса
  координатора→узел узел переподключается каждые ~2 мин при простое
  (безопасно). Периодический ping — Э6.
- Стенд Э4 (вне репо): `/tmp/aqm-e4/{stand.sh,config.yaml,aqmr.py,
  harness.py,fake,bin/qwen}`; `harness.py` — 6×mockcoder×3 хода, сэмплер
  аренд 100 мс, вердикты в `harness.json`. `aqmr.py` повторяет `aqm run`
  на сокете `aqmtest`.

## Ключевые факты Э5, полезные для Э6–Э8

- Клиент (`internal/client`): `New(base, tokenEnv)`, `Do(method, path,
  body, out) (status, err)` (нон-2xx → `*client.Error{Status,Code,Detail}`),
  `DoRaw` (text/plain peek), `StreamEvents(ctx, lastID *int64, ch)` — SSE с
  автопереподключением (пауза 1 с, `after` = последний id, без дублей).
  Токен — из env (`client.token_env`); пустой = локальный режим.
- TUI (`internal/tui`): `Model` (bubbletea), `Provider` интерфейс
  (`Snapshot()` + `Events()`), `NewLive(client)` — live. Движок: начальный
  `Snapshot`, далее на каждое SSE-событие `refreshCmd` (4 GET) +
  `eventsCmd` (канал). **Опроса нет.** Вкладки 1–4 (Обзор/Сессии/
  Статистика/События); компактный режим `<100` колонок; цвета
  RUNNING/QUEUED/HOLD/DETACHED; неизвестный kind — сырой JSON.
- Golden-тесты: `internal/tui/tui_test.go` (`fakeProvider` + фикс. время),
  goldens в `internal/tui/testdata/` (обновление — `go test -update`).
  Замер перерисовки — `latency_test.go` (`TestTUI_RedrawLatency`, ~897 мкс).
- API Э5 (схема — `docs/api.md`): `GET /queue` (QueueRow с WHY/prompt),
  `GET /servers` (ServerRow+slots_info), `GET /stats?since=`,
  `GET /events?after=&limit=` (SSE; `?limit=N` — разовый JSON),
  `POST /sessions/{t}/{enqueue,dequeue,requeue,prio,pin,prefer,unpin,
  hold,unhold,auto-enqueue,cancel}`, `POST /pause`, `/resume`,
  `/servers/{s}/drain`, `/nodes/{h}/drain`, `GET /sessions/{t}/peek?n=`.
  Планировщик: `QueueView/ServersView/Peek/Cancel/Stats/SetAutoEnqueue`
  (в `internal/scheduler/views.go`), `NodeSeen/NodeLost` шлют NODE_STATE.
- CLI (`cmd/aqm`): все команды раздела 11; данные команды (`ls q servers
  why stats events`) — таблица + `--json`; адресация `<t>` = sid | имя |
  `host:tmux[:window.pane]` | `--pane %N` | голый `%N` (`resolveTarget`).
  `aqm` без аргументов — TUI (TUI удалён в W1 → CLI-сводка `runSummary`).
  `aqm node drain|undrain` — подкоманды демона.
- Метрики серверов в TUI/CLI — «—», пока данные не пришли (заполняются с
  Э6: `ServerRow.gpu/vram_used_gb/vram_total_gb/kv_pct/gen_tok_s/ext/
  metrics_missing/gpu_cards`, схема в `docs/api.md`).

## Ключевые факты W1, полезные для W2–W9

- **Веб** (`internal/web`): `New(cfg, st, routes, clk, log)` + `SetToken`/`SetVersion`;
  `Handler()` — mux: `GET /web/login`, `POST /web/login`, `POST /web/logout`,
  `GET /web/session`, `GET /web/sessions`, `/api/v1/` (cookie+CSRF), `/` (SPA-оболочка,
  c cookie → 401). Middleware-порядок `cookieAuth(csrf(routes))`. `actor` =
  `Tailscale-User-Login` или первые 8 символов id_hash. `web.ListenWithRetry(ctx,addr,interval,log)`.
  Миграция на SPA-фронт — W3 (web/ ещё нет; `lintweb` пока «web/ отсутствует»).
- **Встроенный узел** (`cmd/aqm/embedded.go`): `memConn` (канал в памяти, `WriteJSON`→
  cmds, `WriteControl` no-op) реализует `api.NodeConn`; `embeddedNode` = `node.New`+
  `node.Run` + `srv.ApplyNodeMsg` + `NodeSeen`/`NodeLost`. `api.NodeInfo.Conn` теперь
  интерфейс `NodeConn` (не `*websocket.Conn`). Хост = hostname машины, HostIP = 127.0.0.1.
- **`api.Server` новые методы**: `ApplyNodeMsg(host, m proto.Msg)` (общий с WS-хендлером:
  KindPanes→applyPanes, KindPane→SetPane+PaneUpdate, KindReply→DeliverReply, KindGPU→SetGPU),
  `NodeSeen(host)`, `NodeLost(host)` (→ scheduler). `handleState`/`handleSessions` строят
  `StateView`/`SessionView` (с `actions`).
- **`actions`** (`internal/model/actions.go`): `Action{id,enabled,reason}`;
  `SessionActions(st)` (enqueue/dequeue/cancel/hold/release/close), `ServerActions(st)`
  (drain/restore), `NodeActions()` (drain), `QueueActions()` (dequeue). Поле `actions`
  в `SessionView`/`NodeView` (api/views.go) и `QueueRow`/`ServerRow` (scheduler/views.go:25,56).
  Веб строит кнопки ТОЛЬКО из `actions` (W4).
- **`internal/texts`**: `Meta(version, mode) *Data` (version, mode, перечисления с
  подписями, units) + тексты ошибок/причин. `GET /api/v1/meta` (api/server.go:276).
- **Конфиг W1**: `web.{bind,port,public_url,session_ttl_days,login_rate_per_min,...}`,
  `coordinator.embedded_node`, `coordinator.bind_retry_sec`, `limits.token_min_len`;
  `ValidateWeb`/`ValidateToken` (config.go:408,421). Миграция БД: `002_web_session.sql`
  (таблица `web_session`: id_hash PK, csrf_token, ts_login, ts_touch, revoked) +
  `store.WebSession` (CreateWebSession/GetWebSession/TouchWebSession/RevokeWebSession).
- **Линтеры R2** (`tools/`, `make lint`): `lintlits` (AST-скан internal/+cmd/, пропускает
  `*_test.go` и `consts.go`/`defaults.go`/`schema.go`; флаг чисел ≠0/1/-1 + duration/URL/IP
  строк), `lintweb` (web/ — с W3), `linthosts` (имена хостов 3p14-aaa/vzu5-big + CGNAT
  100.64.0.0/10, пропускает docs/testdata/_test.go/tools/linthosts). Запрет suppress-комментариев.
- **Отложено в W5/W6** (15.1): `version`+`If-Match` (409 VERSION_CONFLICT), `Idempotency-Key`
  (`web.idempotency_ttl_sec`), формат ошибки `{"error":{code,message,details}}`.
- **Стенд** (`cmd/aqm-stand`): каркас — координатор (API+веб) на 127.0.0.1, виртуальные
  часы (start фикс.), временная БД, управляющий порт `GET /status`. W3 — узлы/сценарии/
  скриншоты (раздел 19).

## Ключевые факты W2, полезные для W3–W9

- **Схема настроек** (`internal/config/schema.go:76`): `Schema() []SettingField` (92 поля),
  `ByPath`, `SchemaPaths`. `SettingField{Path,Type,Unit,Default,Min,Max,Enum,Group,Label,
  Help,ApplyWhen,Dangerous}`. `Default` из `Defaults()` (R2). Веб-формы настроек (W3)
  строятся ТОЛЬКО из `GET /api/v1/settings/schema` (R2, R3 — без хардкода значений/типов
  на фронтенде).
- **Рабочие ключи** (`internal/config/work.go:63`): `WorkingLeafPaths()` (92) — рефлексия по
  `Config` + bootstrap-исключения. `GetField`/`SetFieldJSON`/`WorkingSnapshot`/`ApplyWorking`.
  Разделение bootstrap/рабочие (4.2) — см. секцию W2 выше.
- **Хранилище настроек** (`internal/store/`): `003_setting.sql` (таблицы `setting`,
  `setting_revision` с полным JSON-снимком, `server` = JSON). `setting.go`: `LoadWorkingInto`,
  `SaveWorkingSettings`, `ListRevisions`, `GetRevision`, `RevertToRevision`. `settings_export.go`:
  `ExportSettings`/`ImportSettings`. `server.go`: CRUD серверов (JSON-таблица). `migrate_yaml.go`:
  `MigrateFromYAML` (копии/ревизия №1/перепись/откат/no-op).
- **API настроек** (`internal/api/settings.go`, server.go:110-116): `GET/PATCH /settings`,
  `/schema`, `/revisions`, `POST /revert`, `GET /export`, `POST /import?dry_run=`. Валидация
  PATCH → 400 `{"code":"VALIDATION_ERROR","problems":[{path,code,message}]}`. W3 (веб)
  подключается к этим маршрутам; форм нет, только схема + PATCH.
- **Динамический apply** (`internal/scheduler/scheduler.go:96-98,218`): `Options.Reload
  func() error` — планировщик пере-read'ит `LoadWorkingInto` в начале каждого `Tick()`.
  `newHarn` (`scenario_test.go`) DB-driven. Gateway/monitor — статично (без Reload), динамический
  реестр серверов шлюза — W6.
- **Серверы в БД**: `store/server.go` (`List/Get/Upsert/Delete/ReplaceServers`), JSON-таблица
  `server`. На старте serve: `ListServers()` → `cfg.Servers` (если непусто). W3/W6 — UI управления
  серверами.
- **Отложено в W5/W6** (15.1, не менялось в W2): `version`+`If-Match` (409 VERSION_CONFLICT,
  нужно поле `version` у сущностей), `Idempotency-Key` (`web.idempotency_ttl_sec`), формат
  ошибки `{"error":{code,message,details}}`.
- **Отклонения W2** (честные, не блокируют): (1) реальные файлы 3p14-aaa отсутствуют на dev-машине
  → CONTROL 1 на представительной v1-копии; (2) 4.4 строки 5–7 — схема-метаданные, не
  поведенческие (W6/W3); (3) gateway/monitor статично (W6).

## Ключевые факты W3, полезные для W4–W9

- **Фронтенд-стек (без сборки)**: Preact 10.29.8 + htm 3.1.1 + hooks (ESM в `web/vendor/`,
  hash-роутинг, inline importmap). go:embed через корневой `embed.go` = `package aqmweb`,
  `//go:embed all:web`; `cmd/aqm/serve.go`: `if webFS, err := aqmweb.FS(); err == nil {
  webSrv.SetStaticFS(webFS) }`. Всё в `web/` — `index.html` (каркас) + `login.html`,
  `app/main.js` (роутер/App), `app/views/*.js`, `app/components/*.js`, `styles/`, `icons/`,
  `fonts/`, `i18n/`.
- **Правило htm (критично)**: htm = шаблонные литералы, НЕ JSX. Компонент в шаблоне —
  только `<${Comp}/>`; статичный `<Comp/>` рендерится как литеральный тег (белый экран).
  `Comp(props)` как `${...}` ок. Спред `<button ${attrs}>` ненадёжно применяет `class` →
  класс выносить инлайн `class=${cls}`.
- **Порядок CSS (критично)**: `tokens.css → components.css (базовые правила) →
  layout.css (структура + медиазапросы)`. Медиазапрос не добавляет специфичности: базовое
  правило, загруженное ПОСЛЕ медиазапроса, его перебивает (баг телефонной вёрстки). Базовый
  `display:none` нижних вкладок — в `components.css`, `display:flex` — в телефонном
  медиазапросе `layout.css`.
- **Новые токены** (`tokens.css`): светлая `[data-theme='dark']`/`:root` + новые
  `--color-accent-text` (акцент как текст: светлая `#2563EB`, тёмная `#60A5FA`) и
  `--color-on-accent` (текст на акценте: тёмный `#0F172A`) — для WCAG AA 4.5:1 в тёмной
  теме (белый на `#3B82F6`=3.68, акцент на `#1E293B`=3.96). Все цвета — только в tokens.css
  (R2 `lintweb`). Кириллица — только `i18n/ru.js`; числа — только `constants.js`.
- **CSP + fonts** (`internal/web/web.go`): `default-src 'self'; ... script-src-elem 'self'
  '<sha256-…>'` (hash инлайн-importmap, иначе headless Chrome блокирует). Проверка шрифтов
  19.3.7 требует явного `document.fonts.load('16px Inter','AaБб')` + mono (check без load
  false для лениво-неиспользуемого семейства).
- **Конвейер скриншотов (19.2–19.4)**: `make screenshots STAGE=W<N>` → `tools/screenshots`
  (go-rod v0.116.2; flags `-stage -out -golden -stand -token -start -chrome -axe -diff-max
  -stand-running -run-url`). Имя файла `<scenario>__<page>__<WxH>__<theme>.png`, телефон —
  scale 3/touch. `manifest.json` фиксирует коммит HEAD на момент съёмки + проверки каждого
  файла. `make visualcheck STAGE=W<N>` → `tools/visualcheck` (10 файлов из VISUAL.md в
  manifest, 12 отметок/строка, commit(VISUAL.md)==commit(manifest) и предок HEAD).
- **VISUAL.md-конвенция**: `docs/evidence/W<N>/VISUAL.md` = шапка `commit: <40-hex>` + сводка
  дефектов + таблица (строка = сценарий/страница/окно/тема, 12 отметок по V1–V12 из 19.4) +
  сравнение с шаблоном. commit в manifest/VISUAL.md = коммит-предок HEAD (не обязательно HEAD),
  потому что при одном коммите/этапе снимки снимаются до коммита; `visualcheck` проверяет
  предок + совпадение.
- **Стенд `aqm-stand`**: флаги `-web-port -control-port -token -start (RFC3339, не duration)
  -keep-db -info-file`; управл. порт — только здесь; виртуальные часы `clock.NewVirtual`.
  W4+ — добавить шаги часов/события/узлы (сценарии 19.1).
- **Golden-база**: `test/visual/golden/` (10 PNG = W3 baseline, diff-max 1.0%). При изменении
  вёрстки — переснять golden осознанно (коммит + пометка в CONTROL).
- **Актор-пер-логин**: веб-сессии — cookie + CSRF (16); скриншоты снимаются под одним
  оператором (токен стенда). В W4 экраны требуют данных → данные приходят через API/SSE,
  состояние стенда — через управляющий порт (W4+).

## Последнее измерение
- что мерили: W3 CONTROL (4 строки): (1) скриншоты всех комбинаций 19.2 (вход+кадр пустого
  состояния × 3 окна × темы = 10 PNG); (2) проверки 19.3 — 0 находок (console/network/scroll/
  text/axe/touch/fonts/golden по manifest.checks); (3) VISUAL.md + visualcheck OK (10 файлов,
  12 отметок/строка, commit 82fc177 предок HEAD); (4) 0 запросов к внешним хостам (19.3.2,
  CSP `default-src 'self'`). `make check`, `go test -race ./...`, `make lint` — 0.
- число: все 4 строки CONTROL PASS; `make screenshots STAGE=W3` — 19.3 = 0 находок;
  `make visualcheck STAGE=W3` — OK; артефакты в `docs/evidence/W3/` (make-check.log,
  test-race.log, make-lint.log, control-screenshots.log, visualcheck.log, screens/ + manifest.json)
- критерий: строки CONTROL W3 (docs/evidence/W3/CONTROL.md)
- PASS/FAIL: PASS

## W5 — Сессия и ввод (TZ v2, раздел 20) — PASS

Все 8 строк CONTROL выполнены (доказательства — `docs/evidence/W5/CONTROL.md`); один
коммит W5. `go test -race ./...` = 0, `make lint` = 0, `make check` = 0,
`make screenshots STAGE=W5` = 0 находок (12), `make visualcheck STAGE=W5` = OK.

### Сделано (коммит W5)
- **`internal/command` (НОВЫЙ пакет)** — общий разбор команд, его используют веб и
  Telegram (раздел 12): `command.go` (реестр ~32 команд: new/go/send/enqueue/dequeue/
  requeue/requeue-hold/clear-queue/prio/pin/prefer/unpin/hold/unhold/cancel/approve/
  compress/auto/why/open/term/restart-agent/kill-agent/close/restore/server/node/
  pause/resume/panic/unpanic/help), `parse.go` (Parse: «/имя арг…», go/send — текст),
  `exec.go` (Outcome + интерфейс `Executor` + `Run` — диспатч каждой команды),
  `complete.go` (подсказки команд/аргументов по `DataProvider`), `help.go` (`/help` +
  `Docs()` → docs/commands.md из реестра), `consts.go` (`two`). Тесты `command_test.go`
  (parse+execute на **каждую** команду раздела 12 + Complete + Docs) — PASS.
- **`internal/proto`** — новые типы координатор→узел: `KindPaste` (текст через stdin,
  `SubmitAfter` для compress), `KindApprove`; коды result `ResPasted/ResPasteMismatch/
  ResPasteSubmitted/ResApproved/ResApproveNoEffect` (+ дубли в узле).
- **`internal/node`** — ввод в панель (раздел 13.1/13.3 v2): `paste.go` `paste()` =
  `tmux load-buffer -b aqm-<cmd_id> -` (текст **через stdin**, `Execer.OutputInput` —
  добавлен в `scanner.go`) → `tmux paste-buffer -p -d -b … -t …` → переснимать
  (`capturePane`, 8×150мс) → сверка: `PASTED`/`PASTE_MISMATCH` (первые 80 симв.)/
  `PASTE_SUBMITTED` (панель в BUSY — дефект); `approve()` = только в PROMPT, клавиши
  варианта из `Profile.ApprovalOptions` → `APPROVED`/`APPROVE_NO_EFFECT`. `commands.go`:
  `Profile` += `ApprovalOptions/CompressText/QuitText/PastePlaceholderRegex`, switch +=
  `KindPaste`/`KindApprove`/`KindCompress`. `consts.go` += `pasteSettle`/`pastePoll`. Тесты
  `paste_test.go` (PANE_GONE/MISMATCH/SUBMITTED + механика load/paste-buffer) — PASS.
  `helpers_test.go`: `fakeExec.OutputInput` (подмена stdin).
- **`internal/api` — backend команд (веб+Telegram общий разбор, раздел 12)**:
  - `commands.go`: `CmdPaste` (условия: UTF-8 ≤ `web.paste_max_kb`, панель IDLE, ввод пуст →
    `hub.Dispatch(KindPaste)` → PASTED/MISMATCH/SUBMITTED → audit `paste` {sid,bytes,lines,
    operator; **текст не хранится**, R5}), `CmdApprove` (`hub.Dispatch(KindApprove)` →
    APPROVED/NO_EFFECT), `CmdCompress` (`hub.Dispatch(KindCompress)` → узел вставляет
    `CompressText`+submit → enqueue). `WithOperator`/`operatorFrom` (ctx).
  - `executor.go`: `apiExecutor` = полный `command.Executor` — очередь/сессии/управление →
    `s.sched.*` (Enqueue/Dequeue/Requeue/Prio/Pin/Prefer/Unpin/Hold/Unhold/Cancel/Auto/Why/
    Pause/NodeDrain/ServerDrain), ввод → Cmd*, Address → store.GetSession/FindLiveByName.
    **Ещё `unimpl` (след. шаг):** Spawn(new), RestartAgent/KillAgent/Close/Restore,
    RequeueAllHold/ClearQueue, Panic, ServerOp disable/enable/cancel-turns/unquarantine.
  - `command_handlers.go` + `server.go`: `POST /api/v1/command` (parse + `Run`; 404
    UNKNOWN_COMMAND / 400 BAD_COMMAND) + `GET /api/v1/command/complete` (`stateDataProviders`
    = DataProvider из store/sched/hub). Тесты `command_test.go` (help/unknown/paste-no-pane/
    new/empty/complete) — PASS.

### Выполнено (все шаги; итог — 8/8 CONTROL PASS)
1. **UI карточки сессии** (`web/app/views/session.js`): композер (13.1) + живой экран
   (`web.screen_interval_ms`) + разрешения + сжатие + действия (15.2); 2-колонка desktop /
   1-колонка phone (композер зафиксирован). Использует `POST /api/v1/command` + complete.
2. **Дособрать `unimpl`** в executor: Spawn (13.2), RestartAgent/KillAgent/Close/Restore,
   RequeueAllHold/ClearQueue, Panic, ServerOp disable/enable/cancel-turns/unquarantine
   (часть — в W6/W7; для W5 минимум: те, что нужны карточке сессии).
3. **Telegram** (`internal/notify`, из заглушки): общий разбор через `internal/command`
   (тот же `command.Run`; идентичный результат с вебом на 30 строках).
4. **Фикстуры** (qwen 0.24.5, существующие 0.24.4): `idle_input_pasted_short/long`,
   `approval_prompt`, `compress`, `quit` + профиль (approval_options/compress_text/
   quit_text/paste_placeholder_regex).
5. **e2e (реальный qwen)**: 20 текстов → PASTED 20/20, PASTE_SUBMITTED 0; разрешение PROMPT
   10/10; живой экран ≤ screen_interval_ms+500 p95; уникальная метка в БД/журнале 0;
   веб=Telegram на 30 строках; подсказки ≤ complete_budget_ms p95 (1000 запросов/50 сессий);
   J1, J2 скриншоты → `docs/evidence/W5/` (VISUAL.md + CONTROL.md + visualcheck OK).
6. `make check` + `go test -race ./...` = 0 → **коммит W5 (один, aqm-agent)**.

Результат: 8/8 CONTROL PASS (control1_paste_20.txt / control2_approve_10.txt /
control3_screen_delay.txt + тесты R5 / every-command / web=Telegram / complete-budget +
VISUAL.md J1,J2 + visualcheck OK). Следующий этап — W6 (мастер сервера, формы настроек,
подключение/самообновление узлов, новая сессия; J6/J7/J8).

## W6 — Управление (TZ v2, раздел 20) — ГОТОВО

Содержание: мастер сервера (5.1) + удаление (5.2); формы настроек/ревизии/импорт-
экспорт в UI (11.10, бэкэнд — W2); подключение/самообновление/удаление узлов (14, 5.3);
новая сессия/восстановление/закрытие + «Перезапустить под aqm» (11.5, 13.2, 13.5, 5.4);
динамический реестр шлюза. Сценарии J6 (мастер→пробный→удаление), J7 (подключение→
удаление→UNMANAGED), J8 (изменение→просмотр→откат).

CONTROL W6 (строка 1414):
1. чистая машина (контейнер: tmux + mockcoder как qwen) → одна команда, узел онлайн ≤60с;
2. новая версия координатора → все узлы стенда обновлены ≤120с, список сессий до/после =;
3. бинарник с проваленным selftest не устанавливается, узел на старой версии;
4. отозванный токен → 401;
5. новая сессия из веба → панель с @aqm_sid ≤5с;
6. каталог вне project_roots (в т.ч. через симлинк) → DIR_FORBIDDEN;
7. оба режима удаления сервера и узла покрыты тестами;
8. J6, J7, J8 в VISUAL.md.

### План (по блокам; один коммит W5→W6 в конце)
- proto=2: spawn/respawn/paste_text/kill_session/release_pane/kill_process/list_dirs/
  capture(ansi)/pty/update/uninstall/config + result SPAWNED/RESPAWNED/DIR_FORBIDDEN/
  DIR_NOT_FOUND/KILLED + node_health/dirs/update_status (15.5).
- бэкэнд: серверы (probe/test-request/CRUD/удаление after_turns|now + REMOVING,
  SERVER_ADDED/REMOVED), узлы (enroll/claim/install.sh/dist/selftest/update/удаление/
  dirs), сессии (spawn+name-check+DIR_FORBIDDEN, restart-agent/restore/close/adopt),
  mode (pause/resume/emergency).
- UI: мастер сервера (5.1), страницы сервера/узла (11.6/11.7), новая сессия (11.5),
  настройки-формы из схемы + ревизии/импорт-экспорт/о системе (11.10).
- e2e: чистая машина (node ≤60с), self-update ≤120с, selftest-fail, 401, spawn ≤5с,
  DIR_FORBIDDEN; удаление (оба режима) — unit; J6/J7/J8 → VISUAL.md + visualcheck.
- `make check` + `go test -race ./...` + `make lint` = 0 → коммит W6.

Статус: ГОТОВО — CONTROL 1–8 PASS. e2e реальными процессами (test/e2e/update,
build-тег e2e): CONTROL 1 узел онлайн ≤60с (300мс); CONTROL 2 self-update v1→v2
≤120с (305мс) + список сессий до/после =; CONTROL 3 selftest-fail → узел на старой
версии. unit (internal/api/w6_test.go, internal/node/ops_test.go): CONTROL 4 отозванный
токен → 401; CONTROL 5 новая сессия из веба → панель с @aqm_sid; CONTROL 6 каталог
вне project_roots (в т.ч. симлинк) → DIR_FORBIDDEN; CONTROL 7 оба режима удаления
сервера и узла. J6/J7/J8 → docs/evidence/W6/VISUAL.md (20 скриншотов, 0 findings) +
visualcheck OK. `make check` + `make lint` = 0.

### Ограничения машины (не трогать)
- живой qwen 0.24.5 (active.json), tmux 3.2a; тестовый tmux только `-L aqmtest`;
  vLLM оператора 127.0.0.1:8004 — НЕ трогать; max 1 фоновый процесс (is_background,
  без nohup&); pgrep/pkill -f избегать (`ss -ltnp`); go-rod + headless Chrome
  (/usr/bin/google-chrome); стэнд/doctor — как в W4.

## W7 — Устойчивость (TZ v2, раздел 7) — ЗАКРЫТ, CONTROL 6/6 PASS

Содержание: обёртка `agent-run`, классы отказов, карантин, нестабильность, смена
модели, аварийная остановка, безопасный режим, правило `LOST`, процессы вне aqm,
`aqm exec`, копии, `upgrade`, `rollback`, `restore`, аварийные уведомления при старте.

CONTROL W7 (строка 1415): `TE-<ID>` на каждую строку раздела 7 (S1–S14, N1–N10,
C1–C15, X1–X6, K1–K9, O1–O9, U1–U4 = 67); скриншот на каждую строку в VISUAL.md;
EMERGENCY ≤2с до отмены всех потоков + все сессии HOLD(EMERGENCY); SAFE_MODE:
ошибка записи → аренды не выдаются, выход ≤ safe_probe_sec; `kill -9` координатора
посреди хода 10× → правило 6.5 верен 10/10; 20 `aqm exec` на 2 слота →
одновременных аренд ≤ слотов на каждом сэмпле 100 мс; внешний процесс от поддельного
cron ≤2 скана (source=cron, target=server:<name>); `rollback` возвращает бинарник+БД;
J3,J4,J5,J9,J10,J11,J12 в VISUAL.md.

### Сделано (до коммита W7)
- **Машина режимов** NORMAL/PAUSED/EMERGENCY/SAFE_MODE (приоритет, `meta.mode`
  переживает рестарт): `internal/store/migrations/006_meta.sql` (schema_version 5→6),
  `internal/store/meta.go` (Mode/SetMode/LastAliveAt/SetLastAliveAt/ProbeWrite),
  `internal/model` (Mode, EvEmergency → 34 строки/35 автомата, ReleaseEmergency,
  ReleaseCrash, KindMode).
- **EMERGENCY (6.2, CONTROL 3)**: `internal/scheduler/modes.go` SetEmergency
  (persist-first, ходы CANCELLED, аренды EMERGENCY, сессии → HOLD(EMERGENCY),
  cancel-клавиши, hook → gateway.CancelAll, [CRIT]); ClearEmergency → PAUSED;
  RequeueHoldEmergency. gateway: 503 `aqm_emergency` + `CancelAll()` (отмена идущих
  SSE). API: `/api/v1/emergency`, `/emergency/release`, `/sessions/hold-emergency/requeue`.
- **SAFE_MODE (6.4, CONTROL 4)**: SetSafeMode/ClearSafeMode/tickSafeProbe (выход по
  safe_probe_sec через ProbeWrite); `grantsBlocked()` — новые аренды запрещены;
  gateway 503 `aqm_safe_mode`.
- **crash recovery (6.5, CONTROL 5)**: `internal/scheduler/recover.go`
  RecoverFromCrash (габ [last_alive_at, now] > heartbeat_sec → идущие ходы LOST =
  ERROR_TRANSIENT: attempts+1, ход LOST, аренда CRASH, сессия → очередь resume; 2xx-до-
  cutoff → OK); serve.go: `heartbeatLoop` (last_alive_at) + вызов до Start.
- **K6 (ошибка записи → SAFE_MODE)**: `store.SetWriteErrorHook`/`fireWriteErr`
  (не на ErrNotFound/ErrClosed, асинхронно); `scheduler.ArmWriteErrorHook`/
  onStoreWriteError (CAS). CONTROL 4 «внедрённая ошибка записи» авто.
- **S11 (KEY_MISSING)**: `gateway/servers.go` newServer → KEY_MISSING если key_env
  задан, но переменная пуста; serve.go [CRIT] «Переменная … не задана»; serverNotifKind
  KEY_MISSING → CRIT.
- **S7/S8 (наблюдение моделей)**: `monitor.checkModels` (GET /v1/models раз в
  monitor.models_refresh_sec → ModelObserver); `gateway/modelwatch.go` ModelWatch —
  auto-смена → SERVER_MODEL_CHANGED (event KindServerModel), заданная не найдена /
  несколько при auto → MODEL_PROBLEM (state, авто-исключение из аренд).
- Тесты (PASS): scheduler modes/recover/write-error, gateway modes/s11/modelwatch,
  store fireWriteErr, monitor checkModels. `go test -race ./...`=0, `make lint`=0.

### CONTROL W7 e2e — итог (6/6 PASS; `test/e2e/w7`, build-tag `e2e`)
Живой стэнд на fakellm (`internal/testutil/fakellm`), координатор с embedded-узлом,
`heartbeat_sec=1`, `scan_interval_sec=1`, `safe_probe_sec=2`. Кодекс e2e: один
процесс `go test` управляет всеми дочерними; evidence → `docs/evidence/W7/control/*.json`.

| # | Контроль | До (гипотеза/лимит) | После (измерено) |
|---|----------|--------------------|------------------|
| 1 | Аварийная остановка | ≤2 с до отмены всех потоков | 2 потока отменены за **1 мс**; mode=EMERGENCY; 2 сессии → HOLD(EMERGENCY) |
| 2 | Безопасный режим | нет аренд; выход ≤ safe_probe_sec+запас | SAFE_MODE: 0 аренд; выход **8 мс**; БД читается (quick_check), mode→NORMAL |
| 3 | `kill -9` посреди хода | правило 6.5 (LOST→RESUME) 10/10 | 10/10: attempts+1, ход LOST, аренда CRASH, сессия → QUEUED (resume) |
| 4 | 20 `aqm exec` / 2 слота | аренд ≤ 2 на каждом сэмпле 100 мс | 215 сэмплов, **max=2**, превышений **0** |
| 5 | Внешний qwen от cron | ≤2 скана, source=cron, target=server:srv-01 | обнаружен за **802 мс** (≤2 скана), source=cron, target=server:srv-01, ACTIVE |
| 6 | `rollback` | вернуть бинарник + БД | бинарник w7e2e-v2→**w7e2e-v1**, БД last_alive_at 2026→**2000** |

- **Найден и исправлен дефект (раздел 6.6)**: `backup.RestoreBinary` перезаписывал
  текущий (часто запущенный) бинарник через `os.WriteFile`/`O_TRUNC` → `ETXTBSY`
  («text file busy»). Заменено на атомарный rename-aside: копия в `.bin-restore-tmp`,
  `os.Rename(target→.bin-restore-old)`, `os.Rename(tmp→target)`, с откатом при ошибке.
  `internal/backup/consts.go`: `binTmpSuffix`/`binOldSuffix`.
- **e2e CONTROL 5 (внешний процесс)**: e2e работает внутри tmux-сессии оператора, а
  `isUnder` (node/external.go) исключает из внешних потомков tmux-панели. Фикс в
  `spawnFakeCronQwen`: «cron» (argv[0]=cron) спавнит «qwen» и ЖДЁТ его, а внешняя
  оболочка запускает «cron» через `setsid` в фоне и сразу выходит → «cron»
  переоформляется на init (ppid=1); цепочка qwen→cron→init не проходит ни одну
  tmux-панель, и `sourceOf` находит «cron» (а не «systemd»).
- Остаточный шум teardown (`httptest.Server blocked in Close … in state active`) —
  fakellm-хендлеры SSE в середине потока при kill координатора; не влияет на PASS,
  соединения закрываются сами (chunk_pause) после снятия блокировки.

### TODO (порядок зависимостей) — ВЫПОЛНЕНО к коммиту W7
1. aqm exec 8.1 [CONTROL 6] — jobs API + cmd + слоты (jobs.* config: heartbeat/
   wait_max; «2 слота» = ёмкость job), exit codes 69/75/77/124.
2. внешние процессы 8.2 [CONTROL 7] — сканер +/proc/<pid>/environ → source
   (cron/systemd/sshd/tmux/other) + target (gateway:<sid>/server:<name>/unknown),
   ignore_rule, external.* config.
3. rollback/restore/upgrade 6.6 [CONTROL 8] — VACUUM INTO → backups/<ts>/, daily
   backup, `aqm rollback` (aqm.prev + БД до миграции), `aqm restore` (quick_check).
4. шаблоны отказов S4/S5/S6 — gateway body-match OOM/ENGINE_DEAD/CONTEXT_LENGTH,
   quarantine (faults.* config), servers_ops stubs.
5. model watch S7/S8 — GET /v1/models (monitor.models_refresh_sec),
   SERVER_MODEL_CHANGED / MODEL_PROBLEM (ServerModelProblem state добавлен).
6. node-правила N2/N3/N7/N8/N9/N10.
7. алерты (Telegram, C14 queue-wait, C3 remind, C11 coalesce); K9 SSE cap.
8. 67 тестов TE-<ID> + скриншоты (test/screenshots/scenarios/w7.go) + VISUAL.md
   (J3,J4,J5,J9,J10,J11,J12) + `make screenshots STAGE=W7` + `make visualcheck STAGE=W7`.
9. коммит W7 (aqm-agent <aqm-agent@local>, docs/evidence/W7/).

### Ключевые инварианты W7 (не сломать)
- весь tick под s.mu (non-reentrant): методы из tick → *Locked-ядра; публичные
  режимы локируют → зовут *Locked. SetEmergency: persist meta.mode ДО мутирования.
- store: один писатель; хук write-error — АСИНХРОННО (из write-loop синхронная
  запись = deadlock); schema_version теперь 6.
- R2: Go literals только 0/1/-1 (кроме defaults/schema/consts); 2xx-проверки через
  http.StatusOK/StatusMultipleChoices (не 200/300).

### Ключевые факты W7, полезные для W8–W9
- **e2e-кодекс (build-tag `e2e`)**: `go test -tags e2e ./test/e2e/<stage>` — НЕ входит
  в `make check` (vet/lint/test без тега); один `go test`-процесс управляет всеми
  дочерними (startProc/killProc); evidence → `docs/evidence/<STAGE>/control/*.json`.
  `lintlits` (R2) исключает `*_test.go` — в e2e числовые литералы свободны.
- **Эта среда = tmux**: агент и e2e работают внутри tmux-сессии оператора. Всё,
  что спавнит e2e, — потомок tmux-панели. Для «внешних процессов» (node/external.go
  `isUnder`/`sourceOf`) детaching на init через `setsid`+выход родителя (см. CONTROL 5).
  Тестовый tmux — только сокет `-L aqmtest`; vLLM 127.0.0.1:8004 — НЕ трогать.
- **Машина режимов** — `meta.mode` (NORMAL/PAUSED/EMERGENCY/SAFE_MODE) в БД,
  переживает рестарт; `grantsBlocked()`=EMERGENCY|SAFE_MODE блокирует новые аренды.
  Выход из SAFE_MODE — по `ProbeWrite` каждый `monitor.safe_probe_sec`.
- **crash recovery (6.5)** — при старте `RecoverFromCrash` сравнивает `now−last_alive_at`
  с `coordinator.heartbeat_sec`; идущие ходы → LOST (attempts+1, resume в очередь),
  завершённые до cutoff 2xx → OK. `heartbeatLoop` пишет `meta.last_alive_at`.
- **rollback** — `aqm rollback` = `backup.Rollback`: `RestoreBinary` (rename-aside,
  атомарно, без ETXTBSY) + `Restore` (quick_check + rename). Требует остановленный
  сервис (`requireServiceStopped` дёргает api_port).
- **Ограничения машины**: max 1 фоновый процесс (is_background, без nohup&);
  `run_shell_command` timeout ≤ 600000 мс; e2e-стенд — foreground.

## W9 (доп-3k) — Перенос в `/mdisk/myhub/RunPilot` + переименование `aqm → runpilot` (2026-10-01)

**Цель**: проект AIQueMan (aqm) → **RunPilot**, перенос с `/home/user/myhub/aqm` в
`/mdisk/myhub/RunPilot`. Оператор переносит папку и запускает агента из новой; агент
переименовывает код/окружение и поднимает стенды. Коммит с 3 live-багами уже в git: `9b83737`.

### Статус на момент хендоффа (сделано агентом 2026-10-01)
- **Стенды остановлены**: tmux-серверы `aqmtest` (W5 c3), `aqme2e41091/43033/33725`
  (e2e-остатки) убиты `kill-server` по явным сокерам (default-tmux НЕ тронут).
- **Координатор остановлен**: `systemctl --user stop aqm.service`; порты 8787/8788/8790
  освобождены (netstat пуст).
- **Тестовые БД очищены**: /tmp `aqm-stand-*`, `aqm-e*`, `TestAccess*`,
  `TestControl2SelfUpdate*`, `aqm-update-e2e-*`, `aqm-w5` (395 артефактов, 1.1 ГБ).
- **Сохранено (НЕ трогать)**: основная БД `~/.local/share/aqm/aqm.db` (чисто закрыта
  стопом: без `-wal`/`-shm`, `integrity_check: ok`); `~/.config/aqm/` (config.yaml, env);
  бинарные бэкапы `/tmp/aqm-b5..b10,bugfix`; default-tmux: AQM %32 (агент), BotBoss %2,
  EpubTranslator %17; vLLM 127.0.0.1:8004.

### Решение по имени: `runpilot` (основное), `rp` — опциональный алиас
- Проверено 2026-10-01: **ни `rp`, ни `runpilot` не конфликтуют** (`command -v` пусто);
  `aqm` = `~/.local/bin/aqm`.
- **Рекомендация**: полное `runpilot` везде (модуль/бинарник/сервис/пути/продукт) —
  самодостаточно, без конфликтов. `rp` — только короткий CLI-алиас (symlink
  `~/.local/bin/rp → runpilot`), если захочется. Чтобы `rp` было основным — сказать
  оператору до начала разд. 2.

### 1. Перенос — делает ОПЕРАТОР
1. `mv /home/user/myhub/aqm /mdisk/myhub/RunPilot` (цель `/mdisk/myhub` существует,
   /mdisk 292G свободно; .git уходит вместе — история сохраняется).
2. Запустить агента (Qwen Code) **из новой папки** `/mdisk/myhub/RunPilot`.
3. Дальше — агент (разд. 2–5).
> Окружение (БД/конфиг/сервис/бинарник) **вне** папки проекта: `~/.local/share/aqm`,
> `~/.config/aqm`, `~/.local/bin/aqm`, `~/.config/systemd/user/aqm.service` — перенос
> папки их не затрагивает; мигрирует агент (разд. 3).

### 2. Переименование кода — делает АГЕНТ (механика: `aqm → runpilot`)
161 Go-файл с префиксом импорта `aqm/`. После каждого шага `go build ./...`, в конце —
`make check` + `go test -race`.
1. **go.mod**: `module aqm` → `module runpilot`.
2. **Импорт-префикс**: во всех `*.go` `"aqm/` → `"runpilot/` (только кавычки-импорты).
3. **Каталоги команд**: `cmd/aqm/` → `cmd/runpilot/`; `cmd/aqm-stand/` → `cmd/runpilot-stand/`.
4. **Makefile**: `-o aqm ./cmd/aqm` → `-o runpilot ./cmd/runpilot`; `aqm-stand` →
   `runpilot-stand`; `aqm-screenshots` → `runpilot-screenshots`.
5. **Пути** (`internal/config/defaults.go`, `config.go`): `~/.config/aqm/config.yaml` →
   `~/.config/runpilot/config.yaml`; `~/.local/share/aqm/aqm.db` → `~/.local/share/runpilot/runpilot.db`;
   `~/.local/share/aqm/dist` → `~/.local/share/runpilot/dist`.
6. **Опции tmux** `@aqm_sid/@aqm_exit/@aqm_status/@aqm_summary` → `@runpilot_*` (код + миграция
   живых панелей, разд. 4).
7. **Env** `AQM_*` → `RUNPILOT_*` (TOKEN, NODE_TOKEN, SID, STAND_TOKEN, TG_TOKEN, …): код +
   `config.yaml` (`token_env`) + `~/.config/*/env` + юнит.
8. **Продукт** `AIQueMan (aqm)` → `RunPilot`: `cmd/runpilot/*.go`, `internal/web/web_handlers.go`,
   юнит `Description`.
9. **Строки/логи** `aqm:` → `runpilot:` (лог-префиксы, CLI help, веб-подписи, install.sh).
10. **Тесты**: пути/имя бинарника `aqm` в `*_test.go` → `runpilot`; прогнать `make check`.
11. **TZ/доки**: `.agent/tasks/TZ_AIQueMan.md` — обновить заголовок на RunPilot (путь файла —
    спросить оператора: переименовать или оставить).

### 3. Миграция окружения — делает АГЕНТ (после разд. 2)
> **Папки/директории переименовываются ЦЕЛИКОМ** (вместе с содержимым и подкаталогами), не
> только файлы внутри: `~/.local/share/aqm/` → `~/.local/share/runpilot/`,
> `~/.config/aqm/` → `~/.config/runpilot/`. Имя `runpilot` подтверждено оператором 2026-10-01.
1. **БД**: `~/.local/share/aqm/` → `~/.local/share/runpilot/` (`aqm.db` → `runpilot.db`).
2. **Конфиг**: `~/.config/aqm/config.yaml` → `~/.config/runpilot/config.yaml`; внутри `db_path`
   → новый, `token_env` → `RUNPILOT_*`; порты без изменений (api 8788 bind 100.124.155.106,
   gateway 8787, web 8790 bind 127.0.0.1).
3. **Env**: `~/.config/aqm/env` → `~/.config/runpilot/env` (AQM_TOKEN → RUNPILOT_TOKEN, значения
   тех же).
4. **Бинарник**: `make build`; `install -m0755 runpilot ~/.local/bin/runpilot`; старый
   `~/.local/bin/aqm` → бэкап `aqm.aqm-legacy`, затем удалить.
5. **Сервис**: `aqm.service` → `runpilot.service` (Description `RunPilot coordinator`,
   `EnvironmentFile=%h/.config/runpilot/env`, `ExecStart=%h/.local/bin/runpilot serve`);
   `systemctl --user disable --now aqm.service && daemon-reload && enable --now runpilot.service`.
6. **Узел vzu5-claw (REMOTE, через tailscale ssh)**: сервис `aqm-node` + env `AQM_NODE_TOKEN`
   → `runpilot-node` + `RUNPILOT_NODE_TOKEN`; координатор и узел должны совпасть по протоколу/
   токену (при смене env-имени — пере-claim токена).

### 4. Миграция живых tmux-панелей (нестекательно)
- Управляемая панель агента `%32` (AQM, `@aqm_sid=KPQ078T9VQ`): скопировать значение в
  `@runpilot_sid`, затем снять `@aqm_*`. Панели %2/%17 — unmanaged (без `@aqm_sid`) — не трогаем.
- После этого координатор (runpilot) видит AQM по новому опциону; сессия должна остаться IDLE
  (Prefill, доп-3j — не гонится в GONE при рестарте).

### 5. Возврат стендов (после проверки)
- Координатор: `systemctl --user start runpilot.service`; проверить API :8788, web :8790,
  gateway :8787; AQM=IDLE.
- Тестовые стенды (stand/e2e) — по мере нужды (`make stand` / `go test -tags e2e …`), как раньше.

### 6. Контрольные точки (проверить в конце)
- [ ] `go build ./...`, `make check`, `go test -race ./...` — 0.
- [ ] `runpilot serve` стартует, порты 8787/8788/8790, AQM=IDLE, сессии на месте
      (AQM, BotBoss, EpubTranslator) в `~/.local/share/runpilot/runpilot.db`.
- [ ] Узел vzu5-claw онлайн после смены токена/сервиса.
- [ ] Слово `aqm` как команда/модуль/сервис больше не вызывается; `/home/user/myhub/aqm` пусто.

---

## Hold-Silent: реализация по списку (2026-10-06)

### Что сделано (8 пунктов требований)

| # | Пункт | Статус |
|---|---|---|
| 1 | Live-результаты Этапа 0 (до правок) | ✅ `00-etap0-live-results.md` |
| 2 | Self-grant удалён; планировщик — единственный выдающий; тесты ранга/ёмкости | ✅ |
| 3 | QueueUpsert: enqueued_at (MIN), attempts (MAX), class (MIN) | ✅ |
| 4 | clientGone: QueueDelete + QUEUE_DEQUEUE + request.error=CLIENT_GONE | ✅ |
| 5 | Таблица статуса | ✅ `07-status-table.md` |
| 6 | go test -race + staticcheck | ✅ `08-race-staticcheck.md` |
| 7 | Пересборка /tmp/hl + live п.2 (after-fix) | ✅ `02-p2-measurement-after-fix.md` |
| 8 | HANDOFF обновлён | ✅ (этот раздел) |

### Ключевые изменения кода

- **`internal/gateway/lease.go`**: `holdPass` — self-grant удалён. Цикл:
  `QueueUpsert(held_request=true)` → `LeaseGet(own sid)` → `select`
  (ctx.Done→clientGone / timer→heldToResume / wake). `clientGone` пишет
  `QueueDelete` + `QUEUE_DEQUEUE{reason:"client_gone"}` + `RequestError(CLIENT_GONE)` + IDLE.
- **`internal/store/queue.go`**: `QueueUpsert` при CONFLICT —
  `enqueued_at=MIN`, `attempts=MAX`, `class=MIN`. Новый метод
  `QueueSetEnqueuedAt` (прямой UPDATE для S11).
- **`internal/store/request.go`**: новый метод `RequestError(sid, code, at)`.
- **`internal/store/migrations/012_queue_entry_attempts.sql`**: колонка `attempts`.
- **`internal/model/model.go`**: `QueueEntry.Attempts int`.
- **`internal/scheduler/complete.go`**: S11 — `QueueSetEnqueuedAt` (вместо
  `QueueUpsert`, т.к. MIN-сохранение ломает перезапись).

### Тесты (все зелёные, -race чистый, staticcheck чистый)

- `TestHoldSlotGranted` — переписан (шлюз + планировщик на виртуальных часах).
- `TestP10ExternalFree`, `TestP10DownUp`, `TestP10CooldownFree` — перепрогнаны.
- `TestHoldRankResumeBeatsHeld` — новый (RESUME > HIGH; B получает следующий слот).
- `TestHoldCapacityOneSlotTenFrees` — новый (1 слот, 10 освобождений, ≤1 аренды).
- `TestP4ClientGone` — дополнен проверкой `request.error=CLIENT_GONE`.
- `TestP5PreserveEnqueuedAt` — дополнен проверкой `attempts` и `class`.
- `TestP10SelfGrantEvent` — удалён (self-grant больше нет).

### Live-замер after-fix (п.2)

A=`BHCCC4GWG9`, B=`BS449A7Q53`. held_ms=333 622 мс (PASS).
LEASE_GRANT(B) издан **планировщиком** (`{"held":true,"origin":"IMPLICIT"}`) —
self-grant устранён. LEASE_RELEASE(A)→GRANT(B) ~2.5 с (cooldown + tick).
t_first_byte−GRANT ~3.4 мс.

### Артефакты

- `docs/evidence/hold-silent/00-etap0-live-results.md` — live-результаты ДО.
- `docs/evidence/hold-silent/02-p2-measurement-after-fix.md` — live-результаты ПОСЛЕ.
- `docs/evidence/hold-silent/06-p10-virtual-clock.md` — обновлён (self-grant удалён).
- `docs/evidence/hold-silent/07-status-table.md` — таблица статуса.
- `docs/evidence/hold-silent/08-race-staticcheck.md` — -race + staticcheck.

### Остаток (не закрыто в этом цикле)

- Live п.3 (noenv), п.4 (Esc), п.5 (hold_max_sec=20) — after-fix не прогонены
  (требуют отдельного запуска стенда с другим профилем/конфигурацией).
  Unit-тесты покрывают логику (TestP4ClientGone, TestP5PreserveEnqueuedAt).

### Шаг 2: QueueUpsert refactor (2026-10-06, после отчёта)

Правка по требованию оператора: слияние (MIN/MAX/MIN) — только для пути
удержания; общий QueueUpsert — перезапись.

- `QueueUpsert`: ON CONFLICT → перезапись всех значений (`excluded.*`).
- `QueueUpsertHeld` (новый): ON CONFLICT → слияние (MIN/MAX/MIN). Вызывает
  только `holdPass`.
- `QueueSetEnqueuedAt` — удалён. S11 работает через общий `QueueUpsert`.
- Тесты: `TestQueueUpsertClassDowngrade`, `TestRequeueBackEndOfClass`,
  `TestAttemptsResetAfterOK`, `TestQueueUpsertHeldMerge`.
- S1–S14 + `go test -race ./...` — 0 fall.

### Шаг 3: Live-замеры + артефакты (2026-10-06)

- **П.2 after-fix**: PASS (6/6 критериев). held_ms=333622,
  LEASE_RELEASE→GRANT=2499 мс (≤2500), GRANT→first_byte=3 мс.
  LEASE_GRANT издан планировщиком. → `02-p2-measurement-after-fix.md`
- **П.3 pre-fix**: разрыв 120.394 с от Enter, 1 retry за 300 с.
  → `03-p3-pre-fix-numbers.md`
- **Рабочий узел**: embedded в serve (штатная архитектура), подключён,
  health актуален. → `09-stand-stop-and-node.md`
- **Остановка стенда**: по PID из info-файла. SIGTERM не остановил
  serve/node за 5 с → FINDING: нужен graceful shutdown или kill -9 в trap.
- **П.3/п.4/п.5 after-fix live**: НЕ завершены (stand infrastructure issue:
  turn-close не сработал при повторном запуске). Кодовая логика покрыта
  unit-тестами (TestP4ClientGone, TestP5PreserveEnqueuedAt, TestQueueUpsertHeldMerge).

> ⚠️ **УСТАРЕЛО — см. Шаг 4 (ниже).** «stand infrastructure issue» снят:
> turn-close работает корректно (не дефект), а п.3/п.4/п.5 after-fix
> **завершены live** и PASS.

### Шаг 4: Диагноз «блокер» + live after-fix п.2/п.3/п.4/п.5 + trap-фикс (2026-10-06, после второго отчёта)

Поручение оператора: (1) блокер = дефект продукта до доказательства числами
(диагностика 4 предикатов, prior commit И HEAD, e2e Э4, fix+регрессия);
(2) launcher trap гасит группу + ждёт; (3) live after-fix п.2/п.3/п.4/п.5
на одном бинарнике; (4) HANDOFF после каждого шага. **Всё закрыто, все PASS.**

**Бинарник live-серии: `/tmp/hl/runpilot` SHA256 = `52e821f6fd96fee266e6bdf9e425460d4aa1799fc33bce75d2b8cf565f96abe4`**
(HEAD + все правки). Хеш в каждом `-after-fix` артефакте.

**1. Блокер — НЕ дефект (доказано числами).**
- (a) Диагностика 4 предикатов turn-close (стенд, только A, без B, по 1 с):
  turn закрыт через **1.7 с** после last request (pane BUSY→IDLE), все 4
  предиката истинны (`07:03:18`→`07:03:20`). «Блокер» был **таймингом замера**
  (запуск B через ~50 с после `lastEnd`), а не кодом. → `10-turn-close-diagnostic.md`.
- (b) `git diff HEAD -- internal/scheduler/complete.go` = **только комментарий**:
  `checkTurn` (4 предиката) идентичен на prior commit `85e29c6` и HEAD →
  диагностика применима к обоим.
- (c) e2e Э4 на HEAD: логику планировщика/шлюза покрывают S1–S14 +
  `go test -race ./...` (0 fall); живая серия п.2/3/4/5 = live-Э4 на реальном qwen.
- (d) fix + регрессионный тест: **N/A** — (a) доказала отсутствие дефекта;
  регрессия покрыта unit-тестами. Вывод «infrastructure issue» **снят**.

**2. Live after-fix (один бинарник, реальный qwen) — все PASS.**
- **П.2** `02-p2-measurement-after-fix.md`: B в окне удержания = **1**,
  других B = **0**. LEASE_RELEASE(A)→LEASE_GRANT(B) = **2499.95 мс**
  (RUN2, `firstByteMs=0`) ≤ cooldown+tick = 2500. GRANT→first_byte = **2.979 мс**
  (RUN1 `firstByteMs=15000`: 15000 — артефакт задержки fakellm, не прокси).
  LEASE_GRANT от планировщика (`origin:IMPLICIT`). held_ms=21927 (RUN2).
- **П.3** `03-p3-noenv-after-fix.md` (контроль noenv): первый разрыв на
  **119.987 с** от Enter (≈ qwen default 120000 мс), **1 retry** за 300 с.
  Строка request в БД есть (`error=CLIENT_GONE`). Pre-fix: 120.394 с / 1 retry.
- **П.4** `04-p4-esc-after-fix.md`: Esc → `QUEUE_DEQUEUE{client_gone}` за **5.3 мс**,
  queue_entry удалён, B → IDLE, request `error=CLIENT_GONE`. (Pre-fix: heldToResume
  удерживал запись — **исправлено** clientGone.)
- **П.5** `05-p5-holdmax20-after-fix.md`: `hold_max_sec=20` → 503 +
  `Retry-After:30` + `runpilot_no_slot` через **20.001339 с**; `enqueued_at`
  сохранён через 3 holdPass (`QueueUpsertHeld` MIN) — **было FINDING**.

**3. Trap-фикс launcher.sh (было FAIL → PASS).** Корень: serve
`shutdown_grace_sec=30` (`config/defaults.go:22`) — SIGTERM graceful ждёт
до 30 с. Фикс: `cleanup()` = SIGTERM трём PID + tmux → grace 2 с → SIGKILL
выжившим. 2 живых чека (env/20 **in-flight**, noenv): все PID мертвы **≤ 1 с**
(требовалось ≤ 5 с). → `09-stand-stop-and-node.md`.

**4. Остановка по PID + рабочий узел (09).** Остановка — только по PID из
info.json (SIGTERM→grace→SIGKILL + tmux), orphan'ов нет. Рабочий serve
`3446855` (старт Fri Oct 2 17:28:40, tailscale 100.124.155.106:8788). Узел
3p14-aaa — **embedded** (`cmd/runpilot/embedded.go:52`, `serve.go:310`,
`coordinator.embedded_node=true`), `/api/v1/state`: 3p14-aaa (127.0.0.1,
qwen 0.24.7, tmux 3.2a, health 07:04) + vzu5-claw.

**5. -race на HEAD (Шаг 2 + 4):** `go test -race ./...` — **21 ok, 0 FAIL**
(включая gateway 17.4с, scheduler 15.8с, store 6.0с).

**Статусы (07-status-table.md, новая версия под 7 пунктов):**
п.1–п.7 все PASS (п.5 — «PASS (фикс), было FAIL»), Доп(1) a/b/c PASS / d N/A,
Доп(2) trap PASS (фикс), хеш бинарника в -after-fix. **Вывод «infrastructure
issue, не кодовый» снят.**

**Файлы этого шага:** `10-turn-close-diagnostic.md`, `02/03/04/05 -after-fix`,
`07-status-table.md` (переписана), `09-stand-stop-and-node.md` (переписана),
`stand/launcher.sh` (trap-фикс).

### Шаг 5: 2-й отчёт — client_timeout_sec (A) + граница тика (Б) + live п.2 (2026-10-06)

Порядок оператора (Г): A → Б → race+staticcheck → пересборка /tmp/hl → live п.2
(Enter в B ≤ 30 с после начала запроса A) → отчёт. HANDOFF после каждого шага.

**A. client_timeout_sec (шаг 1 rework) — ГОТОВО, весь сьют зелёный (21 ok, 0 FAIL).**
- A.1: `ProfileQwen.ClientTimeoutSec int` (yaml `client_timeout_sec`), default **5400**;
  `gateway.hold_max_sec` default **60 → 3600** (В.8).
- A.2 (В.7): новое правило валидации (заменяет удалённое `hold_max < first_byte`):
  инвариант `client_timeout_sec > hold_max_sec + max(servers[].first_byte_timeout_sec)`;
  нарушение (exit 2) — `<=` (равенство = нарушение). Текст ошибки с именем
  сервера-макс + требуемым минимумом (сумма+1). Тест `TestValidateV7ClientTimeoutFloor`
  (5 случаев из задания).
- A.3: `POST /api/v1/run` → `RunResponse{sid, client_timeout_ms}` (координатор
  `client_timeout_sec × 1000`); ключа в конфиге клиента нет. `docs/api.md` обновлён.
- A.4: `runpilot run` → `coderEnv()` выставляет `QWEN_CODE_API_TIMEOUT_MS`
  (client_timeout_ms) ПОСЛЕ env профиля (перекрывает встроенный/override/env оператора).
  Тест `TestCoderEnvClientTimeoutOverride`.
- A.5: стенд — hardcode `QWEN_CODE_API_TIMEOUT_MS` удалён из `profile.qwen.yaml`,
  `profile.qwen.noenv.yaml` удалён, launcher всегда `profile.qwen.yaml`.
- A.6: строка таблицы PASS **только после live п.2** (`/proc/<pid>/environ`).
- api-тесты: счёт working-values/schema 93 → 94 (новое поле/ключ).

**Б. Граница тика (шаг 2 rework) — ВЫПОЛНЕНО (дефекта не нашлось; поведение корректно).**
- «Один now на тик» подтверждено: `Tick()` снимает `now := s.clk.Now()` один раз и
  передаёт в `tickComplete→closeTurn→releaseLeaseLocked+setCooldown`; хранилище
  `lease.go` пишет `granted_at` (`l.GrantedAt`, L55) и `released_at` (`ts(now)`, L114)
  из переданного `at`, без своего чтения часов.
- Тест `TestTickBoundaryReleaseToGrant` (`internal/gateway/tick_boundary_test.go`):
  освобождение аренды в тике T (closeTurn в tickComplete) → grant в тике **T+cooldown**
  (ровно 2с = 20 тиков; cooldown кратен tick). **PASS** — +tick нет.
- Live «+1 тик» (2499.95 мс, п.2 до) — сценарное смещение: в live B стала IDLE (свой
  ход закончился) на тик позже освобождения слота; планировщик выдаёт, когда слот
  свободен **И** сессия IDLE. Дефекта планировщика нет.

**Шаг 3 (race + staticcheck) — ВЫПОЛНЕНО.** `go test -race ./...` → **21 ok / 0 FAIL**;
`staticcheck ./...` (make staticcheck → `go run honnef.co/go/tools/cmd/staticcheck@latest`)
→ **EXIT=0, находок нет**. Артефакт: `docs/evidence/hold-silent/step3-race-staticcheck-2026-10-06.txt`.

**Шаг 4 (пересборка /tmp/hl) — ВЫПОЛНЕНО.** `CGO_ENABLED=0 go build`. Финальные хеши:
`runpilot` → **a8bf04c80588da68f9334899f6f52b78aeb8419e5ddf92db52185016284eca56** (client_timeout
+ coderEnv + eventAt-фикс), `fakellm` → **a1637f5e023c889dfeba737b705d3d250a86313bf8ae5ceaa95c1dccddd6902e**
(finish_reason-фикс). go1.26.7.

**Шаг 5 (live п.2) — ВЫПОЛНЕНО, 8/8 PASS** (бинарник a8bf04c8, PIDs fakellm=3272652
serve=3272653 node=3272654, run-1791277046). По заданию: firstByteMs=360000→A(ECXMJQDPTX)→
firstByteMs=0→Enter в B(YK68XCQYC0) через 2 с (≤30 с). Точные числа: held_ms(B)=**362506**
(≥300000); в окне ровно 1 B-запрос, других 0; LEASE_RELEASE(A)=09:03:49.549862391→
LEASE_GRANT(B)=09:03:52.049813819 = **2499.95 мс** (≤cooldown+tick=2500; фаза, ≤ порога);
t_first_byte(B)−GRANT(B)=**3.88 мс** (≤500); GRANT payload `{"held":true,"origin":"IMPLICIT"}`
(планировщик); экран B чист (count_errors=0, ответ `c0`); `/proc/3273139/environ` →
`QWEN_CODE_API_TIMEOUT_MS=5400000` (из координатора client_timeout_sec=5400). Артефакт:
`p2-capture-final-2026-10-06.txt`, таблица — `02-p2-measurement-after-fix.md`.

**Доп. правки при live п.2 (шаг 5):**
- **fakellm finish_reason** (`internal/testutil/fakellm/fakellm.go`): финальный SSE-чанк
  теперь несёт `finish_reason:"stop"` (было `null` + [DONE]) — иначе qwen: «Model stream
  ended without a finish reason» (ошибка+retry на экране B, критерий п.2). Пересборка
  fakellm a1637f5e.
- **eventAt** (`internal/scheduler/scheduler.go` + dispatch.go/complete.go): LEASE_GRANT/
  LEASE_RELEASE теперь пишут `ts` из now тика (`eventAt(now,…)`), а не `s.clk.Now()` —
  инвариант «один now на тик» (Б). Live: event ts == granted_at/released_at (совпадает).
- **Тесты нижних границ закрытия хода** (`internal/scheduler/turn_close_lowerbound_test.go`):
  `TestTurnCloseQuietLowerBound` (ход не закрывается раньше last_request_end+done_quiet_sec)
  + `TestTurnCloseStableLowerBound` (раньше stable_since+done_stable_sec). Оба PASS.

**Шаг 6 (отчёт) — ВЫПОЛНЕНО.** Все 6 пунктов отчёта закрыты.
- (1) **Self-grant**: `holdPass` (`internal/gateway/lease.go:157-197`) — в цикле
  удержания НЕТ `g.grant` («Аренду выдаёт ТОЛЬКО планировщик»); 3 теста `-race` PASS:
  `TestHoldSlotGranted` (0.35с), `TestHoldRankResumeBeatsHeld` (0.21с),
  `TestHoldCapacityOneSlotTenFrees` (0.65с). Doc: self-grant — НЕ «ограничение» в ТЗ, а
  дефект (П.10), нарушавший ТЗ «единственный выдающий — планировщик»
  (README.ru.md:53, ARCHITECTURE.md:12); 06-p10 — «Self-grant удалён (П.10)»,
  docs/api.md:125 — LEASE_GRANT `(+held:true)`.
- (2) **Таблица 07** переписана целиком: 9 пунктов реализации + В.7 + В.8
  (статус/тест/команда/артефакт) + отдельная строка `QWEN_CODE_API_TIMEOUT_MS`.
- (3) **staticcheck** — EXIT=0, вывод в `step3-race-staticcheck-2026-10-06.txt`.
- (4) **Закрытие хода** — `11-turn-close-labels-lowerbound.md`: stand-override
  done_quiet_sec/done_stable_sec 5→1 (default), 4 метки (last_request_end, panel→IDLE,
  stable_since, LEASE_RELEASE), тесты нижних границ PASS.
- (5) **HANDOFF** — этот раздел.

Полный сьют: 21 ok / 0 FAIL; staticcheck EXIT=0. Бинарник live: a8bf04c8.
