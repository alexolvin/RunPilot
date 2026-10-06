# Остановка стенда + рабочий узел

Дата: 2026-10-06. Stand: `docs/evidence/hold-silent/stand`.

## 1. Остановка стенда только по PID из info-файла

Команда (PID из info-файла):
```bash
S=/mdisk/myhub/RunPilot/docs/evidence/hold-silent/stand
FP=$(python3 -c "import json;print(json.load(open('$S/info.json'))['fakellm_pid'])")
SP=$(python3 -c "import json;print(json.load(open('$S/info.json'))['serve_pid'])")
NP=$(python3 -c "import json;print(json.load(open('$S/info.json'))['node_pid'])")
kill -TERM $FP $SP $NP          # SIGTERM трём PID
export TMUX_TMPDIR=$S/tmux; tmux -L hold-silent kill-server   # qwen-панели
sleep 3                          # grace
for p in $FP $SP $NP; do kill -0 $p 2>/dev/null && kill -KILL $p; done  # выжившие
```

### Остановка A (env/600, стенд IDLE) — 06:55:00.549 UTC
Убитые PID (из info.json, с командной строкой):
| PID | Командная строка |
|---|---|
| 2892449 | `/tmp/hl/fakellm -port 18801 -model fake-e6` |
| 2892450 | `/tmp/hl/runpilot serve --config .../stand/home/.config/runpilot/config.yaml` |
| 2892451 | `/tmp/hl/runpilot node --config .../stand/home/.config/runpilot/config.yaml` |

Итог: SIGTERM → **все 3 PID мертвы за < 3 с**, tmux-сервер «no server running»,
orphan'ов нет. (Стенд был IDLE — без in-flight, graceful быстрый.)

### Остановка B (env/20, trap-чек, IN-FLIGHT) — 06:59:40.736 UTC
Тот же launcher, SIGTERM **launcher'у** (2945852) при **активном in-flight SSE A**:
| PID | Процесс |
|---|---|
| 2945861 | `/tmp/hl/fakellm -port 18801 -model fake-e6` |
| 2945862 | `/tmp/hl/runpilot serve --config .../config.yaml` |
| 2945863 | `/tmp/hl/runpilot node --config .../config.yaml` |
| 2945852 | `bash launcher.sh` (parent) |

Итог: SIGTERM launcher → trap → **все 4 PID мертвы к T+1 с** (замер по 1 с:
T+1..T+5 alive_count=0). ≤ 5 с ✓.

## 2. Trap-фикс в launcher.sh (FINDING из прошлого — РЕШЁН)

**Корневая причина** (почему раньше SIGTERM не останавливал за 5 с): serve/node
обрабатывают SIGTERM штатно (`signal.NotifyContext`, serve.go:259 / node.go:65),
но grace = `coordinator.shutdown_grace_sec` = **30** (`config/defaults.go:22`).
При in-flight запросах/SSE graceful shutdown ждёт до 30 с → > 5 с.

**Фикс** (`launcher.sh`, `cleanup()`):
```bash
for p in "$FP" "$SP" "$NP"; do [ -n "$p" ] && kill -TERM "$p" 2>/dev/null; done
TMUX_TMPDIR="$S/tmux" tmux -L "$SOCKET" kill-server 2>/dev/null
sleep 2                          # grace 2с
for p in "$FP" "$SP" "$NP"; do   # SIGKILL выжившим (serve grace=30 — не ждать)
  [ -n "$p" ] && kill -0 "$p" 2>/dev/null && kill -KILL "$p" 2>/dev/null
done
wait 2>/dev/null
```
SIGTERM → grace 2 с → SIGKILL выжившим. В стенде (fakellm — единственный
апстрим) SIGTERM уже рвёт in-flight цепь (fakellm умирает первым → запрос serve
падает → graceful быстрый), SIGKILL — страховка для реального апстрима.
**Результат: SIGTERM launcher → нет PID за ≤ 1 с (требовано ≤ 5 с). PASS.**

## 3. Рабочий serve (3p14-aaa)

```
$ ps -o pid,lstart,etime,args -p 3446855
3446855  Fri Oct  2 17:28:40 2026  3-16:34:45  /home/user/.local/bin/runpilot serve
```
АПИ слушает на `100.124.155.106:8788` (tailscale), веб на `127.0.0.1:8790`.

## 4. Рабочий узел 3p14-aaa — embedded в serve

Узел **встроен** в `runpilot serve`:
- код: `cmd/runpilot/embedded.go` → `embeddedNode()` (строка 52);
- запуск: `cmd/runpilot/serve.go:310` при `coordinator.embedded_node=true`
  (дефолт `true`, `config/defaults.go:28`).

Отдельного процесса `runpilot node` на 3p14-aaa нет — штатная архитектура
координатор-хоста (`pgrep` показывает только `serve` 3446855).

### /api/v1/state (working coordinator 100.124.155.106:8788, 07:04 UTC)
```json
[
  {"Host":"3p14-aaa","HostIP":"127.0.0.1","RUNPILOTVersion":"0.0.0-dev",
   "TmuxVersion":"tmux 3.2a","OS":"linux","Sockets":["default"],"unmanaged":0,
   "health":{"host":"3p14-aaa","disk_free_mb":72363,"qwen_version":"0.24.7",
             "at":"2026-10-06T07:04:09.161019435Z"}},
  {"Host":"vzu5-claw","HostIP":"100.95.195.40","RUNPILOTVersion":"0.0.0-dev",
   "TmuxVersion":"tmux 3.4","OS":"linux","Sockets":["default"],"unmanaged":0,
   "health":{"host":"vzu5-claw","time_skew_ms":56,"disk_free_mb":378046,
             "qwen_version":"0.24.7","at":"2026-10-06T07:04:01.411994149Z"}}
]
```
3p14-aaa: HostIP=127.0.0.1 (локальный хост координатора), health актуален
(07:04:09), qwen 0.24.7, tmux 3.2a. Узел подключён.

## Вывод

Остановка стенда — только по PID из info-файла (SIGTERM → grace → SIGKILL +
tmux kill-server), orphan'ов нет. **Trap-фикс работает: SIGTERM launcher →
все PID мертвы за ≤ 1 с** (корень: serve `shutdown_grace_sec=30`; фикс: grace 2 с
+ SIGKILL). Рабочий узел 3p14-aaa — embedded в serve (`embedded.go:52`,
`serve.go:310`), подключён, health актуален.
