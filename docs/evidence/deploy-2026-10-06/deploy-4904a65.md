# Повторный выкат 3p14-aaa из коммита (п.4)

- **Коммит:** `4904a657bd9374ec39859170e8088d290577a8a2` (main)
- **Дата:** 2026-10-06T18:02Z
- **Старый бинарник:** vcs.revision=`85e29c6…`, **vcs.modified=true** (грязное дерево)
  → бэкап `/home/user/.local/bin/runpilot.bak-85e29c6-20261006T180203Z`
- **Новый бинарник:** собран из чистого дерева коммита:
  - `vcs.revision=4904a657bd9374ec39859170e8088d290577a8a2` (= HEAD)
  - `vcs.modified=false`
  - → `goversion-m-4904a65.txt`

## Ворота (до рестарта)
- `GET /api/v1/state`: активных аренд = **0** (сессия `6ATNEWJPH2` IDLE,
  RUNNING/DETACHED/DISPATCHING нет; слоты не заняты runpilot-арендами).
- Операторские состояния: mode=NORMAL, серверы 3p14=UP, vzu5=UP,
  scheduler_paused/drain — не заданы.
- → `state_before_restart-4904a65.json`

## Рестарт
- `systemctl --user restart runpilot.service` → **active**

## После рестарта
- `runpilot doctor` → **PASS** (все проверки) → `doctor_post_restart-4904a65.txt`
- `GET /api/v1/state`: mode=NORMAL, серверы 3p14=UP, vzu5=UP, сессия IDLE,
  активных аренд = 0.
- **Операторские состояния ДО == ПОСЛЕ (MATCH=True).**
- → `state_after_restart-4904a65.json`

## Итог
Выкат 3p14-aaa из коммита `4904a65` выполнен: бинарник из чистого коммита
(vcs.modified=false), ворота (0 аренд) соблюдены, doctor PASS, операторские
состояния совпадают до/после рестарта.
