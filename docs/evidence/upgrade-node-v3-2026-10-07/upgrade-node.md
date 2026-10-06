# upgrade-node.sh v3 — temp-файл в каталоге целевого бинарника (п.1, 2026-10-07)

## Симптом (данные оператора, vzu5-claw)
`upgrade-node.sh` v2 упал на шаге 3/6:
```
/tmp/upgrade-node.sh: line 83: /tmp/runpilot.node.new.PSJWaM: Permission denied
```
Бинарник не изменён, узел остался на 4904a65.

## Причина
Временный файл бинарника создавался в `${TMPDIR:-/tmp}` (`mktemp "${TMPDIR:-/tmp}/..."`).
При umask, закрывающем +x (напр. 077), или при правах источника без execute-bit,
файл не исполняемый → шаг 3 `run_doctor "$TMP_BIN"` (выполнение файла) падал на
«Permission denied». Кроме того /tmp — чужой каталог/ФС: `mv -f` туда не был
гарантированно атомарным rename.

## Фикс (deploy/upgrade-node.sh)
1. Temp-файл — в каталоге ЦЕЛЕВОГО бинарника: `mktemp -p "$NODE_DIR" runpilot.node.new.XXXXXX`
   (не /tmp, не $TMPDIR). `mkdir -p "$NODE_DIR"` перед созданием.
2. Сразу после сверки SHA256 — `chmod 0755 "$TMP_BIN"` (гарантия execute-bit независимо от umask).
3. Предпроверка запускает ИМЕННО этот файл; при PASS он же ставится на место `mv -f`
   (атомарный rename в пределах одного ФС). Убрана лишняя STAGE-копия.
4. `trap EXIT` удаляет ВСЕ временные файлы (TMP_BIN, DOCTOR_OUT/POST/OLD, ROLLBACK_STAGE).

## Песочница (deploy/upgrade-node.sandbox-test.sh)
Добавлен сценарий **E** — полный PASS-проход (шаги 1–6) под `umask 077`:
- запуск под umask 077 → ИТОГ PASS (exit 0);
- исполняемый при предпроверке файл лежит в каталоге целевого бинарника, режим 0755
  (маркер пишет фейковый newbin: `/proc/self/exe` + режим, append);
- после прохода временных файлов нет (ни `runpilot.node.new.*` в каталоге целевого,
  ни `rp-doctor.*` в TMPDIR).

Результат: `ИТОГ sandbox: PASS=16 FAIL=0`.

## shellcheck (v0.10.0)
- `shellcheck deploy/upgrade-node.sh` → 0 замечаний;
- `shellcheck deploy/upgrade-node.sandbox-test.sh` → 0 замечаний.
