#!/usr/bin/env bash
# upgrade-node.sandbox-test.sh — песочные тесты deploy/upgrade-node.sh.
#
# Проверяют (без сети, без systemd, в изолированном каталоге):
#   A. целевой бинарник запущен как процесс (ELF держит файл) → атомарная замена
#      (mv -f) и откат (mv -f) успешны; запущенный процесс не затронут;
#   B. демо: plain `cp` (запись поверх) на запущенный ELF -> ETXTBSY
#      («Text file busy») — причина бага старого отката; `mv -f` -> успех (фикс);
#   C. откат: doctor новым бинарником после «рестарта» FAIL -> атомарный откат,
#      doctor старым бинарником PASS, целевой снова старый;
#   D. предпроверка FAIL (doctor новым ДО замены) -> целевой бинарник НЕ изменён
#      (SHA256 до и после совпадает).
#
# Запуск:  bash deploy/upgrade-node.sandbox-test.sh
# Требование: go (собирает мелкие фейковые ELF), sha256sum.

# Тестовый идиом `условие && ok ... || bad ...`: ok/bad — несгораемые счётчики,
# SC2015 (A&&B||C) здесь не влияет на результат.
# shellcheck disable=SC2015

set -u

HERE="$(cd "$(dirname "$0")" && pwd)"
SCRIPT="${HERE}/upgrade-node.sh"
[ -f "$SCRIPT" ] || { echo "нет $SCRIPT" >&2; exit 2; }
command -v go >/dev/null 2>&1 || { echo "нет go" >&2; exit 2; }

SB="$(mktemp -d /tmp/rp-node-sb.XXXXXX)"
trap 'rm -rf "$SB"' EXIT

# --- фейковые ELF: oldbin (doctor всегда PASS) и newbin (doctor: FAIL при
# --- FAIL_DOCTOR или при существовании POST_FAIL_FILE) ---
cat > "$SB/old.go" <<'GO'
package main
import ("fmt"; "os"; "time")
func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "doctor":
			fmt.Println("PASS old doctor")
			return
		case "version":
			fmt.Println("old (fake)")
			return
		}
	}
	for { time.Sleep(time.Second) }
}
GO
cat > "$SB/new.go" <<'GO'
package main
import ("fmt"; "os"; "time")
func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "doctor":
			// Метка для песочницы (APPEND): путь к исполняемому файлу + режим.
			// Записывается и в предпроверке (шаг 3, exe=.../runpilot.node.new.*),
			// и после рестарта (шаг 6, exe=целевой). Позволяет проверить, что
			// предпроверка выполнила файл в каталоге целевого бинарника, 0755.
			if pf := os.Getenv("PRECHECK_INFO_FILE"); pf != "" {
				if exe, err := os.Readlink("/proc/self/exe"); err == nil {
					if fi, err2 := os.Stat(exe); err2 == nil {
						if fh, err3 := os.OpenFile(pf, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err3 == nil {
							fmt.Fprintf(fh, "exe=%s\nmode=%o\n", exe, fi.Mode().Perm())
							fh.Close()
						}
					}
				}
			}
			if os.Getenv("FAIL_DOCTOR") != "" {
				fmt.Fprintln(os.Stderr, "FAIL new doctor (FAIL_DOCTOR)")
				os.Exit(1)
			}
			if f := os.Getenv("POST_FAIL_FILE"); f != "" {
				if _, err := os.Stat(f); err == nil {
					fmt.Fprintln(os.Stderr, "FAIL new doctor (POST_FAIL)")
					os.Exit(1)
				}
			}
			fmt.Println("PASS new doctor")
			return
		case "version":
			fmt.Println("new (fake)")
			return
		}
	}
	for { time.Sleep(time.Second) }
}
GO
( cd "$SB" && CGO_ENABLED=0 go build -o oldbin old.go && CGO_ENABLED=0 go build -o newbin new.go ) \
  || { echo "не удалось собрать фейковые бинарники" >&2; exit 2; }

