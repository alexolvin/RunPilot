# Выкат координатора 3p14-aaa из HEAD — 2026-10-06 (п.3)

Выкат координатора 3p14-aaa (tailnet 100.124.155.106, USER-сервис
`runpilot.service`) на `9a4c8d7` (HEAD, чистое дерево).

## Ворота (п.2)
- `go test -race ./...` — 21 ok / 0 FAIL
- `staticcheck ./...` — 0
- `make lint` — 0

## Состояние до
- mode=NORMAL, sessions=null (0 активных аренд)
- узлы: 3p14-aaa (127.0.0.1), vzu5-claw (100.95.195.40)
- текущий бинарник: vcs.revision=4904a651a663b64579d603a34a343c9562a80291 (vcs.modified=false)
- Main PID: 348326 (запущен 21:02:03)

## Установка + рестарт
1. `cp -a <cur> runpilot.bak-4904a65-20261006T204121Z` (бэкап)
2. **Атомарная** замена (бинарник выполняется — plain cp дал бы ETXTBSY):
   `cp -a /tmp/runpilot-9a4c8d7 runpilot.new.$$ && chmod 755 runpilot.new.$$ && mv -f runpilot.new.$$ runpilot`
3. `systemctl --user restart runpilot.service` → Main PID 348326 → 685037 (23:41:41)

## Состояние после
- doctor — все PASS (config, allow_cidrs, coordinator_bind, secrets_env, tmux,
  qwen_settings, qwen_timeout_guard, **coder_timeout_env**, file_mode, metrics, require_direct)
- Запущенный исполняемый файл (`/proc/<pid>/exe` → `/home/user/.local/bin/runpilot`):
  - vcs.revision=9a4c8d7ab45a32bc4fa995836bbc9616f5dc7179 (= HEAD)
  - vcs.modified=false
- mode=NORMAL, sessions=null (0 сессий)
- узлы: 3p14-aaa (127.0.0.1), vzu5-claw (100.95.195.40) — совпадает с «до»

## Откат (если потребуется)
`cp -a runpilot.bak-4904a65-20261006T204121Z runpilot.new.$$ && mv -f runpilot.new.$$ runpilot && systemctl --user restart runpilot.service`
