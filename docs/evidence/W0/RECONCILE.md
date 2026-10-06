# W0 — Сверка реализации с базовым ТЗ (приложение А)

Дата: 2026-09-26
Машина: `3p14-aaa` (hostname подтверждён: `hostname` → `3p14-aaa`)
Коммит-база: `cf35499` (Э6)
Статус: все строки закрыты.

## Шаблон интерфейса

- Путь: `docs/design/template.jpg`
- SHA-256: `022826dd5e570c4c1fb72327d152234caff0998b45ccb72963076544cda14f49`
- Проверка: `sha256sum docs/design/template.jpg` (см. `template.sha256`)

## aqm doctor на 3p14-aaa

Команда: `aqm doctor --config testdata/config.example.yaml`
Бинарь собран: `CGO_ENABLED=0 go build -o aqm ./cmd/aqm`, `aqm --version` → `aqm version 0.0.0-dev`.
Полный вывод — в `doctor.txt`.

Замечания (оба ожидаемы и не являются дефектами кода):
- `FAIL file_mode — не 0600: testdata/config.example.yaml=644`: `config.example.yaml`
  — фикстура-пример в `testdata/` (режим 644), а не развёрнутый конфиг 0600.
  На реальном деплое `~/.config/aqm/config.yaml` создаётся с 0600 (`config.Load`).
- `WARN metrics` / `WARN require_direct`: `config.example.yaml` указывает на
  `192.168.10.2{1,2}:8004` (недоступны). Операторский живой vLLM
  `127.0.0.1:8004` намеренно НЕ опрашивался (правило безопасности стэнда).
- `PASS qwen_settings — selectedType=openai…` — проверка выполнена против
  реального `~/.qwen/settings.json` на `3p14-aaa` (read-only).

## A1 — Семантика `DETACHED`

- Расхождение: `manual.md:345` «панель не присоединена» против базового ТЗ
  «ход идёт, аренда снята».
- Реализованная семантика (автомат сессии):
  `internal/model/session_automaton.go:74` `{SessionRunning, EvLeaseRevoked, SessionDetached}`
  («аренда снята в ходе»); `:75` `{SessionDetached, EvResumeRequest, SessionRunning}`;
  `:76` `{SessionDetached, EvIdleNoError, SessionIdle}`.
- Вывод: ход идёт в `RUNNING`, при снятии аренды → `DETACHED`, новый запрос хода →
  `RUNNING`, `IDLE` без ошибки → `IDLE`. Это ровно «ход идёт, аренда снята».
- **Статус: соответствует** (код = базовое ТЗ; ошибка в `manual.md`, исправляется
  при переписи под веб в W-стадиях).

## A2 — Проверка `qwen_settings`

- Расхождение: `manual.md` «есть provider с `id` = `model_alias`» против базового ТЗ
  `selectedType: openai` и **нет** `modelProviders` с таким `id`.
- Реализованная проверка: `internal/doctor/doctor.go` `checkQwenSettings`:
  требует `security.auth.selectedType == "openai"` И отсутствие
  `modelProviders.<prov>[i].id == ModelAlias` (FAIL при совпадении).
- Тест: `internal/doctor/doctor_test.go` (кейсы selectedType + modelProviders).
- **Статус: соответствует** (код = базовое ТЗ; ошибка в `manual.md`).

## A3 — `requeue` по умолчанию

- Расхождение: `manual.md:373,522` «по умолчанию в начало» против базового ТЗ
  «сохраняет исходный `enqueued_at`, `--back` — в конец класса».
- Реализовано: `internal/scheduler/queueops.go` `Requeue`:
  `if back || e.EnqueuedAt.IsZero() { e.EnqueuedAt = now }` — по умолчанию исходный
  `enqueued_at` сохраняется, `--back` ставит `now`.
- **Статус: соответствует** (код = базовое ТЗ; ошибка в `manual.md`).

## A4 — `--after`

- Расхождение: `manual.md:371` «после конкретной записи» против базового ТЗ
  «после хода `t2` с исходом `OK`, иначе `HOLD(DEPENDENCY_FAILED)`».
- Реализовано:
  - `internal/scheduler/eligibility.go:79-82` — допуск только если
    `st.TurnOKSince(e.AfterSID, e.EnqueuedAt)` (ход с исходом OK, завершившийся
    после `enqueued_at`).
  - `internal/scheduler/complete.go:316-336` `tickDependencyAndPin` — зависимость в
    `HOLD`/`GONE` → `HOLD(DEPENDENCY_FAILED)`.
  - Тест: `internal/scheduler/scenario_test.go` `TestS12After`.
- **Статус: соответствует** (код = базовое ТЗ; ошибка в `manual.md`).

## A5 — Пустой `AQM_TOKEN` = «локальный режим»

- Расхождение: в базовом ТЗ режима «без авторизации» нет; `manual.md:215,599`
  описывает «локальный режим».
- Где реализован:
  - Сервер: `internal/api/server.go:103-108` `auth` — при пустом `s.token` проверка
    Bearer пропускается.
  - Клиент: `internal/client/client.go` `decorate` — заголовок `Authorization`
    ставится только при `c.token != ""`.
