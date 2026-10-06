# W9 — План приёмки на железе

**Дата:** 2026-09-27 → 2026-09-29
**Машины:** `3p14-aaa` (координатор, сервер, клиент), `vzu5-big` (сервер, клиент)
**Длительность:** 2 часа, 6 сессий, оператор — только браузер ПК + телефон

## CONTROL (7 строк)

| # | Проверка | Критерий | Статус |
|---|----------|----------|--------|
| 1 | Инварианты Э8 (SQL по lease, 0 таймаутов, исходы ходов) | 0 нарушений | ⬜ после 2-часового теста |
| 2 | J1 с телефона 5 из 5 без SSH | 5/5 | ⬜ только оператор (телефон) |
| 3 | Остановка vLLM на vzu5-big во время хода | Ход завершён OK на 3p14-aaa | ⬜ только оператор (root на vzu5) |
| 4 | Перезагрузка 3p14-aaa | Веб отвечает 200 без ручных действий | ⬜ только оператор (sudo reboot) |
| 5 | Минимум одно самообновление узла | Без потери сессий | ✅ `control/self_update.json` |
| 6 | Эхо терминала ≤ 150 мс p95 | Через tailnet | ✅ `control/echo_terminal.json` (p95 59.13 мс, n=20) |
| 7 | aqm doctor | Все PASS на обеих машинах | ✅ `control/doctor_3p14.json`, `control/doctor_vzu5.json` |

## Состояние развёртывания (актуально, 2026-09-29)

### 3p14-aaa (координатор) — ГОТОВО

- Бинарник **B3** (`~/.local/bin/aqm`, резервные `.b1`/`.b2`) в user systemd `aqm`
  (KillMode=process, Restart=on-failure, EnvironmentFile=%h/.config/aqm/env)
- Конфиг `~/.config/aqm/config.yaml`: coordinator.bind=100.124.155.106 (tailnet),
  api_port=8788, gateway_port=8787, gateway_url=http://100.124.155.106:8787,
  web.bind=127.0.0.1:8790, web.public_url=https://3p14-aaa.tail46a65f.ts.net,
  dist_dir **не задан** → дистрибутив = собственный бинарник координатора
- `sudo tailscale set --operator=$USER; tailscale serve --https=0 --bg 8790`
  → веб: `https://3p14-aaa.tail46a65f.ts.net/` (443 → 127.0.0.1:8790)
  **Внимание:** BotBoss был на 443 — после W9 восстановить `tailscale serve 3000`
- vLLM `qwen3.8-27b-fp8` 127.0.0.1:8004, сервер `local` (prio 100, 2 слота) UP
- `aqm doctor` — все PASS (`control/doctor_3p14.json`)

### vzu5-big (узел) — ГОТОВО

- Бинарник **B3** (пришёл самообновлением, CONTROL 5; резервный `aqm.prev`=B2)
  в user systemd `aqm-node` (KillMode=process, Restart=on-failure, linger)
- Конфиг: client.coordinator=http://100.124.155.106:8788,
  **client.token_env=AQM_NODE_TOKEN** (enroll+claim выполнен),
  node.gpu=nvidia, gpu_server=vzu5; сервер `vzu5` (prio 50, 2 слота) на vLLM :8004
- vLLM на vzu5-big — root-процесс (остановка/запуск — только оператор, CONTROL 3)
- `aqm doctor` — все PASS (`control/doctor_vzu5.json`)

## Найденные дефекты и исправления (W9)

1. **gateway: max_tokens=0 в теле запроса** — vLLM отвергал запрос (400).
   `internal/gateway/body.go`: n≤0 → поле удаляется; кап max_* только при
   maxOutput>0. Тест `TestBodyTransformNonPositiveAndNoCap`.
2. **PTY-терминал мёртв на headless-узле** — сервисный aqm-node не имеет TERM,
   `tmux attach` мгновенно умирал («terminal does not support clear»).
   `internal/node/pty/pty.go`: `envWithTerm` (TERM=screen-256color).
   Тест `TestPtyOpenWithoutTermEnv`.
