# Приёмка kill -9 (W11 доп, пункт 4) — 2026-10-07

Бинарник координатора: `aaec90f` (vcs.modified=false). vLLM 3p14: `127.0.0.1:8004`.
Скрипт: `/mdisk/myhub/test/rp-accept/evidence/kill9-accept-v2.py` (10 повторов, pin 3p14,
длинный промпт ~1200 слов, kill -9 exec в момент генерации).

## Замер шага 1

- **Дерево группы кодера ДО kill:** 1 процесс (`MainThread` — qwen/node), pgrp = pgid кодера.
- **Дерево ПОСЛЕ kill:** 0 процессов (пусто) — группа кодера убита (killCoderGroup).
- **`ss -tnp 'dport = :8787'` после kill:** 0 conn — TCP-соединение кодера с шлюзом закрыто.

## Таблица 10 повторов

| # | kill→abort vLLM +1 (с) | kill→LEASE_RELEASE (с) | t_end < LEASE_GRANT next |
|---|---|---|---|
| 1 | — (abort не incremented) | 1.134 | — |
| 2 | — | 0.620 | yes |
| 3 | — | 1.773 | yes |
| 4 | — | 1.726 | yes |
| 5 | — | 0.086 | yes |
| 6 | — | 0.693 | yes |
| 7 | — | 0.455 | yes |
| 8 | — | 0.839 | yes |
| 9 | — | 1.560 | yes |
| 10 | — | 1.951 | yes |

Максимумы: kill→abort = **—** (счётчик не incremented); kill→LEASE_RELEASE = **1.951 с**.
Критерий 4 (t_end < LEASE_GRANT next): **PASS**.

## FINDING: abort-счётчик vLLM не incremented (BUG)

`vllm:request_success_total{finished_reason="abort"}` = **0** до и после всех 11 kill
(10 приёмка + 1 ручной). За это время `finished_reason="stop"` вырос с 590 до 613 (+23) —
запросы убитых сессий **завершаются генерацией до конца**, а не отменяются.

Факты:
- Группа кодера после kill мёртва (0 proc), `ss 'dport = :8787'` — 0 conn (TCP закрыт).
- Сессии → GONE, lease → RELEASED (CRASH) за 0.09–1.95 с.
- Но vLLM продолжает генерацию запроса убитой сессии до completion (stop), abort = 0.

Вывод: **шлюз НЕ отменяет upstream-запрос vLLM при обрыве клиента (кодера)**. Kill-группа
убивает кодера и закрывает TCP, но ReverseProxy шлюза не закрывает upstream-соединение
при cancel контекста — vLLM догенерировывает ответ. Первоначальный дефект
«kill -9 → vLLM keeps generating ~17-32s» **не закрыт полностью**: слот освобождается,
но GPU продолжает генерацию мёртвой сессии.

Локализация: `internal/gateway/proxy.go` — `httputil.ReverseProxy` со std `http.Transport`;
при cancel `pctx` (CancelSID / client disconnect) upstream-соединение не закрывается
(keep-alive пул), vLLM не видит обрыва.

Статус: **требует отдельного задания** (фикс отмены upstream в шлюзе).
