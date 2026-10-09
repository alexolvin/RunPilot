# W12 Stage 3: hold_max_sec reset

## Текущее состояние

`gateway.hold_max_sec` = 3600 (дефолт из `config.Defaults()`).

## Проверка

- `GET /api/v1/settings` → `gateway.hold_max_sec: 3600`
- `runpilot doctor` → `PASS effective_config ... gateway.hold_max_sec=3600`
- `GET /api/v1/settings/revisions` → ревизий для `gateway.hold_max_sec` нет (никогда не менялась)

## Вывод

Оверрайда `gateway.hold_max_sec` не существует — значение уже равно дефолту (3600). Сброс не требуется.

## Оставшиеся оверрайды (отклонения от дефолта)

| Ключ | Дефолт | Текущее | Ревизия |
|------|--------|---------|---------|
| `node.project_roots_default` | `["~"]` | `["~", "/mdisk/myhub"]` | id=3 (2026-10-02) |
| `profiles.qwen.model_alias` | `qwen3.6-27b` | `qwen3.8-27b-fp8` | id=2 (2026-10-01) |

Все остальные ключи совпадают с дефолтами.
