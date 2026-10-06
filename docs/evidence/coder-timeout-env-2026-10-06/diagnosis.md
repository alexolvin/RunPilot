# QWEN_CODE_API_TIMEOUT_MS: диагностика «нет переменной у phonebroker» (2026-10-06)

Симптом: после перезапуска сессии `phonebroker` на vzu5-claw у процесса qwen
(`/proc/<pid>/environ`) **нет** `QWEN_CODE_API_TIMEOUT_MS`. Без неё client-side
таймаут qwen = 120 s (дефолт SDK) → сессия не переживает silent-hold
(>120 s без первого байта) и обрывается.

## Причина (файл:строка)

**`internal/api/spawn.go` — `spawnEnv()`** (координатор, сборка env для
управляемой сессии узла). До фикса функция заполняла `out` **только** из шаблона
профиля `s.prof.Env` и возвращала, никогда не ставя `QWEN_CODE_API_TIMEOUT_MS`.

Контраст: путь `runpilot run` использует **`coderEnv()`**
(`cmd/runpilot/run.go`), который ПОСЛЕ env профиля ставит
`QWEN_CODE_API_TIMEOUT_MS = client_timeout_ms` (`run.go:236`). Поэтому
`runpilot run` (и `--here`) переменную имели, а **управляемые сессии узла — нет**.

`phonebroker` — управляемая сессия (панель с `@runpilot_sid`) на узле vzu5-claw:
она создаётся координатором через **spawn** (`POST /api/v1/sessions/spawn` →
`handleSpawn` → `spawnEnv`) или **adopt** (`POST /api/v1/unmanaged/…/adopt` →
`handleAdopt` → `spawnEnv`). Оба пути шли через `spawnEnv` без переменной →
перезапуск (re-spawn/re-adopt) снова давал процесс без `QWEN_CODE_API_TIMEOUT_MS`.
(Точный подпуть — spawn vs adopt — подтверждается выводом оператора:
`OPENAI_BASE_URL` содержит `/s/<SID>/v1`, SID совпадает с phonebroker.)

## Карта всех путей запуска кодера

| № | Путь | Код | Ставил до фикса | После фикса |
|---|------|-----|-----------------|-------------|
| 1 | `runpilot run` (tmux new-session) | `cmd/runpilot/run.go` → `coderEnv` | ✅ (client_timeout_ms из `/run`) | ✅ |
| 2 | `runpilot run --here` | `cmd/runpilot/run.go` → `coderEnv` → `runHere` | ✅ | ✅ |
| 3 | Веб «Новая сессия» (spawn) | `internal/api/spawn.go` `handleSpawn`→`spawnEnv` | ❌ | ✅ |
| 4 | Веб «Перезапустить под runpilot» (adopt) | `internal/api/unmanaged.go` `handleAdopt`→`spawnEnv` | ❌ | ✅ |
| 5 | agent-run на узле (spawn/adopt/respawn) | узел: `new-session`/`respawn-pane -e` из `m.Env` (заполняет `spawnEnv`) | ❌ | ✅ |
| 6 | Встроенный узел координатора | тот же `handleSpawn`→`spawnEnv` | ❌ | ✅ |

Значение везде — `client_timeout_ms` координатора =
`profiles.qwen.client_timeout_sec × 1000` (метод `ProfileQwen.ClientTimeoutMS()`).

## Фикс

1. `internal/api/spawn.go` — `spawnEnv()` ставит `QWEN_CODE_API_TIMEOUT_MS =
   ClientTimeoutMS()` ПОСЛЕ env профиля (перекрывает операторский override), как
   `coderEnv`. Теперь все 6 путей ставят переменную.
2. `internal/config` — `msPerSec` + `ProfileQwen.ClientTimeoutMS()`: единая точка
   сек→мс (и устранён магический `1000` в `internal/api/server.go`).
3. `internal/doctor/doctor.go` — новый чек **`coder_timeout_env`**: для каждой
   живой управляемой сессии узла (`@runpilot_sid`) читает
   `/proc/<pane_pid>/environ`; нет переменной → **WARN с sid и именем** (НЕ FAIL —
   чтобы предпроверка `upgrade-node.sh` не блокировала обновление узла со старыми
   сессиями).

## Тесты

- `TestSpawnEnvClientTimeout` (api): spawnEnv ставит `5400000` (5400×1000),
  перекрывает операторский override, подставляет шаблон `{{.SID}}`.
- `TestCoderTimeoutEnv*` + `TestProcEnvHas` (doctor): все на месте → PASS;
  одна без переменной → WARN (sid+имя, с переменной не в WARN); нет tmux-сервера
  → PASS; /proc не читается → WARN; NUL-разделение environ.
