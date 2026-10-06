#!/usr/bin/env bash
# upgrade-node.sh — обновить runpilot-бинарник УЗЛА с координатора (3p14-aaa)
# и перезапустить node-службу. Запускается ОДНОЙ командой на машине узла.
#
# Использование (на узле):
#   upgrade-node.sh <EXPECTED_SHA256> [COORD_HOST]
#
#   <EXPECTED_SHA256>  SHA256 бинарника нужного коммита, проверенный оператором на
#                      координаторе:  sha256sum /home/user/.local/bin/runpilot
#                      (на 3p14-aaa). ОБЯЗАТЕЛЕН: бинарник ставится ТОЛЬКО при
#                      совпадении (иначе — FAIL, работающий узел не трогается).
#                      Допускается формат sha256sum («<hex>  путь»).
#   [COORD_HOST]       хост координатора (по умолчанию 3p14-aaa; tailscale).
#
# Переменные окружения (опционально, переопределяют пути/поведение):
#   COORD_BIN      бинарник на координаторе (по умолчанию /home/user/.local/bin/runpilot)
#   NODE_BIN       бинарник узла (по умолчанию $HOME/.local/bin/runpilot)
#   NODE_SERVICE   имя user-сервиса узла (по умолчанию runpilot-node)
#   NODE_ENV       файл env узла для doctor (по умолчанию $HOME/.config/runpilot/env)
#   LOCAL_BIN      [test-hook] если задан — «скачанный» бинарник берётся cp из
#                  этого локального пути вместо scp (для песочницы, без сети)
#   RESTART_CMD    [test-hook] если задан — рестарт службы = `bash -c RESTART_CMD`
#                  (вместо `systemctl --user restart`); для песочницы
#   IS_ACTIVE_CMD  [test-hook] если задан — проверка активности = `bash -c IS_ACTIVE_CMD`
#                  (вместо `systemctl --user is-active`)
#
# Безопасность:
#   1. SHA256: бинарник не ставится при несовпадении (рабочий узел не трогается).
#   2. Временный файл бинарника — в каталоге ЦЕЛЕВОГО (mktemp -p), НЕ в /tmp:
#      сразу после сверки SHA256 получает 0755, ПРЕДПРОВЕРКА запускает именно
#      его, и при PASS он же ставится на место через `mv -f` (атомарный rename
#      в пределах одного ФС). Запись поверх целевого (и тем более запущенного)
#      файла НЕ делается (иначе ETXTBSY / «Text file busy»). Trap EXIT удаляет
#      временные файлы.
#   3. После рестарта doctor FAIL => атомарный откат (mv -f), рестарт, doctor
#      старым бинарником, печать обоих результатов.
#
# Итог: PASS/FAIL, версия (vcs.revision), SHA256.

set -u

COORD_HOST="${2:-3p14-aaa}"
COORD_BIN="${COORD_BIN:-/home/user/.local/bin/runpilot}"
NODE_BIN="${NODE_BIN:-$HOME/.local/bin/runpilot}"
NODE_SERVICE="${NODE_SERVICE:-runpilot-node}"
NODE_ENV="${NODE_ENV:-$HOME/.config/runpilot/env}"
EXPECTED_SHA="${1:-}"
LOCAL_BIN="${LOCAL_BIN:-}"
RESTART_CMD="${RESTART_CMD:-}"
IS_ACTIVE_CMD="${IS_ACTIVE_CMD:-}"

NODE_DIR="$(dirname "$NODE_BIN")"
TMP_BIN=""
BACKUP=""
DOCTOR_OUT=""
DOCTOR_POST=""
DOCTOR_OLD=""
ROLLBACK_STAGE=""
# вызывается только через trap EXIT (SC2317 — ложное срабатывание)
# shellcheck disable=SC2317
cleanup() {
  [ -n "$TMP_BIN" ] && rm -f "$TMP_BIN"
  [ -n "$DOCTOR_OUT" ] && rm -f "$DOCTOR_OUT"
  [ -n "$DOCTOR_POST" ] && rm -f "$DOCTOR_POST"
  [ -n "$DOCTOR_OLD" ] && rm -f "$DOCTOR_OLD"
  [ -n "$ROLLBACK_STAGE" ] && rm -f "$ROLLBACK_STAGE"
}
trap cleanup EXIT
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }

