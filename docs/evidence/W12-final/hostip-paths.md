# Пути регистрации сессии и источник host_ip (W12-доп, п.3)

Шлюз сверяет источник генерирующего запроса с host_ip сессии
(`gateway.go:184`: `remoteIP(r) != sess.HostIP` → 403). Для каждого пути
регистрации host_ip обязан совпадать с адресом, через который кодер сессии
подключается к шлюзу (хост gateway_url).

| # | Путь регистрации | host_ip (источник) | Где задаётся | Кодер ходит в шлюз через | Совпадение |
|---|------------------|--------------------|--------------|--------------------------|------------|
| 1 | Удалённый узел (WS) | локальный адрес интерфейса к координатору | `node/ws.go:279` `m.HostIP = HostIP(hostPort(w.url))` | хост gateway_url (= coordinator.bind) | ✅ IP узла = источник |
| 2 | Встроенный узел | **coordinator.bind** (фикс) | `cmd/runpilot/embedded.go:71` `HostIP: cfg.Coordinator.Bind` | хост gateway_url (= coordinator.bind) | ✅ bind = источник (до фикса: 127.0.0.1 → 403) |
| 3 | exec (`runpilot exec`) | remoteIP запроса /api/v1/jobs | `api/jobs.go:139` `HostIP: remoteIP(r)` | хост gateway_url (= coordinator.bind) | ✅ remoteIP = bind (запрос с машины узла) |
| 4 | `runpilot run` (adopt) | локальный адрес интерфейса к координатору | `cmd/runpilot/run.go:172` `node.HostIP(coordHostPort(cfg.Client.Coordinator))` | хост gateway_url (= coordinator.bind) | ✅ IP узла = источник |

## Валидация конфига (п.2)

`config.Validate()`: хост в `coordinator.gateway_url` обязан равняться
`coordinator.bind`, иначе код выхода 2 (`ErrInvalid`). Кодеры встроенного узла
подключаются к хосту из gateway_url, и источником их соединения становится
этот адрес — он же = host_ip встроенного узла (coordinator.bind).

## Тесты

- `internal/gateway/hostip_test.go`:
  - `TestHostIP_EmbeddedBind` — сессия встроенного узла (host_ip = bind)
    проходит шлюз (200, не 403). Фикс 403 self-node.
  - `TestHostIP_ForeignIP403` — запрос с чужого адреса (host_ip ≠ источник)
    → 403 (безопасность: фикс не открывает доступ).
- `internal/config/validate_45_test.go`:
  - `TestValidate45GatewayURLHostEqualsBind` — хост gateway_url = coordinator.bind
    (совпадение OK, несовпадение → отклонение).

## Итог

До фикса: host_ip встроенного узла был захардкожен `127.0.0.1`
(`loopbackIP`, embedded.go), а кодер ходил в шлюз через coordinator.bind
(100.124.155.106) → несовпадение → 403 на любом управляемом сеансе на
машине координатора. Фикс: host_ip = coordinator.bind (+ валидация gateway_url).
