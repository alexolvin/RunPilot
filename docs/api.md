# runpilot API координатора — Э5

Порт: `coordinator.api_port` из `~/.config/runpilot/config.yaml` (в приёме — 27788).
Все пути в разделе `/api/v1`. Машинная схема ответов — JSON; минимальные поля
`payload` событий по kind зафиксированы ниже (приложение Б ТЗ, TZ:1221) — это
контракт для TUI/CLI **до** золотых снимков.

## Аутентификация

`Authorization: Bearer $<coordinator.token_env>`. Пустой токен (env не задан) —
локальный режим без проверки. Ошибки: `{"code":"…","detail":"…"}` + HTTP-статус:
400 неверный запрос / недопустимая команда, 401 нет токена, 404 нет сущности,
409 недопустимый переход (тело несёт `code`), 422/502 сбой диспетчеризации,
503 планировщик не запущен.

## Адресация `{t}`

`sid` | имя сессии | `host:tmux_session[:window.pane]`. Разрешение в `sid` делает
координатор (`resolveSession`). Клиент дополнительно поддерживает `--pane %N`
и голый `%N`.

## Команды и чтение

| Метод | Путь | Тело | Ответ | Назначение |
|---|---|---|---|---|
| POST | `/run` | `RunRequest` | `201 RunResponse` | Регистрация сессии (`runpilot run`) |
| PATCH | `/sessions/{sid}/pane` | `{tmux_session, window, pane_id, agent_version}` | `204` | Панель сессии |
| GET | `/state` | — | `State` | Срез: сессии, панели, узлы |
| GET | `/sessions?all=` | — | `[SessionRecord]` | Сессии (`runpilot ls`) |
| GET | `/queue` | — | `[QueueRow]` | Очередь с рангом и WHY (`runpilot q`) |
| GET | `/servers` | — | `[ServerRow]` | Серверы и слоты (`runpilot servers`) |
| GET | `/doctor` | — | `{servers:[DoctorRow]}` | Серверная часть doctor (раздел 13, Э6) |
| GET | `/stats?since=24h` | — | `StatsResult` | Ходы по серверам/сессиям, без токенов |
| GET | `/sessions/{t}/why` | — | `Why` | Причина недопуска (приложение А) |
| GET | `/sessions/{t}/peek?n=40` | — | `text/plain` | Экран панели (`runpilot peek`) |
| POST | `/sessions/{t}/enqueue` | `{class,pin,prefer,front,after}` | `204` | В очередь |
| POST | `/sessions/{t}/dequeue` | — | `204` | Снять с очереди |
| POST | `/sessions/{t}/requeue` | `{back}` | `204` | Перепоставить (front/back) |
| POST | `/sessions/{t}/prio` | `{class}` | `204` | Сменить класс |
| POST | `/sessions/{t}/pin` | `{server}` | `204` | Привязка |
| POST | `/sessions/{t}/prefer` | `{server}` | `204` | Предпочтение |
| POST | `/sessions/{t}/unpin` | — | `204` | Снять привязку |
| POST | `/sessions/{t}/hold` | — | `204` | Ручной hold |
| POST | `/sessions/{t}/unhold` | — | `204` | Снять hold |
| POST | `/sessions/{t}/auto-enqueue` | `{on}` | `204` | Автопостановка |
| POST | `/sessions/{t}/cancel` | — | `204`/`502` | Прервать ход |
| PATCH | `/sessions/{t}` | `{orch_url, relay_enabled}` | `200 relayStatus` | Связь с проектом (ТЗ Relay §15.4) |
| GET | `/sessions/{t}/relay` | — | `200 relayStatus` | Статус связи |
| POST | `/sessions/{t}/relay/{send\|deliver\|skip\|resume}` | — | `200 relayStatus` | Действие связи |
| POST | `/relay/check` | `{url}` | `200 relayCheck` | Проверить ссылку на чат |
| GET | `/browser` | — | `200 {running, tabs}` | Состояние браузера |
| WS | `/browser/view?tab=` | — | JPEG-кадры | Живой просмотр вкладки |
| POST | `/pause` | `{on}` | `204` | Пауза выдачи |
| POST | `/resume` | — | `204` | Снять паузу |
| POST | `/servers/{s}/drain` | `{on}` | `204` | Вывод сервера |
| POST | `/nodes/{h}/drain` | `{on}` | `204` | Вывод узла |
| POST | `/nodes/{h}/update` | `{downgrade?}` | `202 {id}` | Асинхронный update узла (W12) |
| GET | `/nodes/{h}/update/{id}` | — | `{id,host,stage,result,version}` | Статус update |
| GET | `/node/version` | — | `{version}` | Версия координатора (для doctor узла) |
| GET | `/events?after=&limit=` | — | SSE / `[Event]` | События (SSE, или JSON при `limit`) |
| POST | `/internal/dispatch` | `{sid,pane_id,mode,expect_hash}` | `{result,detail,rtt_ms}` | Замер dispatch (тесты) |
| GET | `/internal/dispatch-latency` | — | `{n,p95_ms,samples_ms}` | p95 диспетчеризации |
| WS | `/node` | proto=1 | — | Канал узла (раздел 13) |

