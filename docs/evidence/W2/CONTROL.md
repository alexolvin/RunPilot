# W2 — Настройки: CONTROL

Дата: 2026-09-26. Все 6 строк CONTROL выполнены. `make check` — 0; `go test -race ./...` — 0
падений; `make lint` — 0. S1–S14 (базовое ТЗ) — PASS на настройках из БД (R9).

Содержание (раздел 20, W2): bootstrap/рабочие настройки, схема (4.3), миграция с YAML
(4.8), ревизии и откат (4.6), экспорт/импорт (4.7), серверы в БД, проверки (4.5),
момент применения (4.4). Фронтенд форм — W3; динамический реестр серверов шлюза — W6
(см. «Отклонения и решения»).

## Строки CONTROL (раздел 20, W2)

| # | Строка CONTROL (ТЗ) | Команда проверки | Артефакт | Результат |
|---|---|---|---|---|
| 1 | Миграция на копии реальных БД и конфига 3p14-aaa: перенесено ключей = ключей в исходном YAML, копии созданы, конфиг переписан | `go test ./internal/store/ -run TestMigrate -v` | `control-tests.log`, `internal/store/migrate_yaml.go:36` | PASS (с оговоркой, см. откл. 1): `MigrateFromYAML` (migrate_yaml.go:36) — копия БД `aqm.db.bak-<версия>-<время>` + копия `config.yaml.bak-<время>`, ревизия №1 `source=migration` (diff = только YAML-ключи, снимок полный), серверы → таблица `server`, перепись конфига в bootstrap (рабочие секции удалены), откат при ошибке (исходные файлы на месте). `TestMigrateFromYAML` (13 рабочих ключей == 13 ключей YAML, копии созданы, переписан), `TestMigrateFromYAMLFreshDB`, `TestMigrateRollbackOnError`, `TestMigrateRollbackFreshDB` — PASS; второй запуск — no-op (таблица есть / секций нет) |
| 2 | S1–S14 базового ТЗ проходят на настройках из БД | `go test ./internal/scheduler/ -run 'TestS[0-9]' -v` | `control-tests.log` | PASS: `newHarn` (scenario_test.go) переписан на DB-driven — ситит временную БД рабочими настройками, `LoadWorkingInto`, планировщик с `Options.Reload` (re-read из БД в начале `Tick()`, scheduler.go:96-98,218). `TestS1..S14` (+`S4b`,`S8b`) — PASS; логика планировщика не менялась (только источник значений) |
| 3 | Тест на каждую строку 4.4 и каждое правило 4.5 | `go test ./internal/config/ ./internal/scheduler/ ./internal/api/ -run 'TestSchemaApplyWhen\|TestApplyWhen\|TestValidate45\|TestSettingsPatchValidation' -v` | `control-tests.log` | PASS: 4.5 — `TestValidate45{ServerNameTemplate,ServerNameUnique,URLAbsolute,Slots,AcceptNonEmpty,DoneLessThanStartConfirm,HoldMaxLessThanFirstByte}` + `TestSettingsPatchValidation` (400 `VALIDATION_ERROR`, `{path,code,message}`); 4.4 — поведенческие `TestApplyWhen{NextTickScheduler,NewTurn,ServerPriority,ServerSlots}` (строки 1,2,3,4) + `TestSchemaApplyWhen{,Groups}` (классификация `apply_when` всех строк, 1–7) |
| 4 | Откат ревизии даёт действующие настройки, равные исходным (пустой diff) | `go test ./internal/store/ ./internal/api/ -run 'TestRevertYieldsOriginal\|TestSettingsRevert' -v` | `control-tests.log`, `internal/store/setting.go:190` | PASS: `RevertToRevision` (setting.go:190) применяет снимок ревизии как НОВУЮ ревизию `source=revert`; `TestRevertYieldsOriginal` — действующие после отката == исходные (пустой diff); `TestSettingsRevert` (API `POST /settings/revert`) |
| 5 | Экспорт → импорт → пустой diff | `go test ./internal/store/ ./internal/api/ -run 'TestExportImportEmptyDiff\|TestSettingsImport' -v` | `control-tests.log`, `internal/store/settings_export.go:27,62` | PASS: `ExportSettings` (settings_export.go:27) — YAML рабочих настроек + серверы БЕЗ секретов (только имена `key_env`); `ImportSettings` (:62) — YAML, проверка 4.5, diff, применение одной ревизией `source=import`, `?dry_run=`; `TestExportImportEmptyDiff` — экспорт→импорт = пустой diff; `TestSettingsImport{,Invalid}` |
| 6 | Схема покрывает 100% ключей defaults.go (тест сверяет списки) | `go test ./internal/config/ -run TestSchemaCoversAllWorkingKeys -v` | `control-tests.log`, `internal/config/schema.go:76` | PASS: `Schema()` (schema.go:76) — 92 `SettingField` (path/type/unit/default/min/max/enum/group/label/help/apply_when/dangerous), default из `Defaults()` (R2 — литералы не дублируются); `WorkingLeafPaths()` (work.go:63) = 92 рабочих ключа (рефлексия + bootstrap-исключения); `TestSchemaCoversAllWorkingKeys` — 92 == 92 (схемы нет/лишний ключ → FAIL) |

