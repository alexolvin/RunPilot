# Этап 0 — live-прогон (резюме) — ГЕЙТ: п.2 PASS

Stand: 3p14-aaa, изолирован (127.0.0.1, порты 18787/18788/18790/18801, свой
HOME/БД/tmux -L hold-silent, только fakellm). qwen 0.24.7. Реальная метрика (БД),
не код-референс. Детали по пунктам: 02-p2, 03-p3, 04-p4, 05-p5.

## Итог по пунктам
| п. | Имя | Результат | Суть |
|---|---|---|---|
| п.1 | QWEN_CODE_API_TIMEOUT_MS (0.24.7) | **PASS** (документ, 01-qwen-env-var.md) | env-переменная работает; settings.json без modelProviders |
| п.2 | Силент-холд: окно удержания, 1 запрос, grant в тик | **PASS** (02-p2) | held_ms=379517; 1 запрос в окне; 0 других; grant ~1.1 мс после release A; send ~2.6 мс после grant (≤ tick_ms) |
| п.3 | Контроль: без env (дефолт 120000) | **PASS** (03-p3) | клиент B падает на ~120 с (запрос не завершён); зеркало п.2 (там завершался на ~380 с) |
| п.4 | Esc во время удержания | **FINDING** (04-p4) | запись НЕ удаляется (heldToResume: held_request=0, mode=resume); события QUEUE_DEQUEUE нет (и payload Dequeue пустой, без reason:"client_gone"); IDLE — да |
| п.5 | hold_max_sec=20 → 503 | **FINDING** (05-p5) | 503 + Retry-After(30) + runpilot_no_slot — OK (20.002 с); enqueued_at при повторе сбрасывается (QueueUpsert), исходный не сохраняется |

## ГЕЙТ
**п.2 PASS** → по заданию: Этап 2 (п.10: тест на виртуальных часах) → затем
«реализация по списку».

## Список для реализации (из находок Этапа 0)
1. **п.4** — при отмене клиента (Esc) во время удержания: `QueueDelete` + событие
   `QUEUE_DEQUEUE {reason:"client_gone"}` + сессия IDLE (сейчас — heldToResume, без
   удаления и без события).
2. **п.5** — при повторе (CONFLICT в QueueUpsert) не сбрасывать `enqueued_at`
   (сохранить исходный; отдельное поле `first_enqueued_at` или MIN при CONFLICT).
3. **п.10** — holdPass ждёт LEASE_GRANT **своего** sid (не «чужой release»); виртуальный
   тест 3 путей освобождения слота (COOLDOWN→FREE, EXTERNAL→FREE, DOWN→UP) + удержанный
   запрос проксируется в том же тике, где выдана аренда.

## Замечания по стенду (не баг продукта)
- `runpilot run` целит в `$TMUX`: сессии A/B создавались только из console-панели на
  hold-silent (из обычной шеллы падали на default-сокет — изоляция).
- `fakellm /_set-delays|_set-metrics|_set-fail` — **query-параметры**, не JSON-тело.
- Координатор при старте переносит конфиг из YAML в БД (`setting`/`server`), YAML
  становится стабом — настройки живут в БД (hold_max_sec=20 проверен в БД).
