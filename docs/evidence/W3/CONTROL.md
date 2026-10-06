# W3 — Дизайн и стенд: CONTROL

Дата: 2026-09-26. Все 4 строки CONTROL выполнены. `make check` — 0; `go test -race ./...` —
0 падений; `make lint` — 0; `make screenshots STAGE=W3` — 19.3 = 0 находок;
`make visualcheck STAGE=W3` — OK.

Содержание (разделы 9–10, 18–19 ТЗ): дизайн-токены (9.2), компоненты каркаса (9.3),
иконки Lucide (web/icons → icon.js), шрифты Inter + IBM Plex Mono (woff2, кириллица),
каркас (боковая/верхняя/нижние вкладки, 10.1–10.3), оболочка командной строки (12),
вход (16), переключатель темы, пустые состояния; фронтенд — Preact+htm+hooks без сборки
(go:embed, ESM, importmap). Стенд `aqm-stand` (виртуальные часы + временная БД + веб,
каркас). Конвейер 19.2–19.4: go-rod headless Chrome, 10 комбинаций 19.2, автопроверки 19.3,
golden-база (test/visual/golden), `tools/visualcheck`.

## Строки CONTROL (раздел 20, W3)

| # | Строка CONTROL (ТЗ) | Команда проверки | Артефакт | Результат |
|---|---|---|---|---|
| 1 | Скриншоты всех комбинаций 19.2 для входа и каркаса | `make screenshots STAGE=W3` | `docs/evidence/W3/screens/` (10 PNG) + `manifest.json` | PASS: вход + каркас × 3 окна × темы — `empty__{login,queue}__{1440x900,1024x768,390x844}__{light,dark}` (10 файлов; телефон — scale 3/touch). `manifest.json` фиксирует коммит 82fc177 и проверки каждого файла |
| 2 | Проверки 19.3 — 0 находок | `make screenshots STAGE=W3` | `control-screenshots.log`, `manifest.json` (checks) | PASS: `OK: 19.3 — 0 находок`. По каждому файлу (manifest.checks): console=ok, network=ok, scroll=ok (scrollWidth≤clientWidth), text=ok, axe=ok (0 serious/critical), touch=ok (телефон) / n/a, fonts=ok (Inter+IBM Plex Mono), golden=ok (0.00–0.13% ≤ diff_max) |
| 3 | `VISUAL.md` + `visualcheck` OK | `make visualcheck STAGE=W3` | `VISUAL.md`, `visualcheck.log` | PASS: `visualcheck W3: OK — 10 файлов, 12 отметок в строке, commit 82fc177 = предок HEAD`. Агент просмотрел каждый PNG (read_file), V1–V12 по разделу 19.4 |
| 4 | 0 запросов к внешним хостам | `make screenshots STAGE=W3` (19.3.2) | `control-screenshots.log`, `manifest.json` (network) | PASS: 19.3.2 — 0 запросов к внешним хостам + 0 ответов ≥400 (все ресурсы — /vendor, /fonts, /styles, /app; CSP `default-src 'self'`) |

## Дополнительно (строгие правила, проверки)

| Проверка | Команда | Артефакт | Результат |
|---|---|---|---|
| `make check` (vet, staticcheck, test -race, lint) | `make check` | `make-check.log` | PASS: rc=0 |
| `go test -race ./...` | `go test -race ./...` | `test-race.log` | PASS: все пакеты, race чист |
| R2 (нет литералов/цветов вне токенов/реальных хостов) | `make lint` | `make-lint.log` | PASS: `lintlits`/`lintweb`/`linthosts` 0 |
| CSP без внешних ресурсов | `make screenshots` (19.3.2) | `internal/web/web.go` (csp) | PASS: `default-src 'self'; ... script-src-elem 'self' '<sha256>'` (hash инлайн-importmap) |
| Стенд (каркас) | `make screenshots` (поднимает aqm-stand) | `cmd/aqm-stand/` | PASS: виртуальные часы (2026-01-01), временная БД, веб с go:embed; login→/ через токен |

## Ключевые технические решения (зафиксированы)

- **htm = шаблонные литералы, не JSX**: компонент в шаблоне — только `<${Comp}/>`,
  статичный `<Comp/>` рендерится как литеральный тег. `web/app/main.js`,
  `web/app/views/login.js`.
- **Порядок CSS**: `tokens.css → components.css (базовые правила) → layout.css (структура +
  медиазапросы)`. Базовые правила, загруженные после медиазапросов, перебивают их
  (`.sidebar{display:flex}` ломал телефон). Базовый `display:none` нижних вкладок — в
  `components.css`, `display:flex` — в телефонном медиазапросе `layout.css`.
- **Контраст тёмной темы (WCAG AA, 19.3.5)**: белый на акценте `#3B82F6` = 3.68 < 4.5 →
  в тёмной теме `--color-on-accent:#0F172A` (4.85). Акцент как текст на тёмной поверхности
  `#1E293B` = 3.96 < 4.5 → `--color-accent-text:#60A5FA` (5.72) для ссылок/активных пунктов.
  Заполнения (кнопки/brand) остаются `--color-accent`.
- **Шрифты (19.3.7)**: `document.fonts.check` без `load()` возвращает false для
  лениво-неиспользуемого семейства (mono на странице входа) → конвейер явно запрашивает
  оба семейства (`fonts.load('16px Inter','AaБб')` + mono), подтверждая shipped-woff2.
- **Кнопка (htm-спред)**: `<button ${attrs}>` с `onClick`/`type` ненадёжно применяет
  `class` → класс вынесен инлайн в шаблон `class=${cls}` (button.js).

## Отклонения и решения (зафиксированы)

1. **commit в manifest/VISUAL.md = базовый коммит (82fc177), не коммит W3.**
   Скриншоты снимаются с рабочего дерева ДО коммита W3; `git rev-parse HEAD` на момент
   съёмки = 82fc177 (W2, родитель W3). `manifest.json` и `VISUAL.md` фиксируют один коммит
   (пара «снимки+разбор»); `visualcheck` сверяет их совпадение и что коммит — предок HEAD.
   Точное «хеш = HEAD коммита-снимка» при одном коммите на этап недостижимо (хеш зависит
   от содержимого VISUAL.md), поэтому проверка — предок HEAD + совпадение manifest/VISUAL.md.
2. **Операторское решение A9 отложено в самый конец** (Э7/Э8 → впитать в W5/W7/W9);
   на W3 не влияет (W3 — каркас/дизайн/стенд, без сценариев отказов).
3. **`tools/dbgshot`** (пошаговая диагностика вёрстки) — временный инструмент сессии,
   удалён перед коммитом; постоянные инструменты — `tools/screenshots` + `tools/visualcheck`.
