# Сценарий приёмки п.5 (vzu5, ≤ 15 мин)

## Предусловия
- vzu5 UP в RunPilot
- num_requests_running = 0 в течение 60 с
- Координатор запущен (4ffc795 или новее)

## Шаги

### 1. Проверка vzu5 (1 мин)
```bash
# UP + слот FREE
curl -s -H "Authorization: Bearer $TOKEN" http://100.124.155.106:8788/api/v1/servers | \
  python3 -c "import sys,json; s=[x for x in json.load(sys.stdin) if x['name']=='vzu5'][0]; print(s['state'], s['slots_info'])"

# num_requests_running = 0 (2 проверки с интервалом 60с)
curl -s http://100.126.222.23:8004/metrics | grep num_requests_running
sleep 60
curl -s http://100.126.222.23:8004/metrics | grep num_requests_running
```

### 2. Spawn A (пин vzu5) (1 мин)
```bash
curl -s -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  http://100.124.155.106:8788/api/v1/sessions/spawn \
  -d '{"name":"acc-a","dir":"/mdisk/myhub/RunPilot","node":"3p14-aaa","pin":"vzu5"}'
```
Ожидаемый ответ: sid, state=IDLE.

### 3. Paste A + ждать RUNNING (2 мин)
```bash
# Paste (запуск хода)
curl -s -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  http://100.124.155.106:8788/api/v1/sessions/<SID_A>/paste \
  -d '{"text":"Скажи OK","enqueue":true}'

# Ждать пока A получит аренду (ACTIVE на vzu5)
sqlite3 ~/.local/share/runpilot/runpilot.db \
  "SELECT state FROM lease WHERE sid='<SID_A>' AND state='ACTIVE'"
```

### 4. Spawn B (пин vzu5) (1 мин)
```bash
curl -s -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  http://100.124.155.106:8788/api/v1/sessions/spawn \
  -d '{"name":"acc-b","dir":"/mdisk/myhub/RunPilot","node":"3p14-aaa","pin":"vzu5"}'
```

### 5. Paste B + ждать HELD (3 мин)
```bash
curl -s -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  http://100.124.155.106:8788/api/v1/sessions/<SID_B>/paste \
  -d '{"text":"Скажи OK","enqueue":true}'

# B должен быть в HELD (удержание, слот занят A)
# Проверить в вебе: скриншот (HELD)
# Проверить tmux: строка статуса содержит "held"
tmux -L default list-panes -a -F "#{session_name} #{pane_current_command}"

# Ждать 30+ сек (служебный запрос B может получить client_gone)
# Запись очереди должна остаться
sqlite3 ~/.local/share/runpilot/runpilot.db \
  "SELECT sid, held_request FROM queue_entry WHERE sid='<SID_B>'"
```

### 6. Скриншоты (1 мин)
- Веб: скриншот экрана B (HELD)
- tmux: capture-pane B (строка статуса "held")

### 7. Stop A (1 мин)
```bash
# Остановить ход A (освободить слот)
curl -s -X POST -H "Authorization: Bearer $TOKEN" \
  http://100.124.155.106:8788/api/v1/sessions/<SID_A>/cancel
```

### 8. Ждать LEASE_GRANT(B) + завершение (3 мин)
```bash
# B должен получить аренду и завершить ход
# Проверить:
# - LEASE_GRANT(B) позже LEASE_RELEASE(A)
# - Ход B завершён OK
sqlite3 ~/.local/share/runpilot/runpilot.db \
  "SELECT ts, kind, payload FROM event WHERE sid IN ('<SID_A>','<SID_B>') AND kind IN ('LEASE_RELEASE','LEASE_GRANT') ORDER BY ts"
```

### 9. Финальные проверки (1 мин)
- Экран B без ошибок: `tmux capture-pane -t <tmux_session_B> -p`
- Скриншот экрана B

## Критерии PASS
- [ ] Основной запрос B один, удержан до LEASE_GRANT, без 503 и без повторов
- [ ] Служебный запрос B может завершиться client_gone на 30-й секунде; запись очереди остаётся
- [ ] LEASE_GRANT(B) позже LEASE_RELEASE(A)
- [ ] Ход B завершён OK
- [ ] Экран B без ошибок (capture-pane)
- [ ] Во время удержания — HELD в вебе (скриншот) и «held» в строке статуса tmux

## Очистка
```bash
# Закрыть сессии через API
curl -s -X POST -H "Authorization: Bearer $TOKEN" \
  http://100.124.155.106:8788/api/v1/sessions/<SID_A>/stop
curl -s -X POST -H "Authorization: Bearer $TOKEN" \
  http://100.124.155.106:8788/api/v1/sessions/<SID_B>/stop
```

## Уведомление
По окончании сообщить оператору, что vzu5 свободен.