3. **TZ 14.2 exit-75 отсутствовал** — после self-update узел переподключался
   старым кодом, новый бинарник не запускался. `internal/node/ws.go`:
   `ErrUpdated` (Run без реконнекта) + `cmd/aqm/node.go`: `os.Exit(exitSelfUpdated)`.
   Тест `TestWSClientSelfUpdateExits`.
4. **Ответ UPDATED терялся (data race-подобное окно)** — `awaitReplyFlush` ждал
   лишь освобождения канала `out`; писатель мог забрать сообщение, но не
   дописать кадр — ранний `conn.Close()` обрывал ответ (репро: 200/200 потерь).
   Координатор не видел UPDATED. Исправление: барьер записи `flushReq`
   (единственный писатель обрабатывает его после завершения предшествующих
   записей) + сток канала. `internal/node/ws.go`.
5. **URL дистрибутива = gateway_url** — `/api/v1/dist/aqm` живёт на API
   (api_port), а шлюз (gateway_port) отдавал 404; self-update молча падал
   (API возвращал `update-requested` при любом ответе узла).
   `internal/api/nodes.go`: `requestBaseNoPath = http://<bind>:<api_port>`.
   Регрессия в `TestTE_N4`.
6. **gorilla/websocket «отравление» соединения** (подводный камень для
   пробоотладки): после любого read-error (включая истёкший
   SetReadDeadline) `readErr` липнет — дальнейшие ReadMessage мгновенно
   возвращают сохранённую ошибку; 1000 повторов → panic «repeated read on
   failed websocket connection». Инструменты не должны истекать read deadline
   и продолжать читать то же соединение.

## Эксплуатационные заметки (выучено на W9)

- **Деплой на vzu5:** бинарник передавать потоком
  `cat aqm | tailscale ssh vzu5-big 'cat > /tmp/aqm.new'` (scp через
  ProxyCommand не работает); **обязательно `chmod +x`** после установки —
  без execute-бита systemd уходит в 203/EXEC-цикл.
- **Остановка aqm-node не быстрая:** graceful stop тянется до
  TimeoutStopSec (90 c) → SIGKILL (считывающий цикл WS не знает про ctx,
  read deadline 2 мин > 90 с). На сессии не влияет (tmux живёт вне процесса,
  KillMode=process), но закладывать ~90 с в рестарты.
- **Сессия/комок:** cookie `aqm_session` — Secure только по https (через
  tailscale serve); прямой http к 127.0.0.1:8790 cookie не примет.
- **TLS tailscale serve:** сертификат отдаётся только при SNI=ts.net-hostname;
  при подключении по IP — InsecureSkipVerify + ServerName=hostname
  (паттерн `tools/w9echo`, удалён после W9).
- **Схема JSON API:** ключи в верблюдице/заглавные как в `docs/api.md`
  (SID/Name/Host/State…); «online» узла смотреть по `health.at`, поле
  `Conn` в GET /nodes всегда null.
- **Self-update (14.2):** enroll → token узла → claim(host) →
  `client.token_env` на узле; `/api/v1/dist/aqm` принимает только токен узла
  (операторский — 401). Без dist_dir координатор раздаёт собственный бинарник.
- **make check** включает lint-hosts: в коде (даже в комментариях) не должно
  быть реальных имён машин и CGNAT-адресов (100.64.0.0/10).

## Сценарий 2-часового теста (оператор)

### Фаза 1: Запуск (0:00–0:15)

1. Открыть `https://3p14-aaa.tail46a65f.ts.net/` в браузере ПК, ввести токен
   (AQM_TOKEN из `~/.config/aqm/env`)
2. Проверить: Очередь (серверы local+vzu5 UP, GPU), Сессии, Серверы, Узлы
3. Создать 3 сессии на 3p14-aaa и 3 на vzu5-big (мастер «Новая сессия»,
   профиль qwen); дать каждому первое задание (короткий промпт)

### Фаза 2: Нагрузка (0:15–1:30)