## Дополнительно (строгие правила, проверки)

| Проверка | Команда | Артефакт | Результат |
|---|---|---|---|
| `make check` (vet, staticcheck, test -race, lint) | `make check` | `make-check.log` | PASS: rc=0 |
| `go test -race ./...` | `go test -race ./...` | `test-race.log` | PASS: все пакеты, race чист |
| R2 (нет литералов) | `make lint` | `make-lint.log` | PASS: `lintlits`/`lintweb`/`linthosts` 0 |
| API настроек (7 маршрутов) | `go test ./internal/api/ -run TestSettings -v` | `internal/api/settings.go` | PASS: `GET/PATCH /settings`, `/schema`, `/revisions`, `POST /revert`, `GET /export`, `POST /import?dry_run=` (server.go:110-116) |
| Прод-вайринг (serve) | live-смок (см. откл.) | `cmd/aqm/serve.go` | первый старт: «настройки перенесены из YAML в БД», `GET /settings` = 92 ключа с migrated-значениями, `GET /servers` из БД, PATCH → 200/400, revert, export YAML — работает |

## Отклонения и решения (зафиксированы)

### 1. CONTROL 1 — реальные файлы 3p14-aaa отсутствуют на машине
На `3p14-aaa` (эта машина) деплой aqm НЕ развёрнут: `~/.config/aqm/` и
`~/.local/share/aqm/` отсутствуют, `aqm.db` не найден (проверено `ls`). Поэтому
буквально «копии реальных БД и конфига 3p14-aaa» невозможны. Миграция проверена на
**представительной v1-копии** (13 рабочих ключей по всем секциям + сервер; БД v1 =
миграции 001+002 + живая сессия — проверено, что не теряется). Если оператор даст
реальные файлы (`~/.config/aqm/config.yaml` + `~/.local/share/aqm/aqm.db`), тест
`TestMigrateFromYAML` подставит их тривиально (путь параметризован). Логика миграции
(копии/перепись/откат/no-op) покрыта 4 тестами — PASS.

### 2. 4.4 строки 5–7 — схема-метаданные, не поведенческие тесты
Строки 5 (`upstream_url/model/key_env` сервера → новые запросы), 6
(`profiles.qwen.model_alias` → новые сессии/рестарт кодера), 7 (`web.*` → новые
соединения/сессии браузера) требуют прогона **шлюза/веба/spawn**, а не планировщика.
Они покрыты классификацией `apply_when` в схеме (`TestSchemaApplyWhen{,Groups}`
проверяет `apply_when` всех 7 строк); поведенческие тесты — для строк 1–4
(планировщик: next_tick / new_turn / priority / slots). Поведенческая проверка
строк 5–7 — вместе с W6 (реестр/шлюз) и W3 (веб).

### 3. Gateway/monitor в проде читают конфиг статично (динамический реестр — W6)
Планировщик пере-read'ит рабочие настройки из БД на каждом тике (`Options.Reload`,
4.4 строки 1–2 — поведенчески проверено). Шлюз и монитор в `serve.go` получают
`cfg` (рабочие настройки из БД на старте), но **без** Reload-хука → смена
`gateway.*`/`monitor.*` и `priority/slots` серверов через импорт действует после
рестарта. Динамический реестр серверов шлюза + Reload у gateway/monitor — задача W6
(управление серверами). Не влияет на строки CONTROL W2 (4.4 строки 5–7 — см. откл. 2;
S1–S14 не тестируют динамические настройки шлюза).

### 4. Встроенный узел при старте смока
При live-смоке (`embedded_node: true` по умолчанию) бинарь сделал read-only запросы
к tmux-сокету `default` хоста — это поведение бинаря по умолчанию, не tmux-команды
агента (агент `-L aqmtest` только). Смок зачистлён.