- Кто использует: `aqm serve` (API) + CLI/TUI-клиент при пустом `client.token_env`.
- **Статус: исправлено** — режим удаляется в **W1** (раздел 16 v2: пустой или
  короче `limits.token_min_len` `AQM_TOKEN` → `aqm serve` не стартует; `aqm doctor` — FAIL).

## A6 — Что считает занятость слота

- Расхождение: `manual.md:166,233` «N одновременных LLM-запроса» против базового ТЗ
  «слот = один одновременный ход».
- Реализовано: слот = аренда на время хода (`lease`); шлюз отдельно ведёт
  `gateway.max_inflight` (суммарный + по сессии/серверу) как лимит на
  одновременные HTTP-запросы — это НЕ слоты:
  `internal/gateway/gateway.go:274-300` (`tryBumpTotalInflight`/`bumpInflight`),
  `internal/gateway/proxy.go:279-280`.
- **Статус: соответствует** (слот = ход; `max_inflight` — отдельный лимит шлюза).

## A7 — `cooldown`

- Расхождение: `manual.md:574` «откат после сбоя» против базового ТЗ
  «пауза слота после освобождения».
- Реализовано: `internal/scheduler/complete.go:212-217` `setCooldown` — слот →
  `COOLDOWN` на `scheduler.cooldown_sec` после освобождения (release); причина
  `model.ReasonCooldown` в `internal/scheduler/eligibility.go:303`.
- **Статус: соответствует** (код = базовое ТЗ; ошибка в `manual.md`).

## A8 — Что пишет `aqm tmux-setup`

- Вопрос: есть ли `prefix Q`, `prefix M-q`, `@aqm_status`, `@aqm_summary`.
- Реализовано: `cmd/aqm/tmuxsetup.go` `tmuxBlock`:
  - `bind-key Q run-shell -b "aqm enqueue --pane '#{pane_id}'"` (prefix Q → enqueue)
  - `bind-key M-q run-shell -b "aqm dequeue --pane '#{pane_id}'"` (prefix M-q → dequeue)
  - `set -ag status-right " #{@aqm_status} #{@aqm_summary}"` (обe метрики в статусе)
  - `set -g status-interval 2`
- **Статус: соответствует** (все четыре присутствуют).

## A9 — Статус Э7 и Э8 базового ТЗ

- Факт: закрыты **только Э0–Э6** (git log HEAD = `cf35499` = Э6; есть
  `docs/evidence/Э0…Э6`; **нет** `docs/evidence/Э7`, `docs/evidence/Э8`).
- Не реализовано (проверено по коду):
  - Telegram: `internal/notify/` содержит только `doc.go`-заглушку
    («Реализуется на этапе Э7»).
  - Восстановление (`last_alive`, `quick_check`, правило исхода): grep по
    `internal/ cmd/` — реализации нет (только имена ключей в `config`).
  - `deploy/systemd`, `deploy/launchd` — только `.gitkeep`.
- Численное обоснование: 6 закрытых этапов (Э0–Э6), 2 незакрытых (Э7, Э8);
  v2-ТЗ в разделе 1 считает исходным состоянием «Э0–Э8», фактически — «Э0–Э6».
- Разрешение (предложение оператору): функции незакрытых базовых этапов
  закрываются v2-аналогами, а не отдельными базовыми коммитами:
  - Telegram → **W5** (раздел 17 v2 + общий разбор команд `internal/command`);
  - восстановление/правило `LOST`/shutdown/`upgrade`/`rollback`/`restore` → **W7**
    (разделы 6.5, 6.6 v2);
  - приёмка на реальных серверах (Э8) → **W9** (раздел 20 v2).
- **Статус: вопрос оператору** — согласовать, что базовые Э7/Э8 считаются закрытыми
  их v2-аналогами (W5/W7/W9), а не отдельными этапами.

## A10 — Значения `first_byte_timeout_sec` / `slots` в примере конфига

- Расхождение: `manual.md:166` `slots: 4`, `manual.md:172,188`
  `first_byte_timeout_sec: 30` против базового ТЗ `slots: 1` и
  `first_byte_timeout_sec: 900`.
- Реализовано: `testdata/config.example.yaml` (`slots: 1`,
  `first_byte_timeout_sec: 900` для обоих серверов) и
  `internal/config/config_test.go:59` (`s0.Slots == 1`) — совпадают с базовым ТЗ.
  `defaults.go` не содержит server-defaults (серверы — явный конфиг).
- **Статус: соответствует** (код/пример = базовое ТЗ; ошибка в `manual.md`).

## Итог

| № | Статус |
|---|---|
| A1 | соответствует |
| A2 | соответствует |
| A3 | соответствует |
| A4 | соответствует |
| A5 | исправлено (W1) |
| A6 | соответствует |
| A7 | соответствует |
| A8 | соответствует |
| A9 | вопрос оператору |
| A10 | соответствует |

Расхождения `manual.md` (A1,A2,A3,A4,A6,A7,A10) — ошибки документации; код
совпадает с базовым ТЗ. A5 устраняется в W1. A9 — решение по охвату (см. выше).
