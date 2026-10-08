# Стенд с настройками оператора + /v1/models vLLM: диагноз 30s client_gone

**Дата:** 2026-10-09
**Гипотеза оператора:** отличие стенд/прод — settings.json (свой HOME vs настройки оператора) и /v1/models (fakellm vs vLLM); первый удержанный запрос — служебный запрос qwen с коротким таймаутом.

## Результат: 30s client_gone НЕ воспроизведён

B удерживался **2м54с** (21:49:49 → 21:52:43) **без client_gone** (0 событий QUEUE_DEQUEUE/client_gone). Затем получил слот (LEASE_GRANT) и завершил ход.

## П.1 Артефакт прогона на vzu5 (20:23)

- **capture-pane B2 около 30-й секунды: НЕ БЫЛО** (монитор vzu5-прогона — только DB-запросы, без capture-pane).
- **Журнал --openai-logging: НЕ БЫЛ** (B2 поднят через прод-нод, профиль `command: qwen` без --openai-logging; каталога `logs/openai` в dir B2 не существует).

## П.2 Конфигурация стенда (максимально близко к проду)

| Параметр | Значение |
|---|---|
| settings.json кодера | **копия оператора** `/home/user/.qwen/settings.json` (modelProviders openai/openai-vzu5, model.name=qwen3.8-27b, contextWindowSize=262144) |
| /v1/models fakellm | **снимок vLLM vzu5** (`vllm-vzu5-models.json`: qwen3.8-27b + qwen3.8-27b-fp8, max_model_len=262144, permission) через новый флажок `-models-file` |
| Адрес шлюза | 100.124.155.106:18787 (как в проду) |
| Модель конфига | qwen3.8-27b (как в проду; есть в /v1/models — modelwatch без WARN) |
| qwen | 0.25.0 (active.json), wrapper с --openai-logging |
| A | V41DTRYDF6 (hold-a, занял слот fakellm firstByteMs=120000) |
| B | 7BSHWG85VD (hold-b, PANE, pin=fakellm) |

## П.3 Журнал qwen: первый удержанный запрос

**A и B шарят run_dir → общий `logs/openai/`.** Два лога:

1. `openai-...21-50-40...4abd38da.json` — **запрос A** (main turn): model=runpilot, max_tokens=32000, stream=true, tools=20, messages=2 (system+user). Завершён 21:50:40 (ответ fakellm "c0c1c2c3c4").
2. `openai-...21-52-40...subagent-managed-auto-memory-extractor...json` — **subagent** (служебный): "managed memory extraction subagent", max_tokens=32000, stream=true, tools=да. Завершён 21:52:40.

**Первый удержанный запрос B = основной запрос хода** (main turn, 20 tools) — НЕ служебный. Subagent (auto-memory-extractor) запускается **после** хода, не до.

**На 30-й секунде qwen B ничего не делал** — ход продолжался (capture-pane t+35s: "Working on it! (1m 9s)"). Нет обрыва, нет ретрая.

## П.4 Обрыв НЕ воспроизведён

30s client_gone не воспроизведён на стенде даже с настройками оператора + /v1/models vLLM. Таймер искать нечего.

## П.5 Флаппинг vzu5-big

- **Серверное состояние (UP/DOWN) не пишется в БД** (event-таблица: только LEASE_*, SESSION_STATE, QUEUE_*, NODE_STATE; SERVER_* событий нет).
- **Прод-логи serve на 20:23 утрачены** (координатор рестартнулся в 21:03).
- Флаппинг наблюдался на веб-UI, точные метки времени не реконструируются из доступных данных. Совпадение с 20:23:29 подтвердить нельзя.

## Вывод

1. **Гипотеза оператора НЕ подтверждена:**
   - settings.json оператора + /v1/models vLLM на стенде → 30s client_gone НЕ воспроизведён (B удержан 2м54с).
   - Первый удержанный запрос — основной ход (не служебный).
   - qwen **не запрашивает /v1/models** (нет вызовов models.list/get в бандле) — содержимое /v1/models на поведение qwen не влияет.
2. **Единственное оставшееся отличие стенд/прод — флаппинг vzu5-big (состояние сервера UP/DOWN).**
3. 30s client_gone, вероятно, связан со состоянием vzu5 (а не с settings.json / /v1/models).

## Следующий шаг

Нужен vzu5 для воспроизведения (по п.6: «Обрыв не воспроизвёлся → отчёт по п.1–5 и стоп. vzu5 запросить у оператора»).

## Изменения в коде

- `cmd/runpilot-fakellm/main.go`: новый флажок `-models-file F` (тело /v1/models из файла).
- `internal/testutil/fakellm/fakellm.go`: `SetModelsBody(b []byte)` — /v1/models отдаёт b как есть.
- `stand/config.yaml`: модель qwen3.8-27b (как в проду).
- `stand/launcher.sh`: копия settings.json оператора; fakellm с -models-file; ready-чек на 100.124.155.106:18788.
