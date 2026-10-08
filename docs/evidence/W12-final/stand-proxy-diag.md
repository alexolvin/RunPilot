# Диагностика прокси-гипотезы: стенд с адресом прода

**Дата:** 2026-10-08
**Стенд:** gateway на 100.124.155.106:18787 (bind и gateway_url)
**B:** sid `RHHQBNMFY3` (hold-b, PANE, hold-silent, pin=fakellm)
**A:** sid `QWMA0B9GYW` (hold-a, PANE, hold-silent, pin=fakellm)

## Результат: 30s client_gone НЕ воспроизведён

B удерживался **2м13с+** без client_gone (на стенде с адресом прода).

## Данные (t+10s, t+25s, t+35s)

### ss -tnpe (dport :18787)

```
ESTAB 100.124.155.106:44168 → 100.124.155.106:18787  MainThread pid=2849082 (qwen B)
ESTAB 100.124.155.106:46704 → 100.124.155.106:18787  MainThread pid=2847951 (qwen A)
```

- **Соединение B: напрямую qwen→gateway.** Нет промежуточного процесса (прокси).
- Клиентский конец держит `MainThread` (pid=2849082) — процесс qwen B.

### Proxy в environ qwen B

```
(нет proxy в environ)
```

### Proxy в settings.json qwen

- User (`/home/user/.qwen/settings.json`): **нет proxy**.
- Project (`.../stand/work/.qwen/settings.json`): **нет файла**.

### События B

| ts | kind |
|---|---|
| 20:48:10.988 | SESSION_STATE: "" → IDLE |
| 20:48:30.710 | QUEUE_SKIP (pin_mismatch) |
| (нет client_gone на 35с, 60с, 2м13с) | — |

### Запросы B

- Нет запросов (удержание в очереди, запрос не дошёл до сервера).

## Вывод

1. **Гипотеза прокси НЕ подтверждена:**
   - Нет прокси в цепочке соединения (ss: qwen→gateway напрямую).
   - Нет proxy в environ, settings.json.
   - Нет client_gone на стенде (2м13с+).
2. **30s client_gone НЕ воспроизведён на стенде** (даже с адресом прода 100.124.155.106:18787).
3. **Разница стенд/прод НЕ в адресе шлюза.**

## Что отличается стенд от прода

| Параметр | Стенд | Прод |
|---|---|---|
| Gateway адрес | 100.124.155.106:18787 | 100.124.155.106:8787 |
| Upstream | fakellm (127.0.0.1:18801) | vzu5 (100.126.222.23:8004) |
| vzu5-big state | — | **флаппит DOWN/UP** |
| client_gone | **нет** (2м13с+) | **да** (29.3с) |

**Единственное отличие:** upstream (fakellm vs vzu5) и state vzu5-big (флаппинг).

## Следующий шаг

Нужен vzu5 для воспроизведения. Гипотеза: 30s client_gone связан со state vzu5 (флаппинг DOWN/UP) или с самим vzu5 (не с адресом шлюза и не с прокси).
