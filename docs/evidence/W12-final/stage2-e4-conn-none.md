# Stage 2: Причина conn=None (stand log)

Дата: 2026-10-09

## Причина

`conn=None` в `GET /api/v1/state` — WS-соединение узла отсутствовало. Из
`/tmp/runpilot-stand/node-e4.log`:

```
14:57:10 INFO  node: старт  coordinator=http://127.0.0.1:18788 sockets=["default"] scan_sec=2
15:09:15 WARN  node: канал: read: websocket: close 1006 (abnormal closure): unexpected EOF
15:09:16 WARN  node: канал: dial: dial tcp 127.0.0.1:18788: connect: connection refused
... (повтор каждые ~30 с до остановки)
15:16:12 INFO  node: остановлен
```

Причины:
1. **Отдельный процесс узла** сканировал **дефолтный tmux-сокет** оператора
   (нарушение правила 13) — `sockets=["default"]` в `node.yaml`.
2. Когда stand-координатор был остановлен (kill), узел не мог переподключиться:
   `connection refused` на 127.0.0.1:18788. WS-канал оборвался (close 1006),
   `conn` в state стал `None`.

## Исправление (Текущий дизайн)

Стенд теперь использует **встроенный узел** (`embedded_node: true`) с
**отдельным tmux-сокетом `e2e`** (`node.tmux_sockets: [e2e]`). Отдельного
процесса узла и WS-канала нет — транспорт внутренний (`memConn`). `conn=None`
больше не применим: узел хоста всегда «подключён» координатору.

- Бинарники — в каталоге стенда (`/tmp/runpilot-stand/`), не в
  `/home/user/.local/bin`.
- Порты стенда 18787/18788/18789 (не 8787/8788).
- tmux-сокет `e2e` (не `default`).
