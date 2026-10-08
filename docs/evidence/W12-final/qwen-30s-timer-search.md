# Этап 3: Поиск 30s таймера в бандле qwen 0.25.0

**Дата:** 2026-10-08
**Бандле:** `/home/user/.qwen/updates/npm/f8b0b7826ba50adc/versions/0.25.0/node_modules/@qwen-code/qwen-code/chunks/`
**Версия:** 0.25.0 (active.json); CLI `qwen --version` возвращает 0.24.7 (устаревший)

## Результат: 30s таймер API НЕ найден

### Поиск константы 30000 / 3e4 / 30_000

Все вхождения 30-секундных констант в бандле — **не связаны с API-запросами**:

| Файл | Константа | Контекст |
|---|---|---|
| chunk-L2767F3V.js | `DEFAULT_REWRITE_REQUEST_TIMEOUT_MS=3e4` | Rewrite request (не API) |
| chunk-57F5O343.js | `AbortSignal.timeout(3e4)` | Shell publisher (не API) |
| chunk-EH2B57YD.js | `DEFAULT_SAMPLE_INTERVAL_MS=3e4` | Sampling interval (не API) |
| chunk-KWZ47WQ4.js | `DEFAULT_CONFIG_OPERATION_TIMEOUT_MS=3e4` | Config operation (не API) |
| chunk-T543B2B7.js | `DEFAULT_TELEMETRY_EXPORT_TIMEOUT_MS=3e4` | Telemetry (не API) |
| chunk-GJ5FVZ6B.js | `SOCKS_PROXY_TIMEOUT_MS=3e4` | SOCKS proxy (не API) |
| chunk-5ZT6H64U.js | `READY_TIMEOUT_MS=3e4` | Managed runtime session worker (не API) |
| chunk-B55KZ4FZ.js | `DEFAULT_WEBHOOK_TIMEOUT_MS=3e4` | Webhook (не API) |
| chunk-CJ765W6E.js | `gitOperationTimeoutMs=3e4` | Git operation (не API) |
| chunk-7KQ5X667.js | `3e4` | Workspace reload (не API) |
| chunk-55553249.js | `3e4` | Metrics (не API) |
| chunk-G233W4Z3.js | `3e4` | Metrics (не API) |

**Ни одна 30-секундная константа не управляет API-запросом к LLM.**

### Реальные таймауты API

| Таймаут | Значение | Файл |
|---|---|---|
| `DEFAULT_TIMEOUT` (request) | **120 000 ms (120s)** | chunk-EG3H5M5K.js: `resolveRequestTimeout` |
| OpenAI SDK `DEFAULT_TIMEOUT` | **600 000 ms (600s)** | chunk-2UOMCYQG.js |
| undici `headersTimeout` / `bodyTimeout` | **0 (отключено)** | chunk-ZZZAYAJW.js |
| `DEFAULT_STREAM_IDLE_TIMEOUT_MS` | **240 000 ms (240s)** | chunk-EG3H5M5K.js |
| `DEFAULT_STREAM_MAX_LIFETIME_MS` | **900 000 ms (9min)** | chunk-EG3H5M5K.js |
| `QWEN_CODE_API_TIMEOUT_MS` (env) | **120 000 ms (default)** / 5 400 000 ms (стенд) | chunk-QJ3UTSP5.js:1987 |

### Механизм `StreamConnectTimeoutError`

Файл: `openaiResponsesContentGenerator-4Q5KG5NA.js`
```js
class StreamConnectTimeoutError extends Error {
  constructor(connectTimeoutMs) {
    super(`Responses API connect phase exceeded ${connectTimeoutMs}ms before any response headers arrived. Increase the model request timeout (or set it to 0 to disable this bound).`);
  }
}
// connectTimeoutMs = resolveRequestTimeout(config.timeout) = 120s (default)
```

Это для `openai-responses` auth type. qwen coder использует `openai` auth type (OpenAIContentGenerator, chunk-C2JD6ONQ.js), где `timeout=resolveRequestTimeout(this.contentGeneratorConfig.timeout)` = 120s.

### QWEN_CODE_API_TIMEOUT_MS

Файл: chunk-QJ3UTSP5.js:1987 (`applyTimeoutEnvOverride`)
- Default: 120 000 ms
- Приоритет: modelProviders > env
- На стенде: `runpilot run` выставляет 5 400 000 ms (90 мин) из `client_timeout_ms` координатора.
- **Это НЕ 30s.**

### Различия 0.25.0 vs 0.24.7 (3e4 константы)

Новые в 0.25.0: только `DEFAULT_TIMEOUT=3e4` (imap-flow SOCKS proxy) и `READY_TIMEOUT_MS=3e4` (managed-runtime-session-worker). Обе не связаны с API-запросами.

## Вывод

1. **30s таймер API в бандле qwen 0.25.0 не существует.** Все API-таймауты 120s+.
2. **Стендовый тест подтверждает:** qwen 0.25.0 держится 245с в очереди и 46+с с first-byte задержкой без client_gone.
3. **30s client_gone на реальном vzu5 не является таймером qwen.** Источник 30s требует runtime-отладки (qwen log с --openai-logging на реальном vzu5) или является спецификой реального сценария.
