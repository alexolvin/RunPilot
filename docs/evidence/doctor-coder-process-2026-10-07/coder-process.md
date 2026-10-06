# doctor `coder_timeout_env` — читаем env процесса КОДЕРА, не pane_pid (п.2, 2026-10-07)

## Проблема
Проверка читала `/proc/<pane_pid>/environ`. Но кодер — **потомок** `pane_pid`,
совпадающий с `cmdline_regex` профиля: bash-обёртка панели может иметь
`QWEN_CODE_API_TIMEOUT_MS`, а реальный процесс qwen (потомок) — нет. Тогда
проверка по pane_pid давала ложный PASS.

## Фикс
- `doctor.Context` получил `Cmdline *regexp.Regexp` (cmdline_regex профиля).
  `cmd/runpilot/doctor.go` загружает `profiles.LoadQwen()` и передаёт `prof.Cmdline()`
  (best-effort: не загрузился → nil, проверка деградирует до pane_pid).
- В doctor добавлен standalone-обход дерева процессов (doctor standalone — node
  `procTable` unexported, переиспользовать нельзя):
  - `psOutput()` = `ps -A -o pid=,ppid=,args=` (одним вызовом, точка инъекции);
  - `buildProcTree` парсит в `{args[pid], children[ppid]}`;
  - `coderPID(root, re)` — BFS от `root` (включая `root`) к первому совпавшему с
    `re` процессу; `re==nil` → `root`, нет совпадения → 0.
- `checkCoderTimeoutEnv`: для каждой живой управляемой панели находит кодера
  (`coderPID`), читает `readProcEnviron(кодер)`. Нет `QWEN_CODE_API_TIMEOUT_MS` →
  WARN (не FAIL) с sid + именем. `coderPID==0` → деградация до pane_pid.

После `bash→qwen` exec кодером часто бывает сам `pane_pid` (совпадение на корне) —
покрывается тем же `coderPID` (root проверяется первым).

## Тесты (internal/doctor/coder_timeout_env_test.go)
`installFakePanes` теперь фальсифицирует и `psOutput`; контекст несёт `Cmdline`.
- `TestCoderTimeoutEnvCoderChildLacks` — переменная есть у обёртки (pane_pid=bash),
  нет у кодера (потомок qwen) → **WARN** (читаем кодера, не обёртку).
- `TestCoderTimeoutEnvCoderChildHas` — нет у обёртки, есть у кодера → **PASS**
  (доказательство, что читается кодер).
- Существующие (AllPresent / WarnMissing / NoServer / EnvironUnreadable) сохранены
  и переключены на 3-полевой ps-формат `pid ppid args`.

Проверено: `go build ./...` ok, `go test ./internal/doctor/...` ok,
`go run ./tools/lintlits` 0, `go vet` 0.
