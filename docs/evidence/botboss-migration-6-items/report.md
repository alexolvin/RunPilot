# Отчёт: задание агенту RunPilot (6 пунктов)

Миграция BotBoss: 3p14-aaa → vzu5-big / vzu5-claw (ограничение Binance). 3p14 остаётся доверенным.

- **Дата:** 2026-10-19
- **Координатор:** 3p14-aaa `100.124.155.106:8788` (user, Bearer-токен из `~/.config/runpilot/env`)
- **Узел:** `vzu5-big:botagent` `100.126.222.23` (uid 1001, home `/home/botagent`, tmux-сокет `/tmp/tmux-1001/default`, qwen `0.25.3`)
- **Смежный узел:** vzu5-claw `100.95.195.40`
- **БД:** `/home/user/.local/share/runpilot/runpilot.db`

---

## Пункт 1 — код `listDirs` и откуда `/srv/botboss` для `vzu5-big:botagent`

### 1.1. `listDirs` — `internal/node/ops.go:197-231`

```go
// internal/node/ops.go:197
func (n *Node) listDirs(ctx context.Context, m proto.Msg) (*Reply, error) {
	max := n.cfg.Web.DirsMax // web.dirs_max, default 100
	var dirs []string
	seen := map[string]bool{}
	var rootOut []string
	for _, root := range n.rootsList() {
		rroot, err := filepath.Abs(expandTilde(root))
		if err != nil {
			continue
		}
		if rroot, err = filepath.EvalSymlinks(rroot); err != nil {
			continue
		}
		if !seen[rroot] {
			seen[rroot] = true
			rootOut = append(rootOut, rroot)
		}
		entries, err := os.ReadDir(rroot)   // ops.go:216
		if err != nil {                     // ops.go:217
			continue                         // ops.go:218  ← тихий skip при ошибке
		}
		for _, e := range entries {
			if e.IsDir() {
				dirs = append(dirs, rroot+"/"+e.Name())
			}
		}
	}
	sort.Strings(dirs)
	if len(dirs) > max {
		dirs = dirs[:max]
	}
	return &Reply{CmdID: m.CmdID, Result: ResSent, Detail: strings.Join(dirs, "\n"),
		Roots: rootOut, Home: n.homeDir()}, nil
}
```

### 1.2. Откуда корни — `rootsList()` — `internal/node/ops.go:59-65`

```go
// internal/node/ops.go:59
func (n *Node) rootsList() []string {
	n.rootsMu.Lock()
	defer n.rootsMu.Unlock()
	if len(n.roots) > 0 {
		return n.roots                      // из SetProjectRoots (ops.go:53)
	}
	return n.cfg.Node.ProjectRootsDefault  // из конфига узла
}
```

### 1.3. Цепочка загрузки `project_roots_default`

| Файл:строка | Что |
|---|---|
| `internal/config/config.go:387` | `ProjectRootsDefault []string `yaml:"project_roots_default"`` |
| `internal/node/node.go:111` | `roots: cfg.Node.ProjectRootsDefault` (при конструировании Node) |
| `cmd/runpilot/root.go:42` | `c, err := config.Load(cfgPath)` — узел грузит **свой** `~/.config/runpilot/config.yaml` |

### 1.4. Откуда `/srv/botboss`

`/srv/botboss` берётся из **локального конфига самого узла** `vzu5-big`, а не из таблицы настроек координатора:

`/home/botagent/.config/runpilot/config.yaml` (на vzu5-big):
```yaml
client:
  coordinator: http://100.124.155.106:8788
  token_env: RUNPILOT_NODE_TOKEN
node:
  host: ""
  project_roots_default:
    - "~"
    - "/srv/botboss"
profiles:
  qwen:
    command: /home/botagent/.local/bin/qwen
```

Координатор `ProjectRoots` узлу не шлёт (обработчик config-команды их принимает, но координатор не отправляет) — узел читает собственный `config.yaml`. Поэтому удалённые узлы используют **свой** конфиг, а не setting-таблицу координатора.

---

## Пункт 2 — `GET /api/v1/nodes/vzu5-big:botagent/dirs` после рестарта узла

Рестарт узла: **08:39:28Z**. Повторный запрос — **08:56:37Z** и **08:56:58Z**.

Сырой ответ (метка времени запроса):
```
2026-10-19T08:56:37Z  →  {"dirs":[],"home":"/home/botagent","result":"SENT","roots":["/home/botagent","/srv/botboss"]}
2026-10-19T08:56:58Z  →  {"dirs":[],"home":"/home/botagent","result":"SENT","roots":["/home/botagent","/srv/botboss"]}
```

