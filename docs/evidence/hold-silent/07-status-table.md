# Таблица статуса — реализация silent-hold + client_timeout_sec (после 3-го отчёта)

Дата: 2026-10-06. Формат: пункт | статус | тест / команда | артефакт.

**Бинарник live-серии (финал):**
**runpilot `/tmp/hl/runpilot` SHA256 = `a8bf04c80588da68f9334899f6f52b78aeb8419e5ddf92db52185016284eca56`**
**fakellm `/tmp/hl/fakellm` SHA256 = `a1637f5e023c889dfeba737b705d3d250a86313bf8ae5ceaa95c1dccddd6902e`**
(HEAD: self-grant удалён, QueueUpsertHeld, clientGone, migration 012,
QueueUpsert-refactor, **client_timeout_sec** (A) + **граница тика** (Б);
fakellm: последний SSE-чанк несёт `finish_reason:"stop"`.)

## Пункты исходного списка — п.2, п.6, п.7, п.8 + api.md (добавлены после 3-го отчёта)

> 4 пункта, отсутствовавшие из исходного списка после подмены таблицы.
> Реализованы (где не было), каждый — с тестом и артефактом.

| # | Пункт | Статус | Тест / команда | Артефакт |
|---|---|---|---|---|
| **п.2** | **http.Server шлюза**: `ReadTimeout=0`, `WriteTimeout=0` — удержание соединения до `hold_max_sec` не обрывается таймаутом | ✅ PASS | `TestGatewayServerTimeouts` (оба поля `newHTTPServer(nil)` = 0) | `cmd/runpilot/serve.go` (`newHTTPServer`); **live п.2** — удержание **362 506 мс** без разрыва |
| **п.6** | **Видимость удержания**: (a) `held_since` в `queue_entry` (время по `internal/clock`); (b) веб — бейдж **HELD** + таймер `now−held_since`; (c) строка статуса tmux «… Q N/M held» (пуш координатор→узел); (d) `why=held_ok`, если нет более конкретной причины | ✅ PASS (п.6(c) закрыт live 2026-10-06, 5/5) | `TestStateHeldSince`, `TestStateNoHeldSinceWhenNotHeld` (`/api/v1/state` отдаёт `held_since`); `TestQueueViewHeld` (рендер строки очереди: `held`+`held_since`); `TestQueueHeldStatusLine` (текст «Q N/M held»); `why=held_ok` — `TestPrimaryReasonHeldOK`/eligibility; **п.6(c)** — `TestPusherQueueChangeSendsNewStatus`/`TestPusherUnchangedNotResent`/`TestPusherReconnectResendsAll` + **live 5/5** (статус B = `Q 1/1 held`) | (a) `internal/store/queue.go` + миграция `013_queue_entry_held_since.sql` + `internal/gateway/lease.go` (holdPass); (b) `web/app/views/queue.js` (`HeldTimer`+`Badge`) + `components.css` (`.qrow-held`); (c) `internal/scheduler/views.go` (`QueueHeldStatus`) + **`internal/api/status_push.go`** (`StatusPusher` пуш set_status/set_summary) + `Scheduler.OnStatus`/`SummaryText`; (d) `internal/scheduler/eligibility.go` (`primaryReason`); live — `08-p6c-tick-boundary-live-2026-10-06.md` |
| **п.7** | **Восстановление после рестарта координатора**: записи `held_request=true` → `held_request=false`, `mode=resume`, `enqueued_at` сохранён (на каждом старте) | ✅ PASS | `TestRecoverHeldOnStartup` (`-race`): БД с held-записью → старт → held=0, mode=resume, enqueued_at без изменения, idempotent | `internal/scheduler/recover.go` (`RecoverHeldOnStartup`) + `cmd/runpilot/serve.go` (старт) |
| **п.8** | **doctor на клиенте**: в пакете установленного qwen есть строка `QWEN_CODE_API_TIMEOUT_MS`, иначе **FAIL** с версией qwen и путём пакета | ✅ PASS | `TestQwenTimeoutGuardPass` (фейковый пакет со строкой → PASS), `TestQwenTimeoutGuardFail` (без строки → FAIL с версией+путём) | `internal/doctor/doctor.go` (`checkQwenTimeoutGuard` + `qwenPkgContains`) |
| **api.md** | Схема ответа регистрации сессии с `client_timeout_ms` | ✅ PASS | `docs/api.md` → `RunResponse (POST /run, 201)` = `{sid, client_timeout_ms}` (источник только координатор, `profiles.qwen.client_timeout_sec × 1000`) | `docs/api.md` (+ обновлены `QueueRow.held/held_since`, `State.held_since`) |