### `RunRequest` (POST /run)

`{name, host, host_ip, profile, agent_version, prio, pin, prefer, auto_enqueue}`.
`profile` обязан быть `qwen` (иначе `400 BAD_AGENT`). Коллизии: `409 NAME_IN_USE`
(живая или свежая GONE сессия), `400 BAD_PRIO` (неизвестный класс).

### `RunResponse` (POST /run, 201)

`{sid, client_timeout_ms}`.
- `sid` — SID созданной сессии.
- `client_timeout_ms` — таймаут генерирующего запроса кодера (мс); источник
  **только координатор**: `profiles.qwen.client_timeout_sec × 1000`. В конфиге
  клиента ключа нет. `runpilot run` выставляет его в `QWEN_CODE_API_TIMEOUT_MS`
  кодера (перекрывает встроенный/override-профиль и окружение оператора).

### `QueueRow` (GET /queue)

`{rank, mode, class, session, host, sid, why?, wait_sec, attempts, prompt?,
held?, held_since?}`.
`why` — причина последнего пропуска (пусто, если слот доступен); для удерживающей
записи, у которой все слоты заняты, `why=held_ok` (нет более конкретной причины).
`prompt` — первые 80 символов ввода панели. `held` (п.6) — запись удерживает
запрос; `held_since` — время начала удержания (RFC3339Nano, часы координатора):
веб показывает бейдж HELD и таймер `now − held_since`.

### `State` (GET /state) — сессии

Каждая сессия (`sessions[]`) несёт `held_since?` (п.6) — время начала удержания
запроса (то же поле `queue_entry.held_since`), присущее только сессии с
`held_request=true`; у прочих опущено.

### `ServerRow` (GET /servers)

`{name, priority, state, slots, used, slots_info:[{slot, state, session?, turn_since?}],
gpu?, vram_used_gb?, vram_total_gb?, kv_pct?, gen_tok_s?, ext?, metrics_missing?, gpu_cards?[]}`.
`state`: `UP|DRAINING|DOWN`. `slots_info[].state`: `free | external | cooldown |
состояние аренды` (ACTIVE/…); `external` — резерв под внешнюю нагрузку
(первые N свободных, N = min(ext, свободные), подтверждение
`external_confirm_sec`). Мониторинг (Э6): `gpu` — утилизация GPU, % (max по
картам узла); `vram_used_gb/vram_total_gb` — VRAM (сумма по картам); `kv_pct` —
`kv_cache_usage_perc`×100; `gen_tok_s` — скорость генерации (окно
`rate_window_sec`); `ext` — внешняя нагрузка (раздел 5, подтверждается
`external_confirm_sec`); `metrics_missing` — нет метрик (тогда `ext` опущен,
**не** молчаливый 0); `gpu_cards` — разбивка по картам (узел). Всё опущено,
пока данные не получены (TUI/CLI показывают «—»).

### `DoctorRow` (GET /doctor)

