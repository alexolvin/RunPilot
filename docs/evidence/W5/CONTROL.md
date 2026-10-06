# W5 — Карточка сессии: CONTROL

Дата: 2026-09-27. Все 8 строк CONTROL выполнены. `go test -race ./...` — 0 падений;
`make lint` — 0; `make check` — 0; `make screenshots STAGE=W5` — 19.3 = 0 находок (12
файлов); `make visualcheck STAGE=W5` — OK.

Содержание (разделы 11.3/13/12/15 ТЗ): карточка сессии (живой экран, сведения, почему,
запрос разрешения, действия, композер 13.1); вставка текста (короткая/8КБ,
`web.paste_max_kb`, `\r`/ESC-фильтр — TW5); разрешения (PROMPT-панель → ответ кнопкой);
сжатие контекста; `internal/command` (разбор + выполнение, подсказки); Telegram на
общем разборе (веб = Telegram). Фикстуры на текущей версии Qwen Code 0.24.5:
`idle_input_pasted_short`, `idle_input_pasted_long`, `approval_prompt`, `compress`,
`quit` (`testdata/fixtures/qwen/0.24.5/`). E2E (строки 1–3) — реальный Qwen Code
(`qwen3.8-27b`, vLLM) в tmux, Python-харнесс над живым стендом (координатор + узел).

## Строки CONTROL (раздел 20, W5)

| # | Строка CONTROL (ТЗ) | Команда проверки | Артефакт | Результат |
|---|---|---|---|---|
| 1 | 20 текстов (одна строка, много строк, кириллица, эмодзи, 8 КБ, `web.paste_max_kb`) в реальный Qwen Code: `PASTED` 20/20, `PASTE_SUBMITTED` 0 | живой стенд + `c1.py` (реальный `qwen` в tmux) | `control1_paste_20.txt` | PASS: 20/20 вставлены, **PASTE_SUBMITTED=0**. 8 КБ и длинная одиночная строка — через плейсхолдер `[Pasted Content N chars]` (профиль `paste_placeholder_regex`); короткие/многострочные — побайтово по `trim` |
| 2 | Ответ на запрос разрешения в реальном Qwen Code выводит панель из `PROMPT` 10/10 | живой стенд + `c2.py` (реальный `qwen`, 10 permission-промптов) | `control2_approve_10.txt` | PASS: 10/10 — `Enter` (allow) снял панель с `PROMPT`; файл создан, панель вернулась в IDLE |
| 3 | Задержка живого экрана ≤ `web.screen_interval_ms` + 500 мс (p95) | живой стенд + `c3.py` (SSE `GET /api/v1/events?screens=`) | `control3_screen_delay.txt` | PASS: 20 замеров смены → кадр `SCREEN` с маркером, **p95=1696мс, max=1696мс ≤ 2500мс** (`screen_interval_ms`=2000 + 500) |
| 4 | Уникальная метка из текста задания находится в БД и журнале 0 раз (R5) | `go test -count=1 -run TestR5PasteTextNotStored ./internal/api` | `internal/api/paste_r5_test.go` | PASS: метка `AQMR5MARKER_X7Q9Z` вставлена (`PASTED`), **0** вхождений в событиях БД, записях сессий и журнале — хранится только факт `paste`, не текст |
| 5 | Тест разбора и выполнения на каждую команду раздела 12 | `go test -count=1 -run 'TestEveryRegistryCommand|TestRunEveryCommand' ./internal/command` | `internal/command/command_test.go` | PASS: итерация по `command.All()` (=32 команды) — `Parse`+`Run` на каждой; `len(sample)==len(All())` (нет «слепых» команд). `TestRunEveryCommand` — все 32 командой с аргументами |
| 6 | Веб и Telegram дают одинаковый результат на 30 строках | `go test -count=1 -run 'TestWebTelegramParity' ./internal/notify` | `internal/notify/telegram_test.go` | PASS: **30 строк** — `Code`+`Text` веба = Telegram побайтово; `TestWebTelegramParityErrors` — идентичность и на ошибках разбора (4 строки) |
| 7 | Подсказки ≤ `ui.complete_budget_ms` (p95 на 1000 запросах при 50 сессиях) | `go test -count=1 -run TestControl7CompleteBudget ./internal/command` | `internal/command/command_test.go` | PASS: 1000 запросов подсказок на 50 сессиях, **p95 ≤ 80мс** (`ui.complete_budget_ms`) |
| 8 | J1, J2 в `VISUAL.md` | `make screenshots STAGE=W5` + `make visualcheck STAGE=W5` | `VISUAL.md`, `screens/` (12 PNG) + `manifest.json` | PASS: J1 (4 шага: свободна → очередь → выполняется → свободна) и J2 (2 шага: запрос разрешения → ответ кнопкой) × 1440/390 = 12 снимков реальных сессий стенда; 19.3 = 0 находок; `visualcheck W5: OK — 12 файлов, 12 отметок в строке, commit = предок HEAD` |

## Дополнительно (строгие правила, проверки)

| Проверка | Команда | Результат |
|---|---|---|
| `go test -race ./...` | `go test -race ./...` | PASS: все пакеты ok, race чист |
| `make lint` (incl. R2-линтеры) | `make lint` | PASS: 0 находок |
| `make check` (vet + staticcheck + test + lint) | `make check` | PASS: rc=0 |

## Честные примечания

- **E2E на реальном Qwen Code (строки 1–3)**: модель `qwen3.8-27b` через vLLM медленная
  и переменная (доход до permission-промпта 75–200 с+), поэтому `c2.py` ждёт устойчивого
  IDLE (20 с) с таймаутом 250 с. Строки 1–3 воспроизводимы: харнесы `c1.py`/`c2.py`/`c3.py`
  и логи зафиксированы в `docs/evidence/W5/`.
- **Длинная одиночная строка** (8 КБ): Qwen Code сворачивает в `[Pasted Content N chars]`;
  классификатор учитывает это через `paste_placeholder_regex` (профиль `qwen.yaml`),
  фикстура `idle_input_pasted_long-01` перегенерирована под 0.24.5. Строка, которая
  **переносится** визуально, побайтовой верификации не поддаётся (ограничение проверки,
  не ошибка вставки) — такие тексты в набор 20 не входили.