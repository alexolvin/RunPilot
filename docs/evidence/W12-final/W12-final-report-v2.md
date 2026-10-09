# W12 Final Report (v2, закрыт)

## Сводная таблица

| # | Пункт | Статус | Коммит | Артефакт |
|---|-------|--------|--------|----------|
| 1 | test=true в 3 путях + DIR_FORBIDDEN | PASS | c744654 | docs/evidence/W12-final/git-proof.md |
| 2 | Git proof | PASS | 862630b | docs/evidence/W12-final/git-proof.md |
| 3 | hold_max_sec reset | PASS | — | docs/evidence/W12-final/hold-max-sec-reset.md |
| 4 | HELD scenario + PNG | PASS | 79254ff | test/e2e/screenshot.mjs + docs/evidence/W12-final/held-screenshot.png |
| 5 | Flaps test | PASS | 862630b | docs/evidence/W12-final/flaps-test.md |
| 6 | Regression | PASS | 862630b | docs/evidence/W12-final/regression.md |
| 6b | e2e Э4 | PASS | 3be5ae4 | docs/evidence/W12-final/stage2-e4-result.json |
| 7 | Nodes update | PASS | — | docs/evidence/W12-final/nodes-update.md |
| 7b | API update timeout (async) | PASS | 2ae77d6 | docs/api.md (202 + status endpoint) |
| 8 | Gates → deploy | PASS | 2ae77d6 | — |

### Стансы 0–2 (переработка после нарушений)

| Станс | Содержание | Статус | Коммит | Артефакт |
|-------|-----------|--------|--------|----------|
| 0 | Восстановление координатора (чистый HEAD через deploy-coordinator.sh) + смена оператора-токена | PASS | 3be5ae4 | docs/evidence/W12-final/stage0-nodes-versions.md |
| 1 | Влияние стенда на tmux оператора (очистка осиротевших @runpilot_*) | PASS | 3be5ae4 | docs/evidence/W12-final/stage1-tmux-cleanup.md |
| 2 | e2e Э4: встроенный узел + сокет e2e, 18 ходов PASS | PASS | 3be5ae4 | docs/evidence/W12-final/stage2-e4-result.json, stage2-e4-conn-none.md |

## Детали

### Станс 0: координатор + токен
- Найдена и остановлена serve-процесс, запущенный вручную (PID из shell).
- `systemctl --user restart runpilot` → MainPID на 8787/8788.
- `vcs.modified=false` → собран чистый HEAD `77e9550` → `deploy/deploy-coordinator.sh` (гейт 0 аренд PASS) → узлы переподключились.
- Оператора-токен был выведен в командных строках → сменён. Новый токен в
  `/home/user/.config/runpilot/env` (0600) + env CLI. Старый → 401, новый → 200.
  В отчёте — только путь к файлу, не токен.

### Станс 1: tmux оператора
- Станс-узел сканировал дефолтный сокет оператора (нарушение п.13) → остановлен.
- 5 осиротевших `@runpilot_*` на панелях оператора (не в рабочей БД) удалены
  `tmux set-option -pu`. 6 живых SID (в БД, IDLE) сохранены.
- В БД стенда операторских панелей не оказалось.

### Станс 2: e2e Э4
- Стенд: встроенный узел (`embedded_node`), tmux-сокет `e2e` (не default),
  бинарники в каталоге стенда, 2×fakellm (по 1 слоту).
- Причина `conn=None` (из log стенда): отдельный узел сканировал дефолтный
  сокет + connection refused при остановленном координаторе. Устранено
  встроенным узлом (transport memConn, WS-канала нет).
- Критерии (make e2e-e4): 18 ходов OK, 0 фантомов, 54 запроса (все 200),
  аренд ≤1/сервер на каждом сэмпле (0 нарушений), dispatch p95 500мс ≤ 1500мс.

### Этап 1: HELD PNG
- Скрипт: `test/e2e/screenshot.mjs` (headless Chrome, playwright-core)
- PNG: `docs/evidence/W12-final/held-screenshot.png`

### Этап 3: API-обновление узла
- `POST /api/v1/nodes/{host}/update` → 202 `{id}`
- `GET /api/v1/nodes/{host}/update/{id}` → `{stage, result, version}`
- Коммит: 2ae77d6

### Этап 4: Gates → deploy
- go test -race / staticcheck / make lint: PASS

## HANDOFF

W12 **закрыт**: все пункты + стансы 0–2 PASS.

Состояние системы:
- Координатор: `77e9550` (vcs.modified=false), `runpilot.service` active.
- Оператора-токен: сменён. Файл: `/home/user/.config/runpilot/env` (0600).
- Узлы: 3p14-aaa=77e9550, vzu5-big=d96bf26, vzu5-claw=d96bf26 (flaky).

Постоянные правила 12–14 (нарушения, приведшие к переработке):
12. Рабочие сервисы — только через `systemctl --user` и deploy-скрипты.
13. Стенд/тесты не используют /home/user/.local/bin/runpilot, порты 8787/8788,
    tmux default, рабочую БД; бинарники — в каталоге стенда.
14. Токены не в командных строках/логах/отчётах — только чтение из env-файлов.
