# Stage 0: Версии узлов после восстановления координатора

Дата: 2026-10-09 ~15:15 MSK

## Координатор

| Параметр | Значение |
|---|---|
| Бинарник | /home/user/.local/bin/runpilot |
| vcs.revision | 77e9550c1f3001344c1cbe4dd12d0105f39ae218 |
| vcs.modified | false |
| Служба | runpilot.service (systemd --user), active |
| Порт API | 100.124.155.106:8788 |
| Порт gateway | 100.124.155.106:8787 |
| Ворота 0 аренд | PASS (0 активных аренд до deploy) |
| Deploy | deploy-coordinator.sh, PASS |

## Узлы

| Узел | IP | Версия | tmux | Сокеты |
|---|---|---|---|---|
| 3p14-aaa | 100.124.155.106 | 77e9550 | tmux 3.2a | default |
| vzu5-big | 100.126.222.23 | d96bf26 | tmux 3.4 | default |
| vzu5-claw | 100.95.195.40 | d96bf26 | tmux 3.4 | default |

## Примечания

- vzu5-claw нестабилен: периодически пропадает из state и переподключается.
- Токен оператора изменён (попал в командные строки): новый токен в
  ~/.config/runpilot/env (0600). Старый токен → 401, новый → 200.
