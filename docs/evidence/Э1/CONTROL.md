# Э1 — CONTROL: PASS

Дата: 2026-09-25. Все строки CONTROL выполнены.

| # | Строка CONTROL | Команда проверки | Артефакт | Результат |
|---|---|---|---|---|
| 1 | Обязательный набор раздела 7 снят (13 состояний, ≥ 2 экрана на состояние) | `go test -run TestRequiredSetComplete ./internal/detect/`; список: `fixtures-list.txt` | `testdata/fixtures/qwen/0.24.4/` (28 экранов), `fixtures-list.txt` | PASS: 13 состояний × 2 + бонус `plan_mode` × 2 |
| 2 | Классификация 100% | `go test -run TestFixturesClassify100 ./internal/detect/` | `test-race.log` (весь набор) | PASS: 35/35 фикстур (28 qwen 0.24.4 + 7 mockcoder) дают ожидаемое состояние |
| 3 | Текст ввода совпадает побайто на всех `idle_input_*` | `go test -run TestFixtureInputByteExact ./internal/detect/` | `test-race.log` | PASS: 4 `idle_input_*` побайто + 2 `idle_empty` = пусто (плейсхолдер) |
| 4 | `busy_tool_looks_like_idle` и `approval_after_scroll` не классифицируются как IDLE | `go test -run TestNegativeFixturesNotIdle ./internal/detect/` | `test-race.log` | PASS: 4 отрицательных экрана → BUSY / PROMPT (явный assert ≠ IDLE) |
| 5 | Каталога `testdata/fixtures/claude` нет | `go test -run TestNoClaudeFixtures ./internal/detect/`; `ls testdata/fixtures/` | `test-race.log` | PASS: каталог отсутствует (тест падает при появлении) |

Дополнительно (разделы 7 и 15 ТЗ):

| Проверка | Команда | Артефакт | Результат |
|---|---|---|---|
| `make check` (vet, staticcheck, test -race, literals) | `make check` | `test-race.log`, `no-literals.log` | PASS: vet 0, staticcheck 0 замечаний, тесты rc=0, 0 числовых литералов в scheduler/gateway/detect |
| `aqm fixture capture <target> <state>` (NN-инкремент, путь `qwen/<version>/`) | `aqm fixture capture --socket aqmtest --version 0.24.4 mock1 smoke_test` | `cmd/aqm/fixture_test.go` + ручной прогон (smoke) | PASS: `smoke_test-01.txt`, `smoke_test-02.txt` по формату раздела 7 |
| mockcoder классифицируется детектором qwen на 100% своих экранов (раздел 15) | `go test -run TestFixturesClassify100 ./internal/detect/` (фикстуры `mockcoder_*`) | `testdata/fixtures/mockcoder/` (7 экранов: idle, busy, approval, wait_connecting, wait_compact, wait_rate_limit, after_error) | PASS: 7/7 |
| FNV-1a 64 (раздел 7) | `go test -run TestHashFNV1a ./internal/detect/` | `test-race.log` | PASS: `Hash("")` = offset basis `0xcbf29ce484222325`, детерминизм, различимость |

## Отклонения и решения (зафиксированы)

### wait_rate_limit классифицируется как BUSY (не WAIT_UI)

На реальных экранах Qwen Code 0.24.4 (buffer-режим, `ui.useTerminalBuffer: false`)
видимый экран во время ретрая 429 byte-в-byte идентичен ordinary busy:

```
────────…
>   Type your message or @path/tofile  …
────────…
  ➜ aqm-stub-proj · qwen3.8-27b
  Enter to steer · Ctrl+Q to queue · ⏸ Ask permissions (shift + tab to cycle)
```

Строки ретрая существуют, но только в истории терминала (документный поток):
`✕ [API Error: 429 Rate limit reached for requests.] (Retrying in 56s… (attempt 1/10))`
и `↻ Retrying in N seconds… (attempt X/10)` (доказательство: снимки истории
fxcap7 + req.log stub-сервера 85 запросов в ~4,5 мин; UI-ретраи
`RATE_LIMIT_RETRY_OPTIONS`: 10 попыток, 60 с → рост). ТЗ требует снимать
только видимый экран (`tmux capture-pane -p -J`), поэтому маркер WAIT_UI
на реальном экране rate limit в 0.24.4 не существует.

Решение:
- фикстуры `wait_rate_limit-01/02` классифицируются как **BUSY**
  (ожидаемое состояние зафиксировано в `fixture_test.go: expectedState`);