command -v sha256sum >/dev/null 2>&1 || fail "sha256sum не найден"
[ -n "$EXPECTED_SHA" ] || fail "укажите EXPECTED_SHA256 (1-й аргумент)"
# нормализуем sha: первое поле (hex), нижний регистр
EXPECTED_SHA="$(printf '%s\n' "$EXPECTED_SHA" | awk '{print $1}' | tr '[:upper:]' '[:lower:]')"
case "$EXPECTED_SHA" in
  *[!0-9a-f]*) fail "ожидался SHA256-hex, получен: $EXPECTED_SHA" ;;
esac

# doctor берёт конфиг + секреты из env узла (RUNPILOT_NODE_TOKEN и т.п.)
if [ -f "$NODE_ENV" ]; then
  set -a
  # shellcheck disable=SC1090 # динамический путь env-файла
  . "$NODE_ENV"
  set +a
fi

# run_doctor <бинарник> <файл-выход> -> 0=PASS, 1=FAIL
run_doctor() { "$1" doctor >"$2" 2>&1; }

# restart_node — перезапуск службы (systemctl по умолчанию, RESTART_CMD — хук)
restart_node() {
  if [ -n "$RESTART_CMD" ]; then
    # shellcheck disable=SC2086 # команда-хук, намеренно без кавычек
    bash -c "$RESTART_CMD"
  else
    systemctl --user restart "$NODE_SERVICE"
  fi
}
# is_active_node — 0, если служба active
is_active_node() {
  if [ -n "$IS_ACTIVE_CMD" ]; then
    # shellcheck disable=SC2086
    bash -c "$IS_ACTIVE_CMD"
  else
    systemctl --user is-active "$NODE_SERVICE" >/dev/null 2>&1
  fi
}

echo "==> 1/6 получение бинарника"
# Временный файл — в каталоге ЦЕЛЕВОГО бинарника (НЕ в /tmp / $TMPDIR):
# (а) mv -f на место — атомарный rename в пределах одного файловой системы;
# (б) предпроверка запускает именно этот файл, поэтому он должен лежать рядом
#     с целевым и стать исполняемым сразу после сверки SHA256.
mkdir -p "$NODE_DIR" || fail "каталог целевого бинарника $NODE_DIR не создан"
TMP_BIN="$(mktemp -p "$NODE_DIR" runpilot.node.new.XXXXXX)" \
  || fail "не удалось создать временный файл в каталоге $NODE_DIR"
if [ -n "$LOCAL_BIN" ]; then
  cp -a "$LOCAL_BIN" "$TMP_BIN" || fail "не удалось скопировать LOCAL_BIN $LOCAL_BIN"
else
  command -v scp >/dev/null 2>&1 || fail "scp не найден"
  scp -q "${COORD_HOST}:${COORD_BIN}" "$TMP_BIN" || fail "scp с ${COORD_HOST} не удался"
fi

echo "==> 2/6 сверка SHA256"
ACTUAL_SHA="$(sha256sum "$TMP_BIN" | awk '{print $1}')"
printf '    ожидаемо: %s\n' "$EXPECTED_SHA"
printf '    фактично: %s\n' "$ACTUAL_SHA"
[ "$ACTUAL_SHA" = "$EXPECTED_SHA" ] \
  || fail "SHA256 НЕ СОВПАДАЕТ — бинарник не установлен (узел не изменён)"
# Сразу после сверки SHA256 делаем файл исполняемым: umask (например 077) или
# сохранённые права источника могут не давать +x, а предпроверка ниже запускает
# этот файл напрямую — без 0755 шаг 3 падает на «Permission denied».
chmod 0755 "$TMP_BIN" || fail "chmod 0755 $TMP_BIN не удался"

echo "==> 3/6 предпроверка: doctor новым бинарником (до замены)"
DOCTOR_OUT="$(mktemp "${TMPDIR:-/tmp}/rp-doctor.new.XXXXXX")"
if run_doctor "$TMP_BIN" "$DOCTOR_OUT"; then
  echo "    PASS"