Примечание к **п.6(c)**: текст строки статуса (`QueueHeldStatus`) реализован и
тестируется; узел выставляет `@runpilot_status` (`node/commands.go`) и
`tmuxsetup.go` подключает его к status-right. Периодический пуш
«координатор → узлы» (сообщение `set_status`) — следующий интеграционный шаг;
данные и текст для него готовы.

## Пункты реализации (1–9)

| # | Пункт | Статус | Тест / команда | Артефакт |
|---|---|---|---|---|
| 1 | **Silent-hold**: `holdPass` — запись `held_request=true`, ожидание до `gateway.hold_max_sec`, без self-grant | ✅ PASS | live п.2 + `TestHoldSlotGranted` | `00` / `02-p2-after-fix` |
| 2 | **Планировщик — единственный выдающий аренду**: self-grant шлюза удалён; LEASE_GRANT только из `tickGrant`→`grant`→`LeaseCreateIfAbsent` | ✅ PASS | `TestHoldSlotGranted`, `TestHoldRankResumeBeatsHeld`, `TestHoldCapacityOneSlotTenFrees` (`-race`) | `06-p10-virtual-clock` |
| 3 | **Esc/отмена во время удержания → clientGone**: `QueueDelete` + `QUEUE_DEQUEUE{reason:client_gone}` + `request.error=CLIENT_GONE` + сессия IDLE | ✅ PASS | `TestP4ClientGone` | `04-p4-esc-after-fix` |
| 4 | **Повтор удержания (ретраи) сохраняет позицию**: `QueueUpsertHeld` CONFLICT — `enqueued_at=MIN`, `attempts=MAX`, `class=MIN`; общий `QueueUpsert` — перезапись | ✅ PASS | `TestP5PreserveEnqueuedAt`, `TestQueueUpsertHeldMerge` | `05-p5-holdmax20-after-fix` |
| 5 | **hold_max таймаут → 503 + Retry-After + heldToResume**: `hold_max_sec` истёк → `errHoldTimeout`, запись `held_request=false, mode=resume`; клиент 503 + `Retry-After` + `runpilot_no_slot` | ✅ PASS | live п.5 (`hold_max_sec=20`) | `05-p5-holdmax20-after-fix` |
| 6 | **client_timeout_sec (A.1/A.3/A.4)**: `profiles.qwen.client_timeout_sec` (default 5400); `POST /run` → `client_timeout_ms`; `runpilot run` ставит `QWEN_CODE_API_TIMEOUT_MS` после env-переменных профиля (перекрывает операторский override) | ✅ PASS | `TestCoderEnvClientTimeoutOverride` + live п.2 (environ) | `docs/api.md` / `02-p2-after-fix` |
| 7 | **Граница тика (Б)**: один `now` на тик — LEASE_* события (`eventAt(now,·)`), `closeTurn`, `GrantedAt`/`ReleasedAt` берут `now` тика (не свежий `clk.Now()`); аренда в тике T → выдача в тике T+cooldown (не T+cooldown+tick) | ✅ PASS | `TestTickBoundaryReleaseToGrant` (`-race`) | `02-p2-after-fix` (критерий 8) |
| 8 | **Live Этап 0 (до) + live п.2 after-fix**: точные числа — B в окне=1, других=0, LEASE_RELEASE(A)→GRANT(B)=2499.95 мс, held_ms=362 506 мс, send−GRANT=3.882 мс | ✅ PASS (8/8) | stand (tmux/curl) | `00` / `02-p2-after-fix` |
| 9 | **Рассе-тесты + staticcheck + HANDOFF**: `go test -race ./...` — **21 ok / 0 FAIL**; `staticcheck ./...` — **EXIT=0**, находок нет | ✅ PASS | `go test -race ./...` · `make staticcheck` | `step3-race-staticcheck-2026-10-06.txt` |

