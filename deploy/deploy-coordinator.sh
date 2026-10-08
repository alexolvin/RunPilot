#!/usr/bin/env bash
# deploy-coordinator.sh — выкат КООРДИНАТОРА: проверка vcs.modified, бэкап,
# атомарная замена (mv -f, ETXTBSY-безопасно), рестарт, проверка.
#
# ОТКАЗЫВАЕТ, если бинарник собран из грязного дерева (vcs.modified=true) —
# выкат только из чистого HEAD (версия без суффикса «-dirty»).
#
# Использование:
#   deploy-coordinator.sh <NEW_BIN>
#
# Аргументы:
#   NEW_BIN  путь к новому бинарнику (собран из чистого HEAD, vcs.modified=false).
#
# Env:
#   COORD_BIN     бинарник координатора (по умолчанию /home/user/.local/bin/runpilot)
#   COORD_SERVICE user-сервис координатора (по умолчанию runpilot)
#   RESTART_CMD   [test-hook] рестарт = `bash -c RESTART_CMD` (вместо systemctl)
#   IS_ACTIVE_CMD [test-hook] проверка активности = `bash -c IS_ACTIVE_CMD`
#
# Безопасность:
#   1. vcs.modified=true -> FAIL, бинарник не трогается.
#   2. Атомарная замена: `cp -a NEW_BIN <dest>.new.$$ && chmod 755 && mv -f`
#      (temp в каталоге целевого, rename в пределах одного ФС; запись поверх
#      запущенного ELF не делается — ETXTBSY).
#   3. Бэкап до замены: runpilot.bak-<ts>.

set -u

NEW_BIN="${1:-}"
COORD_BIN="${COORD_BIN:-/home/user/.local/bin/runpilot}"
COORD_SERVICE="${COORD_SERVICE:-runpilot}"
RESTART_CMD="${RESTART_CMD:-}"
IS_ACTIVE_CMD="${IS_ACTIVE_CMD:-}"
STAGE=""

# вызывается только через trap EXIT (SC2317 — ложное срабатывание)
# shellcheck disable=SC2317
cleanup() { [ -n "$STAGE" ] && rm -f "$STAGE"; }
trap cleanup EXIT
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }

[ -n "$NEW_BIN" ] || fail "укажите путь к новому бинарнику (1-й аргумент)"
[ -f "$NEW_BIN" ] || fail "бинарник $NEW_BIN не найден"
command -v go >/dev/null 2>&1 || fail "go не найден"
command -v sha256sum >/dev/null 2>&1 || fail "sha256sum не найден"

# --- 1/4 проверка vcs.modified: выкат только из чистого HEAD ---
VCS_REV="$(go version -m "$NEW_BIN" 2>/dev/null | awk -F'vcs.revision=' '/vcs.revision=/{print $2; exit}' || true)"
VCS_MOD="$(go version -m "$NEW_BIN" 2>/dev/null | awk -F'vcs.modified=' '/vcs.modified=/{print $2; exit}' || true)"
printf 'vcs.revision: %s\n' "${VCS_REV:-unknown}"
printf 'vcs.modified: %s\n' "${VCS_MOD:-unknown}"
[ "$VCS_MOD" != "true" ] || fail "vcs.modified=true — бинарник из грязного дерева; выкат только из чистого HEAD (закоммить/состейджить и пересобрать)"

# --- 2/4 бэкап ---
COORD_DIR="$(dirname "$COORD_BIN")"
BACKUP=""
if [ -f "$COORD_BIN" ]; then
  BACKUP="${COORD_DIR}/runpilot.bak-$(date -u +%Y%m%dT%H%M%SZ)"
  cp -a "$COORD_BIN" "$BACKUP" || fail "не удалось создать бэкап $BACKUP"
  printf 'бэкап: %s\n' "$BACKUP"
else
  echo "(прежнего бинарника нет — бэкап не нужен)"
fi

# --- 3/4 атомарная замена (mv -f, без ETXTBSY) ---
STAGE="${COORD_BIN}.new.$$"
cp -a "$NEW_BIN" "$STAGE" || fail "cp -a $NEW_BIN $STAGE не удался"
chmod 755 "$STAGE" || fail "chmod 755 $STAGE не удался"
mv -f "$STAGE" "$COORD_BIN" || fail "mv -f не удался (целевой не изменён)"
STAGE=""
printf 'бинарник: %s\n' "$(sha256sum "$COORD_BIN" | awk '{print $1}')"

# --- 4/4 рестарт + проверка активности ---
if [ -n "$RESTART_CMD" ]; then
  # shellcheck disable=SC2086 # команда-хук, намеренно без кавычек
  bash -c "$RESTART_CMD" || fail "рестарт не удался"
else
  systemctl --user restart "$COORD_SERVICE" || fail "рестарт $COORD_SERVICE не удался"
fi
sleep 3
if [ -n "$IS_ACTIVE_CMD" ]; then
  # shellcheck disable=SC2086 # команда-хук, намеренно без кавычек
  bash -c "$IS_ACTIVE_CMD" || fail "$COORD_SERVICE не active после рестарта"
else
  systemctl --user is-active "$COORD_SERVICE" >/dev/null 2>&1 || fail "$COORD_SERVICE не active после рестарта"
fi

RUN_REV="$(go version -m "$COORD_BIN" 2>/dev/null | awk -F'vcs.revision=' '/vcs.revision=/{print $2; exit}' || true)"
printf '\n==================================================\n'
printf 'ИТОГ: PASS\n'
printf '  vcs.revision: %s\n' "${RUN_REV:-unknown}"
printf '  бинарник    : %s\n' "$COORD_BIN"
[ -n "$BACKUP" ] && printf '  бэкап       : %s\n' "$BACKUP"
printf '==================================================\n'
exit 0