else
  printf -- '---- doctor (новый бинарник, до замены) ----\n' >&2
  cat "$DOCTOR_OUT" >&2
  fail "предпроверка FAIL — целевой бинарник НЕ изменён"
fi

echo "==> 4/6 бэкап + атомарная замена: $NODE_BIN"
if [ -f "$NODE_BIN" ]; then
  BACKUP="${NODE_DIR}/runpilot.bak-$(date -u +%Y%m%dT%H%M%SZ)"
  cp -a "$NODE_BIN" "$BACKUP" || fail "не удалось создать бэкап $BACKUP"
  printf '    бэкап: %s\n' "$BACKUP"
else
  echo "    (прежнего бинарника нет — бэкап не нужен)"
fi
# Атомарная замена: TMP_BIN уже лежит в каталоге целевого бинарника и имеет 0755
# (chmod после сверки SHA256) — ставим ИМЕННО ЕГО на место атомарным rename
# (mv -f). Запись поверх целевого не делаем — иначе ETXTBSY, если бинарник
# запущен. mv атомарен: при неудаче целевой бинарник не изменён, откат не нужен.
mv -f "$TMP_BIN" "$NODE_BIN" \
  || fail "атомарная замена (mv -f) не удалась (целевой бинарник не изменён)"
TMP_BIN=""

VERSION="$(go version -m "$NODE_BIN" 2>/dev/null | awk -F'vcs.revision=' '/vcs.revision=/{print $2; exit}' || true)"
[ -n "${VERSION:-}" ] || VERSION="$("$NODE_BIN" version 2>/dev/null | head -n1 || true)"
[ -n "${VERSION:-}" ] || VERSION="unknown"
printf '    версия: %s\n' "$VERSION"

echo "==> 5/6 рестарт: $NODE_SERVICE"
restart_node || fail "рестарт $NODE_SERVICE не удался"
sleep 3
is_active_node || fail "$NODE_SERVICE не active после рестарта"
printf '    %s: active\n' "$NODE_SERVICE"

echo "==> 6/6 doctor после рестарта"
DOCTOR_POST="$(mktemp "${TMPDIR:-/tmp}/rp-doctor.post.XXXXXX")"
if run_doctor "$NODE_BIN" "$DOCTOR_POST"; then
  printf '\n==================================================\n'
  printf 'ИТОГ: PASS\n'
  printf '  версия   : %s\n' "$VERSION"
  printf '  SHA256   : %s\n' "$ACTUAL_SHA"
  printf '  бинарник : %s\n' "$NODE_BIN"
  [ -n "$BACKUP" ] && printf '  бэкап    : %s\n' "$BACKUP"
  printf '==================================================\n'
  exit 0
fi

# doctor FAIL после рестарта — атомарный откат + рестарт + doctor старым
printf '\ndoctor FAIL после рестарта — атомарный откат + перезапуск\n' >&2
cat "$DOCTOR_POST" >&2
if [ -n "$BACKUP" ]; then
  ROLLBACK_STAGE="${NODE_DIR}/.runpilot.rollback.$$"
  if cp -a "$BACKUP" "$ROLLBACK_STAGE" && mv -f "$ROLLBACK_STAGE" "$NODE_BIN"; then
    restart_node
    sleep 3
    DOCTOR_OLD="$(mktemp "${TMPDIR:-/tmp}/rp-doctor.old.XXXXXX")"
    if run_doctor "$NODE_BIN" "$DOCTOR_OLD"; then OLD_DOCTOR=PASS; else OLD_DOCTOR=FAIL; fi
    printf '  откат выполнен (mv -f): %s -> %s; %s перезапущен\n' "$BACKUP" "$NODE_BIN" "$NODE_SERVICE" >&2
    printf '  doctor после отката (старый бинарник): %s\n' "$OLD_DOCTOR" >&2
  else
    printf '  ОШИБКА отката (mv -f) — восстановите %s вручную из %s\n' "$NODE_BIN" "$BACKUP" >&2
  fi
else
  printf '  бэкап отсутствует — восстановите %s вручную\n' "$NODE_BIN" >&2
fi
printf 'ИТОГ: FAIL (doctor после рестарта); SHA256=%s; версия=%s\n' "$ACTUAL_SHA" "$VERSION" >&2
exit 1