## В.7–В.8 (доп. валидация и default)

| # | Пункт | Статус | Тест / команда | Артефакт |
|---|---|---|---|---|
| В.7 | **Валидация — строгий пол**: нарушение (exit 2) ⟺ `client_timeout_sec ≤ gateway.hold_max_sec + max(servers[].first_byte_timeout_sec)`; равенство — нарушение. Текст ошибки содержит `hold_max_sec`, `max(first_byte)` **с именем сервера**, `client_timeout_sec`, требуемый минимум (сумма+1). 5 кейсов: `=hold+max_fb`→exit 2; `=hold+max_fb−1`→exit 2; `=hold+max_fb+1`→OK; дефолты 5400/3600+fb=900→OK; стенд 600/900/default→OK | ✅ PASS (5/5) | `TestValidateV7ClientTimeoutFloor` (`-race`) | `internal/config/validate_45_test.go` |
| В.8 | **`gateway.hold_max_sec` default 3600** (было 60) | ✅ PASS | `internal/config/defaults.go` (поле `HoldMaxSec: 3600`) | `defaults.go` |

## Отдельная строка (источник `QWEN_CODE_API_TIMEOUT_MS`)

**`QWEN_CODE_API_TIMEOUT_MS` выставляет `runpilot run` из `profiles.qwen.client_timeout_sec`, а не override стенда.**
Доказано live п.2: `/proc/<pid>/environ` = `QWEN_CODE_API_TIMEOUT_MS=5400000`
(= `client_timeout_sec` 5400 × 1000, значение по умолчанию). В конфиге стенда
override **отсутствует**: `profile.qwen.yaml` переменную не содержит,
`profile.qwen.noenv.yaml` удалён. Тест перекрывания:
`TestCoderEnvClientTimeoutOverride` (операторский `Env.QWEN_CODE_API_TIMEOUT_MS=1`
→ итог 5400000 из координатора).

## Сводка по live-серии п.2/п.3/п.4/п.5 (один бинарник HEAD, реальный qwen)

| Пункт | Статус | Ключевое число |
|---|---|---|
| п.2 (silent-hold) | ✅ PASS (8/8) | held_ms=362 506; LEASE_RELEASE→GRANT=2499.95 мс; send−GRANT=3.882 мс |
| п.3 (контроль, noenv) | ✅ PASS | разрыв ≈120 с от Enter; 1 retry за 300 с |
| п.4 (Esc во время удержания) | ✅ PASS | `QUEUE_DEQUEUE{client_gone}` + запись удалена + IDLE |
| п.5 (hold_max_sec=20) | ✅ PASS | 503 + `Retry-After:30` + `runpilot_no_slot`; `enqueued_at` сохранён |

## Итог

Все 9 пунктов реализации + В.7 + В.8 — **PASS** на бинарнике `ff993967…`
(HEAD: + п.1 = п.6(c) пуш set_status/set_summary + граница тика).
**п.6(c) закрыт live 2026-10-06 (5/5)**: строка статуса B во время удержания =
`Q 1/1 held` = `QueueHeldStatus` (`08-p6c-tick-boundary-live-2026-10-06.md`).
Self-grant шлюза устранён (аренду выдаёт только планировщик).
**Граница тика исправлена live**: `LEASE_RELEASE(A)→LEASE_GRANT(B) = cooldown
(+0…+2 мс)` (2000.21…2001.95 мс, 5/5) — было 2499.95 мс (cooldown+tick).
`QWEN_CODE_API_TIMEOUT_MS` — источник только координатор
(`profiles.qwen.client_timeout_sec`), не override стенда.
`go test -race ./...` — 24 ok / 0 FAIL; `staticcheck ./...` — EXIT=0.