NODEDIR="$SB/nodedir"; mkdir -p "$NODEDIR"
NODE_BIN="$NODEDIR/runpilot"
POST_FAIL_FILE="$SB/post-fail.flag"
PRECHECK_INFO="$SB/precheck-info.txt"
NOENV="$SB/no-such-env"

PASS=0; FAIL=0
ok()  { printf '  PASS: %s\n' "$1"; PASS=$((PASS+1)); }
bad() { printf '  FAIL: %s\n' "$1"; FAIL=$((FAIL+1)); }
sha() { sha256sum "$1" | awk '{print $1}'; }

# run_upgrade <expected_sha> <local_bin> <extra-env...>
# запускает скрипт в песочнице, возвращает его exit, печатает вывод
run_upgrade() {
  local exp="$1"; local lb="$2"; shift 2
  env -i PATH="$PATH" HOME="$SB" TMPDIR="$SB" \
    LOCAL_BIN="$lb" NODE_BIN="$NODE_BIN" NODE_SERVICE=fake \
    RESTART_CMD='true' IS_ACTIVE_CMD='true' NODE_ENV="$NOENV" \
    "$@" bash "$SCRIPT" "$exp" 2>&1
  return $?
}

echo "== A. замена при запущенном целевом бинарнике (mv -f, без ETXTBSY) =="
cp -a "$SB/oldbin" "$NODE_BIN"
"$NODE_BIN" & OLD_PID=$!
sleep 0.4
NEW_SHA="$(sha "$SB/newbin")"
run_upgrade "$NEW_SHA" "$SB/newbin" >/dev/null
rc=$?
AFTER="$(sha "$NODE_BIN")"
[ "$rc" -eq 0 ] && ok "скрипт: ИТОГ PASS (exit 0)" || bad "скрипт exit=$rc"
[ "$AFTER" = "$NEW_SHA" ] && ok "целевой заменён (sha=$AFTER)" || bad "целевой НЕ заменён (sha=$AFTER)"
kill -0 "$OLD_PID" 2>/dev/null && ok "старый процесс жив (PID $OLD_PID)" || bad "старый процесс умер"
kill "$OLD_PID" 2>/dev/null; wait "$OLD_PID" 2>/dev/null

echo "== B. демо: plain cp (запись поверх — ст. откат) vs mv -f на запущенном ELF =="
# plain `cp` (без -f) поверх запущенного ELF -> ETXTBSY («Text file busy») —
# именно это делал старый откат; `cp -f` обходит (unlink+create), `mv -f` — атомарно.
"$NODE_BIN" >/dev/null 2>&1 & P2=$!
sleep 0.6
if cp "$SB/newbin" "$NODE_BIN" 2>"$SB/et.err"; then
  bad "cp поверх запущенного НЕ упал (неожиданно)"
else
  if grep -q 'Text file busy' "$SB/et.err"; then
    ok "cp -> ETXTBSY (Text file busy) — подтверждена причина бага (ст. откат)"
  else
    ok "cp упал: $(head -n1 "$SB/et.err")"
  fi
fi
ST="$NODEDIR/.stage.$$"; cp -a "$SB/newbin" "$ST"
if mv -f "$ST" "$NODE_BIN"; then ok "mv -f (атомарно) успешен"; else bad "mv -f не удался"; fi
kill "$P2" 2>/dev/null; wait "$P2" 2>/dev/null

echo "== C. откат: doctor после рестарта FAIL -> атомарный откат -> doctor старый PASS =="
rm -f "$POST_FAIL_FILE"
cp -a "$SB/oldbin" "$NODE_BIN"
"$NODE_BIN" & OLD2=$!
sleep 0.4
BEFORE_C="$(sha "$NODE_BIN")"
run_upgrade "$NEW_SHA" "$SB/newbin" POST_FAIL_FILE="$POST_FAIL_FILE" \
  RESTART_CMD="touch $POST_FAIL_FILE" >/dev/null 2>&1
