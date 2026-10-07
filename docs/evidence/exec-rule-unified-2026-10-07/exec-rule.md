# p.1 — Единое правило «кодер вне очереди» (расширено на EXEC) + ExecPID в записи

**Дата:** 2026-10-07

## Правило

Процесс кодера **управляемый** тогда и только тогда, когда выполнены **оба** условия:
- **(а)** он потомок **корневого процесса живой сессии**:
  - для **PANE** — потомок `pane_pid` панели с `sid` сессии;
  - для **EXEC** — потомок PID процесса `runpilot exec` этой сессии;
- **(б)** его `OPENAI_BASE_URL = gateway_url/s/<sid этой сессии>/v1`.

Иначе кодер — **вне очереди** (показан в веб, doctor WARN, узел шлёт координатору).

Правило **расширено** (не «исключение для EXEC», а общий механизм): PANE и EXEC —
два вида корневого процесса живой сессии, единый вывод sid + единое условие (б).

## Как выводятся sid и управляемость (ОДИН механизм — node и doctor)

`internal/node/external.go`:

- **`GatewayCoderURL(gatewayURL, sid)`** — canonical-URL кодера сессии:
  `TrimRight(gatewayURL,"/") + "/s/" + sid + "/v1"` (то же, что gateway `writeJobEnv`
  и `buildQwenCmd` для PANE — условие (б) строится из одних токенов).
- **`SessionSID(pid, paneSID, coderRunpilotSID, parentOf, argsOf)`** — обходит дерево
  процессов **ВВЕРХ** от кодера до init:
  - встретил PID из `paneSID` (панели живых сессий) → вернул sid панели (PANE);
  - встретил процесс, совпадающий с `execCmdRe` (`(^|/)runpilot exec( |$)`) → вернул
    `coderRunpilotSID` (RUNPILOT_SID из env кодера) — сессия этого `runpilot exec`;
  - дошёл до init без совпадения → `""` (ручной запуск / завершённая сессия).
- **`CoderOutOfQueue(sessionSID, openAIBaseURL, gatewayURL)`** — `true` (вне очереди),
  если `sessionSID == ""` ИЛИ `openAIBaseURL != GatewayCoderURL(gatewayURL, sessionSID)`.

**Локальный вывод EXEC-корней** (а не координатор → узел): `runpilot exec` и qwen живут
на одной машине с узлом и doctor. Узел (ScanExternal) и doctor (checkCoderOutOfQueue)
сканируют `ps` локально: находят процесс `runpilot exec` (execCmdRe) и берут `RUNPILOT_SID`
из env кодера как sid этой сессии. Когда `runpilot exec` умирает, qwen репарентирован в
init (parent=1) → обход вверх живого `runpilot exec` не находит → `sessionSID=""` → вне
очереди (совпадает с тестом «EXEC завершена, кодер жив»). Это один механизм для node
и doctor (doctor — отдельный процесс без доступа к БД координатора на удалённом узле).

## PID процесса runpilot exec в записи EXEC-сессии

- **`store.SessionRecord.ExecPID int`** + миграция **016**
  (`ALTER TABLE session ADD COLUMN exec_pid INTEGER NOT NULL DEFAULT 0;`), версия схемы
  15→16 (store_test.go).
- Клиент `runpilot exec` шлёт `os.Getpid()` при `POST /api/v1/jobs`
  (`jobclient.create` → поле `pid`); координатор (`api/jobs.go handleJobCreate`)
  сохраняет `rec.ExecPID = req.PID`. Это **авторитетная копия** для координатора
  (отчёт/управление), «доступна узлу». Правило не читает это поле (вывод EXEC-корней
  локальный, см. выше) — поле не дублирует вывод, а фиксирует PID в записи.

## Тесты (проходят)

- **node:**
  - `TestScanExternalExecLive` — qwen под живым `runpilot exec` (RUNPILOT_SID + верный
    URL) → управляемый, внешних 0.
  - `TestScanExternalExecFinished` — exec умер, qwen под init (верный URL) → вне очереди
    (1 внешний).
  - `TestScanExternalCron` / `TestScanExternalSystemd` — cron/systemd qwen вне, панель
    с верным URL управляема.
  - `TestCoderOutOfQueue` — 5 случаев единого правила (нет URL / vLLM / чужой sid → вне;
    своя сессия → управляемый).
- **doctor:**
  - `TestCoderOutOfQueueExecManaged` — кодер живой EXEC-сессии → PASS.
  - `TestCoderOutOfQueueExecFinished` — EXEC завершена, qwen жив → WARN.
  - Существующие (Manual/ManagedPass/ForeignSID/VLLMDirect/None) — без изменений, проходят.

## Затронутые файлы

- `internal/node/external.go` — `SessionSID`, `execCmdRe`, `readProcEnvValue`, новая
  сигнатура `ScanExternal` (+ `runpilotSID`), правило по единому `sessionSID`.
- `internal/node/consts.go` — `runpilotSIDKey`.
- `internal/node/node.go` — цикл передаёт `readRunpilotSID` + `gatewayURLValue()`.
- `internal/doctor/doctor.go` — `procTree.ppid` (обход вверх), `checkCoderOutOfQueue`
  через `node.SessionSID` + `node.CoderOutOfQueue`, `procEnvValue`.
- `internal/store/session.go` + `migrations/016_session_exec_pid.sql` +
  `store_test.go` (15→16) — `ExecPID`.
- `cmd/runpilot/exec.go` + `jobclient.go` — `os.Getpid()` в create.
- `internal/api/jobs.go` — `jobCreateRequest.PID` + `rec.ExecPID`.

## Риск/примечание

Микро-окно (<1 с): `runpilot exec` жив, но запись уже GONE (конец finish) — правило
(локальный вывод) назовёт кодера управляемым до репарентирования в init. Незначимо
(сессия завершается).
