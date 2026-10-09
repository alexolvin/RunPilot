# W12 Git Proof

Команда: `git log --since=2026-10-09T06:26:02Z --name-status --format="%H %s"`

## Коммиты

### c744654 — W12 (1): test=true в exec + DIR_FORBIDDEN для всех 3 путей

| Файл | Задача |
|------|--------|
| `cmd/runpilot/exec.go` | test=true в CLI exec |
| `cmd/runpilot/jobclient.go` | test=true в HTTP-клиенте exec |
| `internal/api/jobs.go` | DIR_FORBIDDEN в API exec |
| `internal/api/jobs_test.go` | Тесты DIR_FORBIDDEN (exec) |
| `internal/api/server_test.go` | Тесты DIR_FORBIDDEN (run) |

### 019386d — W12 (2-4): HELD fix (QueueUpsertHeld held_since), flaps tool, regression green

| Файл | Задача |
|------|--------|
| `cmd/runpilot-fakellm/consts.go` | fakellm: константы (станд) |
| `cmd/runpilot-fakellm/main.go` | fakellm: упрощение (станд) |
| `cmd/runpilot/consts.go` | flaps: константа defFlapsSince |
| `cmd/runpilot/servers.go` | flaps: CLI-подкоманда |
| `internal/api/consts.go` | flaps: константа defFlapsSince |
| `internal/api/server.go` | flaps: маршрут GET /api/v1/servers_flaps |
| `internal/api/views.go` | flaps: handler handleServerFlaps |
| `internal/client/types.go` | flaps: тип ServerFlapsRow |
| `internal/monitor/monitor.go` | flaps: метод Flaps(since) |
| `internal/store/queue.go` | HELD: fix QueueUpsertHeld held_since |
| `internal/testutil/fakellm/fakellm.go` | fakellm: /_set-delays (станд) |

### 893dd04 — W12 (1.1): test=true в CLI run + API run (DIR_FORBIDDEN)

| Файл | Задача |
|------|--------|
| `cmd/runpilot/run.go` | test=true в CLI run |
| `internal/api/server.go` | DIR_FORBIDDEN в API run |

### 97a1866 — W12 (item 3): инварианты на эффективном конфиге, lease release, самовосстановление GONE, sid валидация

| Файл | Задача |
|------|--------|
| `cmd/runpilot/lease.go` | lease: CLI-подкоманда |
| `cmd/runpilot/root.go` | lease: регистрация подкоманды |
| `cmd/runpilot/serve.go` | serve: инициализация |
| `docs/evidence/W12-final/b-no-changes-proof.md` | evidence: B-доказательство |
| `docs/evidence/W12-final/sid-origin-report.md` | evidence: sid-отчёт |
| `internal/api/lease.go` | lease: API-эндпоинт |
| `internal/api/lease_test.go` | lease: тесты |
| `internal/api/server.go` | server: lease-маршрут |
| `internal/api/settings.go` | settings: effective config |
| `internal/api/settings_test.go` | settings: тесты |
| `internal/api/spawn.go` | spawn: инварианты |
| `internal/api/w6_test.go` | тесты |
| `internal/model/model.go` | model: типы |
| `internal/scheduler/gone_leases_test.go` | scheduler: GONE-тесты |
| `internal/scheduler/scheduler.go` | scheduler: lease release |
| `internal/sid/sid.go` | sid: валидация |
| `internal/sid/sid_test.go` | sid: тесты |

### 68b2e3c — W12 (доп): spawn — защита тестовых сессий от рабочего репозитория

| Файл | Задача |
|------|--------|
| `docs/evidence/W12-final/acceptance-scenario.md` | evidence: acceptance |
| `internal/api/spawn.go` | spawn: DIR_FORBIDDEN |
| `internal/api/w6_test.go` | spawn: тесты |

## Вывод

Все изменённые файлы относятся к задачам W12. Файлы, не относящиеся к задачам, отсутствуют — revert не требуется.
