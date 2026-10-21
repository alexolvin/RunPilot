# DEFEKT: node_token ключён по host — два os_user на одном хосте отзывают токены друг друга

**Дата:** 2026-10-21 · **Статус:** зафиксирован, фикс не начат (решение владельца: схему менять отдельным этапом)
**Инцидент:** vzu5-big (100.126.222.23), узлы `botagent` + `admin`.

## Суть

Таблица `node_token` (миграция `005_node_token.sql`) имеет `host TEXT PRIMARY KEY` —
**один токен на хост**. Идентичность узла в остальной системе — `host:os_user`
(хаб `Register/Unregister`, `pickNode`, DELETE-разрешение). Модель токена этому не
соответствует: два узла на одном хосте с разными `os_user` не могут одновременно
держать активные токены.

Механизм: `store.BindTokenHost` (claim) при существующем ряде `host` входит в
ветку re-enroll и **без предупреждения** отзывает старый активный токен хоста
(`UPDATE node_token SET revoked_at=… WHERE host=? AND revoked_at='' AND token<>?`),
подставляя новый. Координатор не различает «тот же узел перерегистрируется» и
«другой os_user на том же хосте», т.к. claim шлёт только `{host}` (без `os_user`),
а `enrollClaim` не логируется.

## Факты инцидента (vzu5-big, 2026-10-20)

- 20:26:32Z — claim от `admin` (автоустановка координатора, `install.sh`,
  100.126.222.23) привязал токен `988545f8…` к хосту `vzu5-big` и **отозвал**
  токен `botagent` (`6c11dc27…`).
- `botagent` (сервис active, linger=yes) перестал проходить аутентификацию →
  отсутствует в `/api/v1/nodes` → не шлёт GPU-телеметрию → у сервера `vzu5`
  `power=None` в вебе.
- Восстановлено 2026-10-21 re-enroll `botagent` (новый токен `95600fa2…`);
  побочно отозван токен `admin` (симметричное проявление того же дефекта).

## Воспроизводящий тест

`internal/store/node_token_test.go` →
`TestBindTokenHostTwoOsUsersSameHostRevokeEachOther`: два узла на одном host
(`h:alice` → `h:bob`); текущее (buggy) поведение — токен `alice` отозван claim'ом
`bob`. После фикса условие инвертируется (токен `alice` остаётся валидным).

## План фикса (реализация не начата)

**Модель:** ключ токена `host` → `node_id` (`host:os_user`; `host`, если
`os_user` пуст). Два узла на одном хосте — разные `node_id` — разные токены.

1. **Схема** (миграция `00X_node_token_node_id.sql`):
   `node_token(node_id TEXT PRIMARY KEY, token, created_at, revoked_at)`;
   копирование старых рядов `node_id = host`; pending-ряд (`host=''`) → `node_id=''`.
2. **Claim:** `install.sh` шлёт `{"host":…,"os_user":"$(id -un)"}`;
   `enrollClaim` считает `nodeID = host[:os_user]`; `BindTokenHost` →
   `BindTokenNodeID(token, nodeID, …)`: re-enroll-ветка отзывает старый токен
   только того же `node_id` (перерегистрация того же узла), чужие `node_id`
   на хосте не трогает.
3. **Auth:** `VerifyNodeToken` возвращает `node_id`; N7-проверка в WS-hello
   сравнивает `node_id` токена с `nodeID` hello (`host:os_user`).
4. **Revoke:** удаление узла — `RevokeNodeToken(nodeID)` (сейчас `hostOf(nodeID)`).
5. **Миграция данных (одноразово, после деплоя):**
   - старые ряды: `node_id = host` (`vzu5-claw`, `selftest`, pending);
   - `vzu5-big` → `vzu5-big:botagent` — единственный узел на хосте (admin выведен
     из эксплуатации; активный токен vzu5-big уже botagent'а `95600fa2…`, старый
     токен admin перезаписан re-enroll'ом — особого ряда для admin не нужно);
   - `botagent`: re-issue токена для `vzu5-big:botagent` (ряд + `secrets.env` +
     рестарт службы) — либо re-enroll через диалог после фикса.
6. **Тесты:** инвертировать `TestBindTokenHostTwoOsUsersSameHostRevokeEachOther`;
   `TestBindTokenHostReEnrollReplacesOld` сохраняет смысл (re-enroll того же
   `node_id` отзывает старый токен).

## Почему claim был принят без предупреждения

См. «Суть»: в модели токена нет `os_user`, claim несёт только `host`,
`BindTokenHost` интерпретирует существующий ряд хоста как re-enroll того же узла
и молча отзывает старый токен; `enrollClaim` не имеет проверки
«на хосте есть активный токен другого узла» и не логируется.
