# П.3 — контрольный замер (PASS: без таймаута-переопределения клиент падает на ~120 c)

Stand: `HL_PROFILE=env` (launcher), но A создан с env-профилем, затем профиль на
диске переключён на noenv перед созданием B (`runpilot run` перечитывает профиль с
диска на каждый вызов — client-side, run.go). Итог: **A = env (QWEN_CODE_API_TIMEOUT_MS
= 5400000, не таймаутится), B = noenv (дефолт qwen 120000 мс)**.

Проверка environ (до теста):
- A (PAT1PF7SFY): `QWEN_CODE_API_TIMEOUT_MS=5400000` + `OPENAI_BASE_URL=.../s/PAT1PF7SFY/v1`
- B (7NGNX2EBB1): `OPENAI_BASE_URL=.../s/7NGNX2EBB1/v1`, `QWEN_CODE_API_TIMEOUT_MS` **отсутствует**

Процесс: `fakellm /_set-delays?firstByteMs=360000` → A «сидит 6 мин» (A=env, держит
слот 360 c) → `firstByteMs=0` → Enter в B → B удерживается → B's клиент (120 c) падаёт.

## Данные (БД, UTC)
A (PAT1PF7SFY) аренда: `granted_at=02:22:13.295`, `released_at=''` (активна — держит слот).
B (7NGNX2EBB1):
```
queue_entry: held_request=1, mode=submit, enqueued_at 02:22:27.363 → 02:24:27.757 (обновлено ~120 с)
event 02:22:27.695 QUEUE_SKIP {"reason":"no_up_server"}
event 02:24:28.194 QUEUE_SKIP {"reason":"no_up_server"}
request: НЕТ строки (запрос не дописан — отменён клиентом до завершения)
```
Сервер fakellm: `/health` ok, аренда A активна (слот занят) → `no_up_server` = «нет
свободного слота», а не «сервер DOWN».

## Вывод (контроль)
**П.3 PASS.** Без `QWEN_CODE_API_TIMEOUT_MS` (дефолт qwen 120000 мс) клиент B не
дожидается удержания: запрос удерживался ~120 с (enqueued_at 02:22:27 → 02:24:27), затем
клиент B таймаутится (ctx.Done → heldToResume), запрос **не завершён** (нет строки
request, нет status 200). Это зеркало п.2: там (env, 5400000 мс) тот же сценарий —
запрос удерживался ~380 с и **завершился** (held_ms=379517, status 200).

Критерий: без большого таймаута клиент не переживает удержание >120 с → нужно
`QWEN_CODE_API_TIMEOUT_MS > hold_max_sec + first_byte_timeout_sec`. Подтверждено замером.

Побочное: после таймаута qwen B повторил запрос (QUEUE_SKIP 02:24:28) — цикл
таймаут-повтор, пока слот занят. enqueued_at сбрасывается на heldToResume (см. п.5).
