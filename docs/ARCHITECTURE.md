# RunPilot (runpilot) — архитектура

Единый статический бинарник на Go (CGO_ENABLED=0), три роли:
координатор (`runpilot serve`), узел (`runpilot node`), клиент (`runpilot` — TUI/CLI).

## Топология

- Кодеры (Qwen Code в tmux) ходят **только в шлюз** координатора:
  `OPENAI_BASE_URL=http://<coord>:8787/s/<sid>/v1`.
- Узел управляет **только панелями** своей машины: `exec.Command` без
  оболочки (`tmux`, `ps`, `nvidia-smi`, `rocm-smi`).
- Решения о выдаче слотов принимает **только планировщик** координатора.

| Порт | Назначение |
|---|---|
| 8787 | Шлюз `/s/<sid>/…` (SSE-прокси) |
| 8788 | REST, SSE, WebSocket узлов, `/metrics` |

## Ключевые инварианты

1. Не более N одновременных ходов на сервер, где N = его слоты
   (аренды, а не GPU-загрузка — ADR-2).
2. Ожидание слота штатной постановки — **до** Enter; запрос без аренды
   удерживается в шлюзе ≤ `gateway.hold_max_sec` (раздел 6).
3. Каждый переход состояния сначала пишется в SQLite, потом — побочный
   эффект (раздел 14).
4. Время в планировщике, автоматах и таймаутах — только `internal/clock`;
   тесты — на виртуальных часах (строгое правило 9).
5. Свежесть снимка панели — по `received_at` **координатора** (ADR-9).
6. Числа (пороги, таймауты, лимиты) — только из конфигурации;
   defaults — только `internal/config/defaults.go` (строгое правило 2).
   Контроль: `scripts/check-literals` по `internal/{scheduler,gateway,detect}`.
7. Полей токенов в БД нет; тела запросов не логируются (строгие правила 3, 7).

## Состояние и автоматы

- **Сессия**: IDLE / QUEUED / DISPATCHING / RUNNING / DETACHED / HOLD / GONE.
  Таблица переходов — `internal/model/session_automaton.go` (32 перехода).
- **Сервер**: UP / DOWN / DRAINING; серии health — чистая функция
  `ServerHealth.Observe` (downAfter/upAfter из конфига).
  Решения Э0: старт в UP до первой проверки; из DRAINING health-отказ ведёт
  в DOWN, восстановление из DOWN возвращает в UP (намерение оператора
  сохраняется при повторном drain/undrain).
- **Слот**: FREE / LEASED / COOLDOWN / EXTERNAL; EXTERNAL — после
  непрерывного ext>0 в течение `scheduler.external_confirm_sec`
  (чистые функции `Slot.ExternalReady/ExternalCleared`).
- **Аренда**: PENDING / ACTIVE / RELEASED / RECOVERING;
  origin DISPATCH / IMPLICIT / Migrated.

## Пакеты

| Пакет | Этап | Назначение |
|---|---|---|
| `internal/config` | Э0 | строгий YAML (KnownFields), defaults.go, валидация |
| `internal/model` | Э0 | сущности, автоматы как чистые функции |
| `internal/store` | Э0 | SQLite (modernc, WAL, одна горутина-писатель), миграции, retention |
| `internal/clock` | Э0 | Real/Virtual часы |
| `internal/doctor` | Э0+ | построчные PASS/WARN/FAIL |
| `internal/detect` | Э1 | классификатор панелей, извлечение ввода |
| `internal/node`, `internal/tmux` | Э2 | узловой демон, tmux-вызовы |
| `internal/gateway` | Э3 | прокси, удержание, миграция |
| `internal/testutil/fakellm`, `cmd/runpilot-mockcoder` | Э1/Э3 | тестовые двойники |
| `internal/scheduler` | Э4 | тик, ранги, допустимость, выбор сервера |
| `internal/api`, `internal/tui` | Э2/Э5 | REST+SSE, TUI |
| `internal/monitor` | Э6 | health, метрики vLLM, GPU |
| `internal/notify` | Э7 | Telegram |

## Коды выхода CLI

- 0 — успех;
- 1 — runtime-ошибка или любой FAIL в `runpilot doctor`;
- 2 — нарушение схемы/семантики конфигурации (включая неизвестный ключ).

## Хранилище

SQLite WAL, `busy_timeout=5000`, одна горутина-писатель
(`Store.DoWrite`/`QueueWrite`), параллельное чтение. Миграции —
`internal/store/migrations/NNN_*.sql` + `schema_version`.
Файл БД — 0600. Времена — фиксированный RFC3339-UTC с 9 знаками дробной
части (порядок строк = порядок времени, `model.FormatTime`).
`server_runtime` хранится в памяти координатора и восстанавливается
монитором при старте (раздел 14: персистентны очередь и аренды).
