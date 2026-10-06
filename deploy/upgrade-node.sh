#!/usr/bin/env bash
# upgrade-node.sh — обновить runpilot-бинарник УЗЛА с координатора и перезапустить
# node-службу. Запускается ОДНОЙ командой на машине узла (например vzu5-claw).
#
# Использование (на узле):
#   upgrade-node.sh <EXPECTED_SHA256> [COORD_HOST]
#
#   <EXPECTED_SHA256>  SHA256 бинарника нужного коммита, проверенный оператором на
#                      координаторе:  sha256sum /home/user/.local/bin/runpilot
#                      (на 3p14-aaa). ОБЯЗАТЕЛЕН: бинарник устанавливается ТОЛЬКО
#                      при совпадении SHA256 (иначе — FAIL, работающий бинарник не
#                      трогается). Можно передать и в формате sha256sum («<hex>  путь»).
#   [COORD_HOST]       хост координатора (по умолчанию 3p14-aaa; tailscale).
#
# Переменные окружения (опционально, переопределяют пути):
#   COORD_BIN   бинарник на координаторе (по умолчанию /home/user/.local/bin/runpilot)
#   NODE_BIN    бинарник узла (по умолчанию $HOME/.local/bin/runpilot)
#   NODE_SERVICE имя user-сервиса узла (по умолчанию runpilot-node)
#   NODE_ENV    файл env узла для doctor (по умолчанию $HOME/.config/runpilot/env)
#
# Шаги:
#   1. scp бинарника с координатора (tailscale) во временный файл;
#   2. сверка SHA256 с EXPECTED_SHA256 (не совпало → FAIL, бинарник НЕ трогается);
#   3. бэкап текущего бинарника + замена;
#   4. перезапуск node-службы;
#   5. runpilot doctor — при FAIL: откат на бэкап + перезапуск;
#   6. итог: PASS/FAIL, версия (vcs.revision), SHA256.

set -u

COORD_HOST="${2:-3p14-aaa}"
COORD_BIN="${COORD_BIN:-/home/user/.local/bin/runpilot}"
NODE_BIN="${NODE_BIN:-$HOME/.local/bin/runpilot}"
NODE_SERVICE="${NODE_SERVICE:-runpilot-node}"
NODE_ENV="${NODE_ENV:-$HOME/.config/runpilot/env}"
EXPECTED_SHA="${1:-}"

TMP_BIN=""
BACKUP=""
DOCTOR_OUT=""
cleanup() { [ -n "$TMP_BIN" ] && rm -f "$TMP_BIN"; [ -n "$DOCTOR_OUT" ] && rm -f "$DOCTOR_OUT"; }
trap cleanup EXIT
fail() { echo "FAIL: $*" >&2; exit 1; }

command -v scp >/dev/null 2>&1 || fail "scp не найден"
command -v sha256sum >/dev/null 2>&1 || fail "sha256sum не найден"
[ -n "$EXPECTED_SHA" ] || fail "укажите EXPECTED_SHA256 (1-й аргумент)"
# нормализуем sha: берём первое поле (hex), нижний регистр.
EXPECTED_SHA="$(printf '%s\n' "$EXPECTED_SHA" | awk '{print $1}' | tr '[:upper:]' '[:lower:]')"
case "$EXPECTED_SHA" in
  *[!0-9a-f]*) fail "ожидался SHA256-hex, получен: $EXPECTED_SHA" ;;
esac

echo "==> 1/5 скачивание: ${COORD_HOST}:${COORD_BIN}"
TMP_BIN="$(mktemp /tmp/runpilot.node.new.XXXXXX)"
scp -q "${COORD_HOST}:${COORD_BIN}" "$TMP_BIN" || fail "scp с координатора не удался"

echo "==> 2/5 сверка SHA256"
ACTUAL_SHA="$(sha256sum "$TMP_BIN" | awk '{print $1}')"
echo "    ожидаемо: $EXPECTED_SHA"
echo "    фактично: $ACTUAL_SHA"
if [ "$ACTUAL_SHA" != "$EXPECTED_SHA" ]; then
  fail "SHA256 НЕ СОВПАДАЕТ — бинарник не установлен (узел не изменён)"
fi

echo "==> 3/5 бэкап + замена: $NODE_BIN"
if [ -f "$NODE_BIN" ]; then
  BACKUP="${NODE_BIN}.bak-$(date -u +%Y%m%dT%H%M%SZ)"
  cp -a "$NODE_BIN" "$BACKUP" || fail "не удалось создать бэкап $BACKUP"
  echo "    бэкап: $BACKUP"
else
  echo "    (прежнего бинарника нет — бэкап не нужен)"
fi
install -m 755 "$TMP_BIN" "$NODE_BIN" || {
  [ -n "$BACKUP" ] && cp -a "$BACKUP" "$NODE_BIN"
  fail "установка бинарника не удалась (откат выполнен)"
}
rm -f "$TMP_BIN"; TMP_BIN=""

# версия: vcs.revision из штампа (если есть go), иначе runpilot version, иначе unknown
VERSION="$(go version -m "$NODE_BIN" 2>/dev/null | awk -F'vcs.revision=' '/vcs.revision=/{print $2; exit}' || true)"
[ -n "${VERSION:-}" ] || VERSION="$("$NODE_BIN" version 2>/dev/null | head -1 || true)"
[ -n "${VERSION:-}" ] || VERSION="unknown"
echo "    версия: $VERSION"

echo "==> 4/5 рестарт: $NODE_SERVICE"
systemctl --user restart "$NODE_SERVICE" || fail "рестарт $NODE_SERVICE не удался"
sleep 3
systemctl --user is-active "$NODE_SERVICE" >/dev/null 2>&1 \
  || fail "$NODE_SERVICE не active после рестарта"
echo "    $NODE_SERVICE: active"

echo "==> 5/5 runpilot doctor"
# doctor берёт конфиг + секреты из env узла (RUNPILOT_NODE_TOKEN и т.п.)
[ -f "$NODE_ENV" ] && { set -a; . "$NODE_ENV"; set +a; }
DOCTOR_OUT="$(mktemp /tmp/rp-doctor.out.XXXXXX)"
if "$NODE_BIN" doctor >"$DOCTOR_OUT" 2>&1; then
  DOCTOR=PASS
else
  DOCTOR=FAIL
  echo "---- doctor ----" >&2; cat "$DOCTOR_OUT" >&2
fi

if [ "$DOCTOR" = "PASS" ]; then
  echo
  echo "=================================================="
  echo "ИТОГ: PASS"
  echo "  версия   : $VERSION"
  echo "  SHA256   : $ACTUAL_SHA"
  echo "  бинарник : $NODE_BIN"
  [ -n "$BACKUP" ] && echo "  бэкап    : $BACKUP"
  echo "=================================================="
  exit 0
else
  echo >&2
  echo "doctor FAIL — откат на бэкап + перезапуск" >&2
  if [ -n "$BACKUP" ]; then
    cp -a "$BACKUP" "$NODE_BIN" && systemctl --user restart "$NODE_SERVICE" \
      && echo "  откат выполнен: $BACKUP -> $NODE_BIN, $NODE_SERVICE перезапущен" >&2 \
      || echo "  ОШИБКА отката — восстановите $NODE_BIN вручную из $BACKUP" >&2
  else
    echo "  бэкап отсутствует — восстановите $NODE_BIN вручную" >&2
  fi
  echo "ИТОГ: FAIL (doctor); SHA256=$ACTUAL_SHA; версия=$VERSION" >&2
  exit 1
fi
