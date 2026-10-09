# CHANGES W11–W12

Изменения поведения и конфигурации в W11 (silent-hold) и W12 (защита
self-update, асинхронный update API).

## W11: Silent-hold (удержание без ответа клиенту)

### Поведение

Когда слот не свободен, шлюз **не отвечает клиенту** — запрос удерживается
в памяти (`held_request=true`). Клиент (кодер) ждёт до
`profiles.qwen.client_timeout_sec` и отменяет запрос по таймауту.

- `gateway.hold_max_sec = 3600` (1 час) — максимум удержания.
- Параллельные удерживаемые запросы учитываются: каждый запрос сессии
  удерживается независимо; при выдаче слота удержанный запрос получает
  аренду и отправляется upstream.
- Причины отмены удержания: `HOLD_TIMEOUT` (hold_max_sec), `CANCEL`
  (оператор), `SESSION_GONE` (сессия удалена).
- `client_timeout_sec` — таймаут генерирующего запроса кодера (сек).
  Инвариант: `client_timeout_sec ≤ hold_max_sec` (кодер не ждёт дольше,
  чем шлюз удерживает).

### Передача кодеру

`runpilot run` выставляет:
- `QWEN_CODE_API_TIMEOUT_MS = client_timeout_sec × 1000`
- `--model` — из `profiles.qwen.model_alias`
- `--openai-base-url` — `http://<coord>:8787/s/<sid>/v1`

### Конфиг (новые/изменённые ключи)

| Ключ | По умолчанию | Описание |
|---|---|---|
| `gateway.hold_max_sec` | `3600` | Максимум удержания запроса (сек) |
| `profiles.qwen.client_timeout_sec` | `120` | Таймаут запроса кодера (сек) |

## W12: Защита self-update, асинхронный update API

### Поведение

- **Автообновление узлов удалено.** Mismatch версий узла и координатора →
  WARN в doctor + бейдж «версия ≠ координатора» на карточке узла в вебе.
  Обновление — только по команде оператора (API/веб/CLI/выкат).
- **Асинхронный update API:** `POST /api/v1/nodes/{host}/update` → `202 {id}`;
  `GET /api/v1/nodes/{host}/update/{id}` → `{stage, result, version}`.
  Стадии: `started → done | failed`.
- **Защита:**
  - Та же версия → `UPDATE_NOOP` (узел ничего не делает, не рестартируется).
  - Откат на старую версию (build_time target < current) → `UPDATE_REFUSED`,
    допускается только с `downgrade=true`.
  - SHA256 скачанного бинарника ≠ ожидаемому → `UPDATE_FAILED`.
  - Версия скачанного бинарника ≠ ожидаемой → `UPDATE_REFUSED`.
  - Stale dist (dist_dir ≠ версия координатора) → API `409 DIST_MISMATCH`.

### Конфиг (новые/изменённые ключи)

Новых ключей нет. Существующие:
| Ключ | По умолчанию | Описание |
|---|---|---|
| `coordinator.dist_dir` | `~/.local/share/runpilot/dist` | Каталог дистрибутива |
| `enroll.selftest_timeout_sec` | `30` | Таймаут selftest при update |

## exec: PID, время старта, группа процессов, отмена при обрыве

- `kill_process` несёт `pid`, `uid`, `start_time` — сверка идентичности
  перед kill (защита от PID-reuse).
- Отмена при обрыве: при обрыве TCP-соединения клиента шлюз закрывает
  upstream-соединение; vLLM прерывает генерацию.

## test=true и DIR_FORBIDDEN

- `test=true` в `RunRequest`/`SpawnRequest` — тестовая сессия: каталог обязан
  быть внутри `/mdisk/myhub/test` (иначе `400 DIR_FORBIDDEN`).
- `DIR_FORBIDDEN` — каталог вне `project_roots` узла или симлинк вне корней.

## Настройки в БД, сброс через API

- Настройки (project_roots и др.) хранятся в БД (`settings` table), не в
  config.yaml. Сброс через API: `POST /api/v1/settings/reset`.
- Проверка инвариантов — на эффективном конфиге (config.yaml + БД).

## Обновление узлов: процедура

1. Build clean HEAD: `CGO_ENABLED=0 go build -o /tmp/runpilot-new ./cmd/runpilot`
2. Ворота: `go test -race ./...` + `make staticcheck` + `make lint`
3. Deploy: `bash deploy/deploy-coordinator.sh /tmp/runpilot-new`
   (устанавливает бинарник + синхронизирует dist_dir + сверяет SHA256)
4. Update узлов: `POST /api/v1/nodes/{host}/update` (для каждого узла)
5. Проверка: версии всех узлов = версия координатора; SHA256 dist = SHA256
   координатора; 0 переходов NODE_STATE за 60 мин (кроме вызванных обновлением)

## Правила 12–14 (оперативные)

12. Рабочие сервисы — только через `systemctl --user` и deploy-скрипты.
13. Стенды не используют рабочие бинарники/порты/сокеты/БД.
14. Токены только из env-файлов, не в командных строках/логах/отчётах.
