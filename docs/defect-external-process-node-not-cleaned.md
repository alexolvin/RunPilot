# DEFEKT: external_process — ряды удалённого узла не чистятся + кросс-детект на одном хосте

**Дата:** 2026-10-21 · **Статус:** зафиксирован, фикс не начат (решение владельца: не менять БД руками)
**Инцидент:** vzu5-big (100.126.222.23), узлы `botagent` + `admin` (admin выведен из эксплуатации).

## Суть

Два связанных дефекта в учёте внешних кодеров (`external_process`):

1. **Удаление узла не чистит `external_process`.** `handleNodeDelete`
   (DELETE /api/v1/nodes/{host}, `internal/api/nodes.go`) делает
   `RevokeNodeToken` + `hub.DetachHost` + `hub.Unregister` + `sched.NodeLost` —
   но **не удаляет** ряды `external_process` узла. Штатного пути удалить ряды по
   node_id нет: в `store/external.go` есть только `ExternalPurge(cutoff)`
   (retention по `last_seen`), per-node delete отсутствует. Результат: после
   вывода узла из эксплуатации его ряды остаются в БД (и в live-списке
   GET /api/v1/externals) со статусом ACTIVE.

2. **Кросс-детект на одном хосте.** `ScanExternal`
   (`internal/node/external.go`) исключает управляемые кодеры через
   `CoderOutOfQueue(sessionSID, …)`, где `sessionSID` выводится из
   `panePIDToSID` **данного узла**. Два узла на одном хосте (разные os_user) —
   разные `panePIDToSID`. Кодекс чужой сессии (в tmux другого узла) не попадает в
   `panePIDToSID` этого узла → `SessionSID=""` → `CoderOutOfQueue=true` → кодер
   управляемой сессии узла A учтён как «внешний» узлом B.

## Идентификация сессий (подтверждено по БД, 2026-10-21)

- `bbmig` = **A55T7004R1** — состояние **GONE** (last_turn_end 2026-10-20T20:23:45Z).
- `migration` = **RJGCN1D737** — состояние **IDLE** (живая, узел botagent).
- Ранее в отчёте bbmig ошибочно назван RJGCN1D737; корректно: bbmig=A55T7004R1 (GONE),
  migration=RJGCN1D737.

## Факты инцидента (vzu5-big, 2026-10-21)

- Сессия `migration` (RJGCN1D737, узел `botagent`, cwd /srv/botboss/migration)
  запущена 2026-10-20T21:22:56Z; её процессы: tmux-сервер pid 2124528 + qwen
  pid 2124529 (start_time 1792531376).
- Узел `admin` (выведен из эксплуатации) в тот же период сканировал хост и
  записал те же pid 2124528/2124529 как внешние под `vzu5-big:admin`
  (first_seen 2026-10-20T21:22:57Z, target=unknown — чужая сессия, sid не выведен).
- После вывода `admin` ряды `vzu5-big:admin` для pid 2124528/2124529 остались
  ACTIVE в `external_process` (last_seen 2026-10-21T08:59:20Z) и показываются в
  GET /api/v1/externals — хотя узла `admin` больше нет.
- Штатно удалить эти ряды нельзя: `handleNodeDelete` их не трогает, а `admin`
  offline → DELETE вернёт 503 NODE_OFFLINE.

## План фикса (реализация не начата)

1. **Store:** `ExternalDeleteByNode(nodeID)` — `DELETE FROM external_process
   WHERE host=?` (nodeID = host:os_user).
2. **handleNodeDelete:** после `hub.Unregister` вызвать
   `s.store.ExternalDeleteByNode(nodeID)`.
3. **Кросс-детект:** кодер с `target = gateway:<sid>` существующей сессии
   (по OPENAI_BASE_URL) исключать из внешних независимо от `panePIDToSID` узла
   — либо координатор фильтрует `externals` по «владению» сессией.
4. **Тесты:** (a) удаление узла чистит его `external_process`; (b) кодер чужой
   сессии на том же хосте не учитывается как внешний.

## Почему не менять БД руками

Узлы на одном хосте делят pid-пространство; «внешний» ряд может быть живым
процессом чужой управляемой сессии (pid 2124528/2124529 = сессия migration
botagent'а — завершать их нельзя). Ручной `DELETE FROM external_process WHERE
host='vzu5-big:admin'` безопасен (это только ряды admin), но штатного пути в
коде нет — фикс через код (п.1-2), не через БД.
