# ТЗ «запуск сессий на узле выбранного пользователя» (host:os_user)

Идентичность узла = `<host>:<os_user>`. Два узла с одинаковым host и разными
os_user подключаются одновременно. Сессия привязана к узлу (host + os_user).

## Изменения

| Слой | Файл | Что |
|------|------|-----|
| proto | `internal/proto/proto.go` | `OsUser` в hello; `Agent` в spawn; `Home` в dirs; коды `NAME_IN_USE`/`TMUX_SESSION_EXISTS`/`AGENT_NOT_FOUND`; `KindCheckSettings` |
| node | `internal/node/node.go` | `OSUser()` (user.Current) |
| node | `internal/node/ws.go` | hello несёт `os_user` |
| node | `internal/node/ops.go` | spawn: валидация `agent` (AGENT_NOT_FOUND), `TMUX_SESSION_EXISTS`; `checkSettings` (doctor per-node); `listDirs` возвращает `Home` |
| store | `internal/store/session.go` | `OsUser` в SessionRecord, `NodeID()`, миграция 023, `MigrateSessionOsUser` |
| hub | `internal/api/hub.go` | реестр по `nodeID` (host:os_user); `PushToNode`; `HasHost` |
| server | `internal/api/server.go` | hello: nodeID; read-цикл по nodeID; `reconcilePanesWithStore` по `NodeID()` |
| spawn | `internal/api/spawn.go` | `pickNode` по nodeID; `m.Agent`; `rec.OsUser` |
| nodes | `internal/api/nodes.go` | `hostOf()` (токен per-host); `handleNodeSettingsCheck` (doctor) |
| scheduler | `internal/scheduler/*.go` | `sess.NodeID()` вместо `sess.Host` |
| web | `web/app/store.js` | `normSession`/`normNode`: `os_user` + `node_id` |
| web | `web/app/components/new-session-dialog.js` | узел (host:os_user), dir default = home узла, POST node=node_id |
| web | `web/app/views/{session,queue,nodes}.js`, `session-row.js` | показ `node_id` |
| ls | `cmd/runpilot/ls.go` | `nodeIDOf(s)` (host:os_user) |
| doctor | `cmd/runpilot/doctor.go`, `internal/doctor/doctor.go` | `node_qwen_settings` — per-node check (диспатч check_settings) |
| migration | `internal/store/migrations/023_session_os_user.sql` | `os_user TEXT NOT NULL DEFAULT ''` |

## Тесты

- `TestNodeIdentityCoexist` — два узла srv1:alice + srv1:bob сосуществуют.
- `TestNodeIdentitySpawnByName` — spawn на srv1:alice → 201; то же имя на srv1:bob → 409 NAME_IN_USE.
- `TestSpawnTmuxSessionExists` — tmux-сессия существует → TMUX_SESSION_EXISTS.
- `TestMigrateAndSchemaVersion` / `TestOpenIdempotent` — schema_version = 23.

Вывод: `test-output.txt`.

## Pre-existing (не из этой задачи)

3 теста падают из-за `browser.proxy` (браузная задача, commit d6d3065):
- `TestDefaultsCoverSection13`, `TestSchemaCoversAllWorkingKeys` (config)
- `TestSettingsGetReturnsWorkingValues` (api)

Подтверждено бисекцией (stash → тесты на HEAD → падает).
