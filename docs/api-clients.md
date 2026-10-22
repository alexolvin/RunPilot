# API-клиенты шлюза (безсессионные OpenAI-запросы в общую очередь)

Безсессионные OpenAI-запросы `POST /v1/chat/completions` с Bearer-ключом.
Каждый запрос — один ход псевдо-сессии `api:<name>` (Kind=API) в общей
очереди координатора. Очередь, аренда, лимиты и учёт — как у кодерных
сессий, класс по умолчанию BATCH (ниже кодерных ходов, с aging до кодерного).

Спека: [spec-llm-need-runpilot.md](spec-llm-need-runpilot.md).

## Контракт запроса

- Метод: `POST /v1/chat/completions`
- Авторизация: `Authorization: Bearer <ключ>` (ключ `rp_` + 64 hex).
- Тело проксируется к серверу без изменений (`transformBody`):
  `chat_template_kwargs`, `temperature`, `max_tokens`, `stop`, `top_k`
  проходят по байтам; меняется только `model` (на модель сервера) и
  ограничиваются `max_tokens`/`max_completion_tokens` до
  `MaxOutputTokens` сервера.
- Ответ: `choices[0].message.content`, `finish_reason`, `usage`, `model` —
  без изменений (как от vLLM).

### Ошибки

| Ситуация            | Статус | Тело                                          |
|---------------------|--------|-----------------------------------------------|
| Неверный/отключённый ключ | 401 | `{"error":{"message":"invalid api key","code":"invalid_api_key"}}` |
| `stream: true`      | 400    | `code: stream_not_supported`                  |
| Нет слота за `max_hold_sec` | 503 | `code: runpilot_no_slot` + заголовок `Retry-After` |
| Сервер упал в запросе | 502   | `code: runpilot_server_down` + `Retry-After`, аренда снимается |

## Параметры клиента

| Параметр        | По умолчанию | Описание |
|-----------------|--------------|----------|
| `class`         | `BATCH` (4)  | Класс очереди. BATCH ниже кодерных ходов; после `aging_sec` получает приоритет кодерного хода на один выпуск. |
| `priority`      | `0`          | Приоритет выбора сервера (больше — раньше). |
| `enabled`       | `true`       | Отключённый клиент → 401. |
| `allowed_servers` | пусто (все) | Белый список серверов. Пусто = все серверы с подходящей моделью. |
| `max_hold_sec`  | `100`        | Макс. ожидание слота; потом 503 + `Retry-After`. |
| `sticky_idle_sec` | `20`        | Липкая аренда: сервер удерживается за клиентом (без вытеснения кодера). Если в очереди ждёт кодер — липкая аренда не ставится. |
| `aging_sec`     | `600`        | Через сколько ожидания BATCH получает приоритет кодерного хода. |

## Управление

### Веб (экран «Очередь», блок «API-клиенты»)
Список клиентов: учёт за сутки (запросы, токены), кнопки
«Создать» / «Отключить» / «Включить» / «Перевыпустить ключ».
Ключ показывается один раз (при создании и перевыпуске) и нигде не
хранится — в БД только `key_hash` (SHA-256).

### CLI
```
runpilot api-client [--json]            # список + учёт за сутки
runpilot api-client create <name>       # создать (ключ один раз)
runpilot api-client enable <name>       # включить
runpilot api-client disable <name>      # отключить
runpilot api-client reissue <name>      # перевыпустить ключ (один раз)
```

### HTTP API
```
GET    /api/v1/api-clients                 # список + учёт за сутки
POST   /api/v1/api-clients                 # {name, ...} → {name, key} (ключ один раз)
POST   /api/v1/api-clients/{name}/enable   # 204
POST   /api/v1/api-clients/{name}/disable  # 204
POST   /api/v1/api-clients/{name}/reissue  # {name, key} (ключ один раз)
```

## Учёт (api_request)

Каждый запрос пишется в `api_request`:
`ts, client, server, model, prompt_tokens, completion_tokens, wait_ms,
latency_ms, result` (OK/401/400/502/503). Токены — из `usage` ответа.
Сводка «за сутки» (запросы + токены) — в вебе и CLI.

## Псевдо-сессия

- `sid = api:<name>`, `Kind = API`, состояние IDLE/RUNNING.
- Видна в очереди и в списке сессий.
- Закрыть её нельзя: операторские действия сессии (cancel и т.п.) не
  применяются к Kind=API.