**Результат: `dirs` всё ещё пуст** после рестарта. `roots` корректны (`/home/botagent`, `/srv/botboss`), но список каталогов пуст.

**Причина (незакрытый баг):** `os.ReadDir` в Go-процессе узла на vzu5-big возвращает пусто, хотя `ls` / Python `os.listdir` от `botagent` видят содержимое (`/srv/botboss` — 2 записи, home — 12). Тихий `continue` при `err != nil` (`ops.go:217-218`) скрывает ошибку. SELinux `unconfined_t` — не причина. Локализация: `internal/node/ops.go:216`.

---

## Пункт 3 — лог планировщика 07:45:37Z

В **07:45:37Z** создано 5 сессий `bbmig-1…5` в `/srv/botboss/migration` на `vzu5-big:botagent` (`auto_enqueue:true`):

```
2026-10-19T07:45:37Z
--- bbmig-1 ---  {"name":"bbmig-1","node":"vzu5-big:botagent","pane":"%0","sid":"FQTSD117E1"}
--- bbmig-2 ---  {"name":"bbmig-2","node":"vzu5-big:botagent","pane":"%1","sid":"YAEABA7DT0"}
--- bbmig-3 ---  {"name":"bbmig-3","node":"vzu5-big:botagent","pane":"%2","sid":"S617H9WJ0G"}
--- bbmig-4 ---  {"name":"bbmig-4","node":"vzu5-big:botagent","pane":"%3","sid":"7E2MVD4TA6"}
--- bbmig-5 ---  {"name":"bbmig-5","node":"vzu5-big:botagent","pane":"%4","sid":"NMNGN7P4HV"}
```

**Кто занимал сервер vzu5 в 07:45:37Z:** сессия **NextLife** (`1YHT3GSSYS`, host `vzu5-claw`) — аренда vzu5 выдана **07:36:22Z**, снята **07:53:20Z** (`TURN_DONE`). То есть big **был занят** → повторный тест при свободном big не потребовался.

**Записи в очереди / ineligible_reason:** `bbmig-1…5` **не попали в очередь** — `auto_enqueue` упал на пустом вводе панели:
```
2026-10-19T07:46:53Z  POST /api/v1/sessions/FQTSD117E1/enqueue
{"code":"ENQUEUE","detail":"enqueue: FQTSD117E1: ввод пуст"}
```
Сессии остались `IDLE` (не в очереди), `ineligible_reason` не присваивался (записи в `queue_entry` нет).

**0 аренд на 3p14 (проверено 08:58:47Z):**
```
bbmig в очереди?           → (пусто — не в очереди)
Аренды bbmig на 3p14       → 0
Аренды bbmig вообще        → (пусто — аренд нет)
LEASE_GRANT на 3p14 с 07:45:37Z → (пусто — нет выдач на 3p14)
```

---

## Пункт 4 — enroll: сырой вывод ручной установки vs `auto_install`

### 4.1. Ручной install (ssh + `curl | bash`), **07:19:34Z → 07:19:42Z**

Команда:
```
ssh -o BatchMode=yes -o ConnectTimeout=10 -o StrictHostKeyChecking=accept-new \
  botagent@100.126.222.23 \
  'curl -fsSL http://100.124.155.106:8788/enroll/6f8528647870b4bc0576294b8f76317a/install.sh | bash'
```
Сырой вывод (`exit=0`):
```
runpilot: скачиваю бинарник…
runpilot: привязываю узел…
{"host":"vzu5-big","ok":"true"}
runpilot: устанавливаю службу узла…
записан юнит: /home/botagent/.config/systemd/user/runpilot-node.service
служба включена и запущена
блок runpilot записан в /home/botagent/.tmux.conf
tmux source-file пропущен (сервер не запущен?): no server running on /tmp/tmux-1001/default
runpilot: узел запущен (runpilot-node: active) — «Узел vzu5-big подключён» появится в панели.
exit=0
```

### 4.2. `auto_install` — повтор (события `NODE_ENROLL` в БД)

| ts | result | вывод |
|---|---|---|
| 07:14:11Z | failed | `ssh: Could not resolve hostname vzu5-big: Name or service not known` |
| 07:17:21Z | failed | `ssh: Could not resolve hostname vzu5-big: Name or service not known` |
| **09:05:03Z** | **ok** | идентичен ручному (см. ниже) |

Сырой вывод успешного `auto_install` (09:05:03Z) — **построково идентичен** ручному:
```
runpilot: скачиваю бинарник…
runpilot: привязываю узел…
{"host":"vzu5-big","ok":"true"}
runpilot: устанавливаю службу узла…
записан юнит: /home/botagent/.config/systemd/user/runpilot-node.service
служба включена и запущена
блок runpilot записан в /home/botagent/.tmux.conf
tmux source-file пропущен (сервер не запущен?): no server running on /tmp/tmux-1001/default
runpilot: узел запущен (runpilot-node: active) — «Узел vzu5-big подключён» появится в панели.
```