- маркер `Retrying in [0-9]+s` всё же включён в `wait_regex`: в
  **mockcoder** строка рисуется на видимом экране
  (`mockcoder_wait_rate_limit-01.txt` → WAIT_UI), и в будущих версиях Qwen
  Code, где строка станет видимой, классификация автоматически станет WAIT_UI
  — как требует таблица состояний раздела 7;
- для диспетчеризации отклонение безопасно: BUSY, как и WAIT_UI, не даёт
  dispatch submit (раздел 8 ТЗ), Enter не отправляется.

### wait_compact: маркер — высокая строка контекста

Команды `/compact` в 0.24.4 нет (auto-compaction только; порог
`autoCompactThreshold`, окно `generationConfig.contextWindowSize`). Во время
компрессии видимый экран = ordinary busy + строка статуса
`Context 100% used` (доказано экспериментально: окна 4096/16384,
реальные попытки компрессии, 23 с). Маркер: `Context (8[5-9]|9[0-9]|100)% used`.

### plan mode → WAIT_UI (бонус-состояние `plan_mode`)

Таблица состояний раздела 7 относит plan mode к WAIT_UI. Видимый маркер —
строка режима `plan mode (shift + tab to cycle)` (строчные буквы; проверено
на реальной сессии `--approval-mode plan`). Сняты 2 экрана (idle и busy в
plan-режиме) — оба → WAIT_UI; проверяется приоритет wait→busy.

### busy_regex включает `esc to cancel`

Наряду с реальным маркером 0.24.4 `Enter to steer`, `busy_regex` содержит
`esc to cancel` — строку, которую обязан показывать `aqm-mockcoder`
(раздел 15 ТЗ), чтобы детектор qwen классифицировал экраны двойника.
На реальных экранах 0.24.4 `esc to cancel` видима только в документном
потоке (история), поэтому ложных срабатываний на видимых экранах нет.

### volatile_regex пуст

В 0.24.4 все видимые экраны, участвующие в диспетчеризации (IDLE/BUSY),
кадр-стабильны; анимированные строки (спиннеры `⠏`/`.`) несут классифицируемый
текст (`Waiting for user confirmation`, `Initializing`) и удалять их нельзя.
Поле оставлено в схеме (нормализация ТЗ), значение — пусто.

## Версия агента и предупреждение

- Фикстуры сняты на **Qwen Code 0.24.4** (pin
  `QWEN_CODE_MANAGED_NPM_PIN` на staged-версию; проверено по
  `qwen --version` и ps-процессам).
- Автoupdater оператора переключил `~/.qwen/updates/npm/…/active.json` на
  **0.24.5** (2026-09-24 22:34). Следующий свежий запуск qwen — 0.24.5.
  Сессии с версией без фикстур помечаются TUI-флагом `untested` (раздел 7 ТЗ).
  Процесс обновления фикстур — в `docs/HANDOFF.md`.

## Как снимались фикстуры (воспроизводимость)

- tmux-сокет `-L aqmtest`, панели **200×50**. Важно: в tmux 3.2a
  `new-session -d -x -y` игнорируется (панель получает размер pty
  запускающей оболочки) — размер ставится
  `tmux resize-window -t <сессия>:0 -x 200 -y 50` после создания.
- Снимок: `tmux capture-pane -p -J -t <pane>` (видимый экран, `-J` склеивает
  переносы); файл = сырой вывод команды.
- 429/500: локальный stub OpenAI-сервера (127.0.0.1:18004, режимы 429/500);
  provider — project-override `modelProviders` в `/tmp/aqm-stub-proj`.
- `after_api_error`: чистый HOME (без `~/.qwen/memories` — иначе
  memory-селектор перехватывает 500 в pre-step), provider через env.
- `wait_compact`: `autoCompactThreshold: 0.5` + малое `contextWindowSize`
  (4096/16384) в project-настройках.
- Сессии запускались с пином 0.24.4
  (`QWEN_CODE_MANAGED_NPM_PIN={"version":"0.24.4",…}`), потому что
  `active.json` к моменту съёмок указывал на 0.24.5.

## Исправление инфраструктуры (найдено на этапе Э1)

- Makefile/CI: таргет `test` форсил `CGO_ENABLED=0`, с которым
  `go test -race` не запускается (−race требует cgo). Статика
  (`CGO_ENABLED=0`) теперь применяется только к vet/staticcheck/build;
  тесты гонятся с cgo (локально и в CI).