`{name, state?, metrics_missing, found_vllm?[], absent_vllm?[]}`. `state` —
`UP|DRAINING|DOWN` из планировщика; `found_vllm`/`absent_vllm` — найденные и
отсутствующие required `vllm:` метрики последнего сбора (раздел 10 ТЗ).

### `StatsResult` (GET /stats)

`{by_server:[{server,turns,ok,err,total_sec,avg_sec}], by_session:[{session,sid,turns,ok,err,total_sec}]}`.
Без токенов.

### `Why` (GET /sessions/{t}/why, приложение А)

`{sid, state, reason?, predicates?, candidates:[]}`. `reason` пуст — слот доступен.

## События (SSE /events, приложение Б ТЗ)

Кадры SSE: `id: <n>` / `event: <KIND>` / `data: {json}`. Поле `id` — монотонный
номер (для `after=`). `after=0` — начальная лента (последние 200). Keep-alive —
комментарий `: keep-alive` каждые 15 с. `?limit=N` — разовый JSON-ответ
(последние N) для `runpilot events` без `-f`.

Каждое событие: `{kind, sid?, server?, ts, payload}`. **TUI обязан показать
неизвестный `kind` как сырой JSON и не падать** (TZ:1219).

### Минимальные поля `payload` по kind (TZ:1221)

| kind | sid/server | payload (минимум) |
|---|---|---|
| `SESSION_STATE` | sid | `from, to` · (+`auto_enqueue:bool` при смене) |
| `QUEUE_ENQUEUE` | sid | `class, mode, front` · или `requeue:true` · или `auto:true, attempts:N` |
| `QUEUE_DEQUEUE` | sid | `{}` |
| `QUEUE_SKIP` | sid | `reason, prev` |
| `LEASE_GRANT` | sid, server | `server, slot, origin(DISPATCH\|IMPLICIT)` · (+`held:true`) |
| `LEASE_RELEASE` | sid, server | `reason, server, slot` |
| `DISPATCH` | sid[, server] | `pane_id, mode, expect_hash` · или `result, dispatch_ms` · или `turn_started:id` |
| `TURN_END` | sid | `turn_id, outcome, requests, servers` |
| `SERVER_STATE` | server | `server, state` |
| `NODE_STATE` | server | `host, state(UP\|DOWN)` · или `host, drain:bool, ts` |
| `HOLD` | sid | `reason` |
| `NOTIFY` | sid | `action` (напр. `cancel`) |

Примечания:
- `ts` — RFC3339Nano, часы **координатора**.
- `DISPATCH` несёт разные payload по фазе (заказ → подтверждение → старт хода);
  TUI использует `kind` + первые доступные поля, неизвестные поля игнорирует.
- Добавление **обязательного** поля в существующий kind без версии события —
  дефект (контракт фиксирован выше).

## Протокол канала узла

JSON, `proto = 1` (раздел 13 ТЗ). Координатор версии N принимает узлы N и N−1
одной мажорной версии; неизвестная мажорная — закрытие соединения. `host_ip` в
`hello` обязан совпадать с удалённым адресом (раздел 14).

Mismatch версий узла и координатора → WARN в doctor координатора + бейдж
«версия ≠ координатора» на карточке узла в вебе. **Автообновление нет** (W12).

## Update узла (W12)

`POST /api/v1/nodes/{host}/update` — асинхронный update. Тело: `{downgrade?}`
(опционально, для отката на старую версию). Ответ: `202 {id, host}`.

Статус: `GET /api/v1/nodes/{host}/update/{id}` → `{stage, result, version}`.
Стадии: `started → done | failed`.

Защита (узел):
- Та же версия → `UPDATE_NOOP` (без скачивания/рестарта).
- Downgrade без `downgrade=true` → `UPDATE_REFUSED`.
- SHA256 ≠ ожидаемому → `UPDATE_FAILED`.
- Версия бинарника ≠ ожидаемой → `UPDATE_REFUSED`.

Защита (координатор):
- Stale dist (dist_dir ≠ версия координатора) → `409 DIST_MISMATCH`.