4. Поочерёдно — задания всем 6 сессиям (каждое = 1 ход); на каждом ходу:
   ход в «В работе», статус RUNNING → IDLE, результат в карточке
5. На телефоне: открыть веб, видеть сессии/ходы/статусы (J1)
6. ≈1:00: остановить vLLM на vzu5-big (root: `systemctl stop <vllm>` или kill)
   во время активного хода; проверить, что ход завершён OK (миграция на
   local/3p14-aaa); включить vLLM обратно
7. ≈1:30: открыть терминал в браузере, печатать, проверить эхо

### Фаза 3: Проверки (1:30–2:00)

8. **J1 с телефона (5/5):** 5 сессий — статус, последний ход, дать задание.
   Без SSH, только браузер.
9. **Перезагрузка 3p14-aaa:** записать время → `sudo reboot` → после
   загрузки открыть веб (ожидать 200 без ручных действий) → записать время
   восстановления → `control/reboot_time.txt`
10. **aqm doctor** на обеих машинах → все PASS (уже прогонялось до теста;
    повторить после — `control/doctor_*.json`)
11. **Восстановить BotBoss:** `tailscale serve 3000` (443 → 3000)

### Фаза 4: Инварианты Э8 (после теста)

Схема сверена с живой БД (2026-09-29): `lease(server, slot, granted_at,
released_at, state, …)`, `request(status INT, error TEXT, …)`,
`turn(ended_at, outcome TEXT)`; исходы ходов: `OK`/`ERROR`/`CANCELLED`/`LOST`
(коды HOLD — в `session.hold_reason`).

12. SQL к `~/.local/share/aqm/aqm.db` (3p14-aaa):
    ```sql
    -- (а) Пересечения интервалов двух аренд на одном (server, slot): 0 строк
    SELECT a.server, a.slot, a.id, b.id
    FROM lease a
    JOIN lease b ON a.server = b.server AND a.slot = b.slot AND a.id < b.id
    WHERE b.granted_at < a.released_at AND a.released_at != '';
    -- (б) Таймауты клиентов (отмена контекста медленного клиента): 0
    SELECT COUNT(*) FROM request WHERE error = 'CLIENT_GONE';
    -- (в) Завершённые ходы без исхода: 0
    SELECT COUNT(*) FROM turn WHERE ended_at != '' AND outcome = '';
    -- (г) Неизвестные исходы: пусто
    SELECT DISTINCT outcome FROM turn
    WHERE outcome NOT IN ('OK','ERROR','CANCELLED','LOST');
    -- (д) Нет колонок tokens_* в любой таблице: пусто
    SELECT m.name AS tbl, p.name AS col
    FROM sqlite_master m, pragma_table_info(m.name) p
    WHERE m.type = 'table' AND p.name LIKE '%token%';
    ```
    Плюс метрика (координатор): `curl -s 127.0.0.1:8788/metrics | grep
    aqm_dispatch_latency_seconds` → p95 ≤ `dispatch.budget_p95_ms`.
    Результат — `control/invariants.sql` + `.json`.

## Артефакты

- [x] `control/self_update.json` — CONTROL 5 (PASS, 2026-09-29)
- [x] `control/echo_terminal.json` — CONTROL 6 (PASS, p95 59.13 мс)
- [x] `control/doctor_3p14.json`, `control/doctor_vzu5.json` — CONTROL 7
- [ ] `control/reboot_time.txt` — CONTROL 4 (оператор)
- [ ] `control/invariants.sql` + `.json` — CONTROL 1
- [ ] `screens/` — скриншоты из браузера (опционально)
- [ ] `VISUAL.md` — разбор скриншотов (если есть)

## Что нужно оператору (итоговый список, в порядке)

1. Провести 2-часовой тест (фазы 1–3): 6 сессий, ходы, J1 с телефона 5/5,
   остановка vLLM на vzu5-big (root), перезагрузка 3p14-aaa (sudo reboot,
   записать время)
2. Заполнить артефакты фаз 3–4 (reboot_time, doctor после, SQL-инварианты)
3. `tailscale serve 3000` — вернуть 443 BotBoss
4. Сообщить результат
