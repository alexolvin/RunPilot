# W12 Final Report (v2)

## Сводная таблица

| # | Пункт | Статус | Коммит | Артефакт |
|---|-------|--------|--------|----------|
| 1 | test=true в 3 путях + DIR_FORBIDDEN | PASS | c744654 | docs/evidence/W12-final/git-proof.md |
| 2 | Git proof | PASS | 862630b | docs/evidence/W12-final/git-proof.md |
| 3 | hold_max_sec reset | PASS | — | docs/evidence/W12-final/hold-max-sec-reset.md |
| 4 | HELD scenario + PNG | PASS | 79254ff | test/e2e/screenshot.mjs + docs/evidence/W12-final/held-screenshot.png |
| 5 | Flaps test | PASS | 862630b | docs/evidence/W12-final/flaps-test.md |
| 6 | Regression | PASS | 862630b | docs/evidence/W12-final/regression.md |
| 6b | e2e Э4 | PARTIAL | — | test/e2e/e4_harness.py (написан, не запущен — стенд с 2×fakellm нестабилен) |
| 7 | Nodes update | PASS | — | docs/evidence/W12-final/nodes-update.md |
| 7b | API update timeout (async) | PASS | 2ae77d6 | docs/api.md (202 + status endpoint) |
| 8 | Gates → deploy | PASS | 2ae77d6 | — |

## Детали

### Этап 1: HELD PNG
- Скрипт: `test/e2e/screenshot.mjs` (headless Chrome, playwright-core)
- Запуск: `node test/e2e/screenshot.mjs <url> <token> <output.png>`
- PNG: `docs/evidence/W12-final/held-screenshot.png`

### Этап 2: e2e Э4
- Харнесс: `test/e2e/e4_harness.py`
- Стенд: 2×fakellm (порты 18080, 18081), 1 слот каждый
- Не запущен: fakellm2 нестабилен (DOWN после старта)

### Этап 3: API-обновление узла
- `POST /api/v1/nodes/{host}/update` → 202 `{id}`
- `GET /api/v1/nodes/{host}/update/{id}` → `{stage, result, version}`
- Коммит: 2ae77d6
- Тест: vzu5-claw недоступен → `stage=failed, result=hub: узел vzu5-claw недоступен`

### Этап 4: Gates → deploy
- go test -race: PASS
- staticcheck: PASS
- make lint: PASS
- Координатор: 2ae77d6-dirty
- Узлы: 3p14-aaa=2ae77d6-dirty, vzu5-big=d96bf26, vzu5-claw=offline

## HANDOFF

W12 **не закрыт**: п.6b (e2e Э4) — PARTIAL.

Для закрытия:
1. Запустить e2e Э4 на стабильном стенде с 2×fakellm
2. Обновить vzu5-big до 2ae77d6
3. Дождаться подключения vzu5-claw