rc=$?
AFTER_C="$(sha "$NODE_BIN")"
[ "$rc" -eq 1 ] && ok "скрипт: ИТОГ FAIL (exit 1, как ожидается)" || bad "скрипт exit=$rc (ожидался 1)"
[ "$AFTER_C" = "$BEFORE_C" ] && ok "откат: целевой снова старый (sha=$AFTER_C)" || bad "откат не восстановил старый (sha=$AFTER_C)"
kill "$OLD2" 2>/dev/null; wait "$OLD2" 2>/dev/null

echo "== D. предпроверка FAIL -> целевой НЕ изменён (SHA256 до == после) =="
rm -f "$POST_FAIL_FILE"
cp -a "$SB/oldbin" "$NODE_BIN"
D_BEFORE="$(sha "$NODE_BIN")"
out_d="$(run_upgrade "$NEW_SHA" "$SB/newbin" FAIL_DOCTOR=1)"
rc=$?
D_AFTER="$(sha "$NODE_BIN")"
[ "$rc" -eq 1 ] && ok "скрипт: exit 1 (предпроверка FAIL)" || bad "скрипт exit=$rc (ожидался 1)"
[ "$D_AFTER" = "$D_BEFORE" ] && ok "целевой не изменён (sha до==после=$D_AFTER)" || bad "целевой ИЗМЕНИЛСЯ (до=$D_BEFORE после=$D_AFTER)"
printf '%s\n' "$out_d" | grep -q 'предпроверка FAIL' && ok "печать «предпроверка FAIL» присутствует" || bad "нет печати «предпроверка FAIL»"

echo "== E. PASS-проход (шаги 1-6) под umask 077: temp в каталоге целевого (0755), без остатков =="
# umask 077 закрывает +x у mktemp-файла — без chmod 0755 после SHA шаг 3 упал бы
# на «Permission denied». Проверяем полный PASS-путь + локацию/режим файла
# предпроверки + отсутствие временных файлов после прохода.
umask 077
rm -f "$PRECHECK_INFO" "$POST_FAIL_FILE"
cp -a "$SB/oldbin" "$NODE_BIN"
out_e="$(run_upgrade "$NEW_SHA" "$SB/newbin" PRECHECK_INFO_FILE="$PRECHECK_INFO")"
rc=$?
umask 022
E_AFTER="$(sha "$NODE_BIN")"
[ "$rc" -eq 0 ] && ok "E: umask 077 -> ИТОГ PASS (exit 0)" || bad "E: скрипт exit=$rc (ожидался 0): $(tail -n3 "$out_e" | tr '\n' ' ')"
[ "$E_AFTER" = "$NEW_SHA" ] && ok "E: целевой заменён (umask 077 не сломал +x)" || bad "E: целевой НЕ заменён (sha=$E_AFTER)"
grep -q "^exe=${NODEDIR}/runpilot.node.new\." "$PRECHECK_INFO" 2>/dev/null \
  && ok "E: предпроверка выполнила файл в каталоге целевого бинарника" \
  || bad "E: предпроверка НЕ в каталоге целевого (маркер: $(tr '\n' ' ' <"$PRECHECK_INFO" 2>/dev/null))"
grep -q "^mode=755$" "$PRECHECK_INFO" 2>/dev/null \
  && ok "E: исполняемый при предпроверке файл 0755" || bad "E: исполняемый при предпроверке файл не 0755"
if ls "$NODEDIR"/runpilot.node.new.* >/dev/null 2>&1; then
  bad "E: в каталоге целевого остались runpilot.node.new.*"
else
  ok "E: временных runpilot.node.new.* в каталоге целевого нет"
fi
if ls "$SB"/rp-doctor.* >/dev/null 2>&1; then
  bad "E: в TMPDIR остались rp-doctor.*"
else
  ok "E: временных rp-doctor.* в TMPDIR нет"
fi

echo
printf 'ИТОГ sandbox: PASS=%d FAIL=%d\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ] && exit 0 || exit 1
