# W13 — этапы 7–9: отчёт

## Этап 7 — маршрут кодера только через аргументы

### Удалённые записи settings.json

Все записи settings.json удалены. Ранее писали:

| Файл:строка | Что писал | Статус |
|---|---|---|
| `internal/node/qwen_settings.go` (EnsureQwenSettings) | `security.auth.selectedType=openai`, `model.generationConfig.contextWindowSize`, `modelProviders` | **Удалено** |
| `internal/node/qwen_settings.go` (safeModelAlias) | Подмена `OPENAI_MODEL` при коллизии | **Удалено** |
| `internal/node/qwen_settings.go` (patchQwenSettings) | Патч `selectedType`, `contextWindowSize` | **Удалено** |
| `internal/node/qwen_settings.go` (createQwenSettings) | Создание settings.json | **Удалено** |

### История правок ~/.qwen/settings.json оператора

| Файл | model.name | provider ids | Кто написал |
|---|---|---|---|
| `settings.json.pre-restore-20261002` | `qwen3.8-27b-fp8` | — | Оператор (до RunPilot) |
| `settings.json.runpilot.bak` | `qwen3.8-27b` | `qwen3.8-27b` (×2) | **RunPilot** (EnsureQwenSettings) |
| `settings.json` (текущий) | `qwen3.8-27b` | `qwen3.8-27b` (×2) | RunPilot (не откатывать) |

Запись modelProviders с id `qwen3.8-27b` (не `qwen3.8-27b-fp8`) — от RunPilot
(EnsureQwenSettings, W9 доп-3c). `model.name` был `qwen3.8-27b-fp8` до RunPilot,
RunPilot изменил на `qwen3.8-27b`.

### Почему doctor W11 не остановил коллизию

Doctor `checkQwenSettings` проверяет коллизию `model_alias` с id modelProviders.
На момент W11 `model_alias` = `qwen3.8-27b-fp8` (из server config), а provider
id = `qwen3.8-27b` (записан RunPilot). Разные строки → коллизия не найдена →
doctor PASS. Реальная коллизия — между `model.name` (`qwen3.8-27b`) и provider
id (`qwen3.8-27b`), но doctor не проверяет `model.name`.

### /proc/<pid>/cmdline

На момент отчёта активных qwen-процессов нет. Все пути запуска
(spawn, respawn, adopt, runpilot run, exec) используют
`CoderArgsFromEnv`: `--auth-type openai --model <alias> --openai-base-url <шлюз>`.

## Этап 8 — диалог «Новая сессия»

### PhoneBroker pin:vzu5

**Выбор оператора** (не дефолт диалога). Диалог не имеет полей pin/prefer —
создаёт сессию без ограничения. Pin установлен через API
(`POST /api/v1/sessions/{sid}/pin`).

### Реализовано

- `project_dir_match` (defaults.go: пустая строка, schema, config_test).
- `handleNodeDirs`: фильтр каталогов по `project_dir_match`.
- Диалог: имя = `baseName(dir)`, занято → `-2`, `-3`…
- Карточка сессии: pin/prefer + кнопка «Снять привязку».
- `project_dir_match = "myhub"` установлено через API.

## Этап 9 — имена моделей

### /v1/models (read-only)

| Сервер | Модели |
|---|---|
| 3p14 (127.0.0.1:8004) | `qwen3.8-27b`, `qwen3.8-27b-fp8` |
| vzu5 (100.126.222.23:8004) | `qwen3.8-27b`, `qwen3.8-27b-fp8` |

### Обслуживаемые имена

- `servers[].upstreams.openai.model = "qwen3.8-27b"` (3p14, vzu5).
- `profiles.qwen.model_alias = "qwen3.8-27b (RunPilot)"` (через API).

### Ворота → выкат → узлы

- `go test -race ./...` + `make staticcheck` + `make lint` — 0 нарушений.
- Deploy `1fbe60a` (vcs.modified=false), бэкап `runpilot.bak-20261009T233200Z`.
- Узлы: 3p14-aaa, vzu5-claw, vzu5-big — все на `1fbe60a`.
- Сессии на перезапуск (без нового алиаса в cmdline): **PhoneBroker**, **NextLife**.
