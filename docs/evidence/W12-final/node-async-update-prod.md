# W12 п.1 — асинхронное обновление узла в проде (7b)

Дата: 2026-10-09. Координатор 77e9550 (3p14-aaa), узлы обновлены до 77e9550
через `upgrade-node.sh` (tailscale ssh). Тест: повторное обновление vzu5-big
до той же версии через `POST /api/v1/nodes/vzu5-big/update`.

## Прогон 1 — FAILED (stale dist)

До теста `~/.local/share/runpilot/dist/runpilot` содержал **d96bf26**
(от Oct 8) — `deploy-coordinator.sh` не обновляет `coordinator.dist_dir`,
поэтому `distBinaryPath()` раздавал старый бинарник и self-update **откатил**
vzu5-big с 77e9550 на d96bf26.

```
POST /api/v1/nodes/vzu5-big/update  ->  202 {"host":"vzu5-big","id":"upd-1791553642334786615"}
poll 1: stage=started
poll 2: stage=started
poll 3: stage=started
poll 4: stage=started
poll 5: stage=done result=ok version=77e9550
   (но узел фактически получил d96bf26 из stale dist)
```

Причина: SHA256 в update-сообщении вычислялся от `distBinaryPath()`
(dist/runpilot = d96bf26), узел скачал d96bf26, SHA256 совпал, установил
старую версию. Механизм self-update работал корректно — устарел dist-бинарник.

## Фикс

`cp -a /home/user/.local/bin/runpilot ~/.local/share/runpilot/dist/runpilot`
(dist теперь 77e9550, SHA256 383485c7… совпадает с координатором).

## Прогон 2 — PASS

```
POST /api/v1/nodes/vzu5-big/update  ->  202 {"host":"vzu5-big","id":"upd-1791553801169137420"}
poll 1: {"stage":"started","started_at":"2026-10-09T13:50:01.16914088Z"}
poll 2: {"stage":"started"}
poll 3: {"stage":"started"}
poll 4: {"stage":"started"}
poll 5: {"stage":"started"}
poll 6: {"stage":"done","result":"ok","version":"77e9550",
         "started_at":"2026-10-09T13:50:01.16914088Z",
         "ended_at":"2026-10-09T13:50:10.080848215Z"}
```

После self-update (рестарт узла):

```
3p14-aaa  ver=77e9550
vzu5-big  ver=77e9550
vzu5-claw ver=77e9550
```

Итог: PASS — async 202 + стадии started→done, узел на версии координатора.

## Рекомендация (оперативный пробел)

`deploy/deploy-coordinator.sh` не обновляет `coordinator.dist_dir`. После
выката координатора `dist/runpilot` остаётся со старой версией → self-update
узлов разворачивает устаревший бинарник. Фикс процедуры: после выката
копировать актуальный бинарник в `coordinator.dist_dir/runpilot` (или
удалить stale-копию, чтобы `distBinaryPath()` отдавал собственный бинарник).
