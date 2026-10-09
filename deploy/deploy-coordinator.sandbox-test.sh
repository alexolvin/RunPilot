#!/usr/bin/env bash
# deploy-coordinator.sandbox-test.sh — песочные тесты deploy/deploy-coordinator.sh.
#
# Проверяет (без сети, без systemd, в изолированном каталоге):
#   A. vcs.modified=true (грязное дерево) -> скрипт ОТКАЗЫВАЕТ (exit 1,
#      целевой бинарник не тронут, печать vcs.modified=true);
#   B. vcs.modified=false (чистый HEAD)   -> PASS (атомарная замена, рестарт,
#      целевой заменён).
#
# VCS-stamping: фейковый Go-модуль в temp git-репо; cleanbin — из чистого
# коммита (vcs.modified=false), dirtybin — после незакоммиченного изменения
# (vcs.modified=true).
#
# Запуск:  bash deploy/deploy-coordinator.sandbox-test.sh
# Требование: go, git, sha256sum.

set -u

HERE="$(cd "$(dirname "$0")" && pwd)"
SCRIPT="${HERE}/deploy-coordinator.sh"
[ -f "$SCRIPT" ] || { echo "нет $SCRIPT" >&2; exit 2; }
command -v go >/dev/null 2>&1 || { echo "нет go" >&2; exit 2; }
command -v git >/dev/null 2>&1 || { echo "нет git" >&2; exit 2; }

SB="$(mktemp -d /tmp/rp-coord-sb.XXXXXX)"
trap 'rm -rf "$SB"' EXIT

# --- фейковый Go-модуль + git-репо для VCS-stamping ---
mkdir -p "$SB/repo"
cat > "$SB/repo/go.mod" <<'EOF'
module fakecoord
go 1.21
EOF
cat > "$SB/repo/main.go" <<'GO'
package main

import (
	"fmt"
	"os"
	"time"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "doctor":
			fmt.Println("PASS coordinator doctor")
			return
		case "version":
			fmt.Println("fake coordinator")
			return
		}
	}
	for {
		time.Sleep(time.Second)
	}
}
GO
(
  cd "$SB/repo"
  git init -q
  git config user.email t@t
  git config user.name t
  git add -A
  git commit -qm init
  CGO_ENABLED=0 go build -o cleanbin .
) || { echo "не удалось собрать cleanbin" >&2; exit 2; }
# Грязное дерево: незакоммиченное изменение -> vcs.modified=true.
echo "// dirty" >> "$SB/repo/main.go"
( cd "$SB/repo" && CGO_ENABLED=0 go build -o dirtybin . ) \
  || { echo "не удалось собрать dirtybin" >&2; exit 2; }

COORDDIR="$SB/coordinatedir"
mkdir -p "$COORDDIR"
COORD_BIN="$COORDDIR/runpilot"
# Начальный бинарник — dirtybin (иной SHA), чтобы deploy cleanbin менял SHA.
cp -a "$SB/repo/dirtybin" "$COORD_BIN"

PASS=0; FAIL=0
ok()  { printf '  PASS: %s\n' "$1"; PASS=$((PASS+1)); }
bad() { printf '  FAIL: %s\n' "$1"; FAIL=$((FAIL+1)); }
sha() { sha256sum "$1" | awk '{print $1}'; }

run_deploy() {
  env -i PATH="$PATH" HOME="$SB" TMPDIR="$SB" \
    COORD_BIN="$COORD_BIN" COORD_SERVICE=fake \
    RESTART_CMD='true' IS_ACTIVE_CMD='true' \
    bash "$SCRIPT" "$1" 2>&1
  return $?
}

echo "== A. vcs.modified=true -> отказ (бинарник не тронут) =="
A_BEFORE="$(sha "$COORD_BIN")"
out_a="$(run_deploy "$SB/repo/dirtybin")"
rc=$?
A_AFTER="$(sha "$COORD_BIN")"
[ "$rc" -eq 1 ] && ok "скрипт: отказ (exit 1)" || bad "скрипт exit=$rc (ожидался 1)"
[ "$A_AFTER" = "$A_BEFORE" ] && ok "целевой не изменён (sha до==после)" || bad "целевой ИЗМЕНИЛСЯ (vcs.modified=true поставлен!)"
printf '%s\n' "$out_a" | grep -q 'vcs.modified=true' && ok "печать vcs.modified=true" || bad "нет печати vcs.modified=true"
printf '%s\n' "$out_a" | grep -q 'FAIL' && ok "печать FAIL" || bad "нет печати FAIL"

echo "== B. vcs.modified=false -> PASS (замена + рестарт) =="
B_BEFORE="$(sha "$COORD_BIN")"
out_b="$(run_deploy "$SB/repo/cleanbin")"
rc=$?
B_AFTER="$(sha "$COORD_BIN")"
[ "$rc" -eq 0 ] && ok "скрипт: PASS (exit 0)" || bad "скрипт exit=$rc (ожидался 0): $(printf '%s' "$out_b" | tail -n3 | tr '\n' ' ')"
[ "$B_AFTER" != "$B_BEFORE" ] && ok "целевой заменён (sha)" || bad "целевой НЕ заменён"
printf '%s\n' "$out_b" | grep -q 'ИТОГ: PASS' && ok "печать «ИТОГ: PASS»" || bad "нет печати «ИТОГ: PASS»"

echo "== C. dist_dir/runpilot = бинарник координатора (W12, ТЗ 14) =="
DIST_BIN="$SB/.local/share/runpilot/dist/runpilot"
[ -f "$DIST_BIN" ] && ok "dist_dir/runpilot создан" || bad "dist_dir/runpilot отсутствует"
if [ -f "$DIST_BIN" ]; then
  [ "$(sha "$DIST_BIN")" = "$(sha "$COORD_BIN")" ] \
    && ok "dist sha256 = координатор sha256" \
    || bad "dist sha256 != координатор sha256"
  # версионная защита: dist отчитывается той же версией, что и координатор.
  [ "$("$DIST_BIN" version)" = "$("$COORD_BIN" version)" ] \
    && ok "dist version = координатор version" \
    || bad "dist version != координатор version"
fi

echo
printf 'ИТОГ sandbox: PASS=%d FAIL=%d\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ] && exit 0 || exit 1
