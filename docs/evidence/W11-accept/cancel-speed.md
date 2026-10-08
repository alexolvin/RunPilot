# Скорость отмены в vLLM (W11 доп-5, п.2a) — 2026-10-08

Критерий: «Скорость отмены в vLLM: 3× прямой запрос (ignore_eos, max_tokens 4000),
kill -9 curl через 3 с. Опрос `vllm:num_requests_running` каждые 100 мс: от kill
до падения значения на 1 — ≤ 1 с. Повтор, где падение замаскировано чужим запросом,
не засчитывается и повторяется.»

**Скрипт:** `/mdisk/myhub/test/rp-accept/evidence/cancel-speed.sh`.
**Вывод:** `/mdisk/myhub/test/rp-accept/evidence/cancel-speed.out`.

**Метод:** прямой POST → :8004 (`stream=true, ignore_eos=true, max_tokens=4000`,
длинный промпт). До запроса — ждём idle (running=0.0 AND waiting=0.0). Ждём
running=1.0 (мой запрос в батче). Через 3 с — `kill -9` на curl. Дальше опрос
`vllm:num_requests_running` каждые 100 мс: фиксируем момент падения 1.0→0.0.
Т=kill→drop. Повтор засчитывается, только если running=1.0 до kill и не было
роста >1.0 после (рост = чужой запрос = маскировка, не засчитывается).

## Таблица (a) — kill→drop(running 1→0)

| iter | running до kill | kill→drop (с) | ≤1с |
|---|---|---|---|
| 1 | 1.0 | 0.137 | PASS |
| 2 | 1.0 | 0.136 | PASS |
| 3 | 1.0 | 0.137 | PASS |

**Итог: 3/3 PASS (≤1 с).** Среднее kill→drop ≈ 0.137 с.

## Вывод

vLLM v0.30.0 (`ced6857a`) при обрыве TCP-соединения клиента прекращает генерацию
быстро: счётчик `num_requests_running` падает 1→0 за **~0.137 с** от kill (≤ 1 с).
Критерий (a) **выполнен**.