**Сравнение:** после фикса резолвинга tailnet-hostname → IP (commit `4c61616`, MagicDNS выкл / хост вне `/etc/hosts`) `auto_install` даёт **тот же результат** что и ручная установка (оба `ok`, вывод идентичен).

**Побочный эффект:** `auto_install` (09:05:03Z) **перезаписал** `/home/botagent/.config/runpilot/config.yaml`, сбросив `project_roots_default` (пропал `/srv/botboss`) → spawn вернул `DIR_FORBIDDEN`. Восстановлено вручную + `systemctl --user restart runpilot-node`. Поведение `install.sh` (перезапись operator-конфига) стоит поправить.

---

## Пункт 5 — состояние `bbmig-1…5` после закрытия панелей

Проверено **09:06:33Z** (session table):

```
FQTSD117E1  bbmig-1  vzu5-big  botagent  GONE  2026-10-19T08:39:27.762365852Z
YAEABA7DT0  bbmig-2  vzu5-big  botagent  GONE  2026-10-19T08:39:27.762674561Z
S617H9WJ0G  bbmig-3  vzu5-big  botagent  GONE  2026-10-19T08:40:59.241281171Z
7E2MVD4TA6  bbmig-4  vzu5-big  botagent  GONE  2026-10-19T08:40:59.24168758Z
NMNGN7P4HV  bbmig-5  vzu5-big  botagent  GONE  2026-10-19T08:39:27.764067967Z
```
Живых tmux-панелей bbmig на vzu5-big: **нет**. Все 5 — **GONE** (ожидание выполнено).

**Штатное закрытие сессии в коде ЕСТЬ** — не дефект:
- `internal/api/commands.go:192` — `func (s *Server) CmdClose(ctx, sid)`: owned-сессия → `KindKillSession` узлу → `GONE`; панели в hub нет → `GONE` без операции узла.
- Команда `close <t>`: `internal/command/command.go:100`, `internal/command/exec.go:297-300`, `executor.go:219`.

---

## Пункт 6 — сессия в `/srv/botboss/migration`: `whoami; hostname; qwen --version`

Сессия **bbrun-1** (`YN4H3JTBFR`) создана **09:54:14Z** на `vzu5-big:botagent` в `/srv/botboss/migration`. Ввод отправлен **09:59:16Z**.

Ход ждал свободный слот (оба были заняты: MoxMate на vzu5, сессия координатора на 3p14). Ход MoxMate завершился сам в **10:12:58Z** → vzu5 освободился → bbrun-1 выдан в **10:13:00Z**.

**Вывод панели (совпало с ожиданием):**
```
botagent
vzu5-big
0.25.3
```

**Запись аренды (выбранный сервер — vzu5):**

| поле | значение |
|---|---|
| sid | `YN4H3JTBFR` (bbrun-1) |
| server / slot | **vzu5** / 1 |
| origin / rule | IMPLICIT / **priority** |
| granted_at | 2026-10-19T10:13:00.792Z |
| ход | #263, OK, 3 запроса, 10:13:00.792Z → 10:13:47.788Z |
| итог | аренда RELEASED по `TURN_DONE`, сессия закрыта штатно (`/close` → GONE, 10:16:06Z) |

Сыро (БД):
```
lease:  YN4H3JTBFR | vzu5 | 1 | RELEASED | IMPLICIT | priority | 2026-10-19T10:13:00.792050037Z
turn:   263 | YN4H3JTBFR | OK | 2026-10-19T10:13:00.792050037Z | 2026-10-19T10:13:47.788502168Z | 3
```

---

## Принятые решения владельца

- **`RUNPILOT_NODE_TOKEN` в env кодера** — токен узла попадает в env tmux-сервера и наследуется кодер-сессиями. **Принято владельцем как допустимое (2026-10-19)** — не дефект, кодовый фикс не требуется.

## Нерешённые / к фиксу

1. **`listDirs` пуст на vzu5-big** — `os.ReadDir` в Go-процессе узла возвращает пусто (`ls`/Python работают). `internal/node/ops.go:216`, тихий `continue` при ошибке (`:217-218`) скрывает причину. Незакрытый баг.
2. **`install.sh` перезаписывает `config.yaml` узла** при `auto_install` — потерял `project_roots_default: /srv/botboss` (восстановлено вручную). Поведение `install.sh` стоит поправить (не затирает operator-конфиг).
