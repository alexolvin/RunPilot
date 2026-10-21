package node

import (
	"context"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"runpilot/internal/proto"
)

// GatewayCoderURL — URL шлюза кодера сессии: <gateway_url>/s/<sid>/v1.
func GatewayCoderURL(gatewayURL, sid string) string {
	return strings.TrimRight(gatewayURL, "/") + "/s/" + sid + "/v1"
}

// ExecRoot — EXEC-корень сессии: время старта процесса runpilot exec
// (/proc/<pid>/stat, поле 22, clock ticks с boot) и sid сессии. PID хранится
// в ключе карты (map[int]ExecRoot). Кодекс: кодер управляем, если он потомок
// живого корня И время старта корневого процесса совпадает с сохранённым
// (совпадение защищает от переиспользования PID).
type ExecRoot struct {
	Starttime int64
	SID       string
	PGID      int // PGID кодера (kill -PGID SIGKILL при смерти корня)
}

// procStartTimeFn — время старта процесса; подменяется в тестах (ScanExternal).
var procStartTimeFn = ProcStartTime

// ProcStartTime — время старта процесса (/proc/<pid>/stat, поле 22: clock
// ticks с boot). 0, если процесс не найден / stat не читается / процесс
// zombie (Z) — zombie считается мёртвым (п.3 плана оператора).
// comm (поле 2) может содержать пробелы и скобки — режем после последней «)».
func ProcStartTime(pid int) int64 {
	b, err := os.ReadFile(procDir + "/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0
	}
	s := string(b)
	// comm (поле 2) может содержать пробелы/скобки — режем после последней «)».
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return 0
	}
	// После «)» поля сдвинуты: state=fields[0], ..., starttime (поле 22) =
	// fields[statStarttimeIdx] (0-based).
	f := strings.Fields(s[i+1:])
	if len(f) < statStarttimeIdx+1 {
		return 0
	}
	// Zombie (Z) — процесс мёртв, starttime не имеет смысла.
	if f[0] == "Z" {
		return 0
	}
	v, err := strconv.ParseInt(f[statStarttimeIdx], decimalBase, int64Bits)
	if err != nil {
		return 0
	}
	return v
}

// CoderOutOfQueue — единое правило «кодер вне очереди» (правило 2 ТЗ), общее
// для doctor (coder_out_of_queue) и веба (ScanExternal). Процесс кодера
// (cmdline_regex) УПРАВЛЯЕМ тогда и только тогда, когда выполнены оба условия:
//   (а) он потомок корня живой сессии (sessionSID != ""): PANE-корень — панель
//       с sid, EXEC-корень — сохранённый PID exec (время старта совпадает);
//   (б) его OPENAI_BASE_URL = gatewayURL/s/<sessionSID>/v1.
// Всё остальное — вне очереди (true): ручной запуск qwen (нет корня,
// нет/чужой URL), qwen под vLLM напрямую, qwen завершённой EXEC-сессии
// (exec умер → не потомок живого корня).
func CoderOutOfQueue(sessionSID, openAIBaseURL, gatewayURL string) bool {
	if sessionSID == "" {
		return true
	}
	return openAIBaseURL != GatewayCoderURL(gatewayURL, sessionSID)
}

// SessionSID — единый вывод sid сессии кодера (общий для doctor и веба). Идёт
// вверх по цепочке родителей кодера (pid):
//   - попадается панель из paneSID → PANE: возвращает sid панели;
//   - попадается EXEC-корень из execRoots (PID) И время старта процесса
//     совпадает (starttimeOf(cur) == root.Starttime) → EXEC: возвращает
//     root.SID;
//   - иначе (ручной запуск, завершённая/чужая сессия) → "".
// Корни не выводятся по командной строке: EXEC-корень — только по сохранённому
// PID + времени старта (защита от переиспользования PID).
// parentOf/starttimeOf — доступ к таблице процессов (общий интерфейс).
func SessionSID(pid int, paneSID map[int]string, execRoots map[int]ExecRoot, starttimeOf func(pid int) int64, parentOf func(pid int) int) string {
	seen := map[int]bool{}
	cur := pid
	for cur != 0 && !seen[cur] {
		seen[cur] = true
		if s, ok := paneSID[cur]; ok {
			return s
		}
		if root, ok := execRoots[cur]; ok && starttimeOf(cur) == root.Starttime {
			return root.SID
		}
		cur = parentOf(cur)
	}
	return ""
}

// ScanExternal — внешные кодеры (8.2 ТЗ): процессы, совпадающие с
// cmdline_regex, вне очереди по единому правилу (SessionSID + CoderOutOfQueue).
// panePIDToSID — панель → sid (PANE-корни). execRoots — PID → EXEC-корень
// (время старта + sid), присланный координатором (условие (а) для EXEC).
// gatewayURL — gateway_url координатора (условие (б)). openAIBaseURL(pid) —
// OPENAI_BASE_URL кодера из /proc environ; по нему координатор выводит target.
// source — цепочка родителей до PID 1.
func ScanExternal(t *procTable, panePIDToSID map[int]string, cmdlineRe *regexp.Regexp, gatewayURL string, openAIBaseURL func(pid int) string, execRoots map[int]ExecRoot, cwd func(pid int) string) []proto.ExternalProc {
	var out []proto.ExternalProc
	for pid, args := range t.args {
		// Совпадение по EXE (basename первого поля), а не по всей cmdline:
		// tmux-сервер несёт команду кодера как аргумент (tmux new-session …
		// /path/qwen …), но сам кодером не является (exe=tmux) — иначе он
		// числился бы внешним кодером (pid tmux-сервера, source=tmux).
		if !cmdlineRe.MatchString(procName(args)) {
			continue
		}
		sessionSID := SessionSID(pid, panePIDToSID, execRoots, procStartTimeFn,
			func(p int) int { return t.ppid[p] })
		if !CoderOutOfQueue(sessionSID, openAIBaseURL(pid), gatewayURL) {
			continue // кодер живой сессии с верным URL шлюза — управляемый
		}
		exe, flags := exeAndFlags(args)
		out = append(out, proto.ExternalProc{
			PID:           pid,
			PPID:          t.ppid[pid],
			UID:           t.uid[pid],
			StartSec:      t.etimes[pid],
			Exe:           exe,
			Flags:         flags,
			Source:        t.sourceOf(pid),
			OpenAIBaseURL: openAIBaseURL(pid),
			CWD:           cwd(pid),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PID < out[j].PID })
	return out
}

// readProcCwd — cwd процесса (readlink /proc/<pid>/cwd); пустая строка при
// ошибке (процесс исчез/нет прав). Для команды перезапуска внешнего кодера.
func readProcCwd(pid int) string {
	b, err := os.Readlink(procCwdPath(pid))
	if err != nil {
		return ""
	}
	return b
}

// sourceOf — источник по цепочке родителей (включая сам процесс) до PID 1:
// первое совпадение cron/crond/anacron→cron, systemd→systemd, sshd→sshd,
// tmux→tmux; иначе other (8.2 ТЗ).
func (t *procTable) sourceOf(pid int) string {
	seen := map[int]bool{}
	cur := pid
	for cur != 0 && !seen[cur] {
		seen[cur] = true
		switch procName(t.args[cur]) {
		case "cron", "crond", "anacron":
			return sourceCron
		case "systemd":
			return sourceSystemd
		case "sshd":
			return sourceSshd
		case "tmux":
			return sourceTmux
		}
		if cur == procRootPID {
			break
		}
		cur = t.ppid[cur]
	}
	return sourceOther
}

// procName — базовое имя команды (fields[0] по basename).
func procName(args string) string {
	f := strings.Fields(args)
	if len(f) == 0 {
		return ""
	}
	p := f[0]
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		p = p[i+1:]
	}
	return p
}

// exeAndFlags — exe (basename args[0]) и имена флагов (поля, начиная с -).
// Значения аргументов не возвращаются (R5 — только exe и имена флагов).
func exeAndFlags(args string) (string, []string) {
	f := strings.Fields(args)
	if len(f) == 0 {
		return "", nil
	}
	exe := f[0]
	if i := strings.LastIndexByte(exe, '/'); i >= 0 {
		exe = exe[i+1:]
	}
	var flags []string
	for _, a := range f[1:] {
		if strings.HasPrefix(a, "-") {
			flags = append(flags, a)
		}
	}
	return exe, flags
}

// procEnvironPath — путь к /proc/<pid>/environ.
func procEnvironPath(pid int) string {
	return procDir + "/" + strconv.Itoa(pid) + "/" + procEnvironName
}

// procStatusPath — путь к /proc/<pid>/status.
func procStatusPath(pid int) string {
	return procDir + "/" + strconv.Itoa(pid) + "/" + procStatusName
}

// procCwdPath — путь к /proc/<pid>/cwd (символическая ссылка).
func procCwdPath(pid int) string {
	return procDir + "/" + strconv.Itoa(pid) + "/" + procCwdName
}

// verifyKillTarget — сверка идентичности процесса перед kill (8.2 ТЗ):
// /proc/<pid> жив, UID совпадает, start_time совпадает (now−etimes, допуск).
// Возвращает "" если ок, иначе код result (PROCESS_GONE / PROCESS_CHANGED).
func (n *Node) verifyKillTarget(ctx context.Context, pid, uid int, startTime int64) string {
	if uid <= 0 && startTime <= 0 {
		return "" // сверка не задана (6.7: простой kill)
	}
	status, err := os.ReadFile(procStatusPath(pid))
	if err != nil {
		return proto.ResProcessGone
	}
	if uid > 0 {
		if got, ok := procFieldInt(status, procUidField); !ok || got != uid {
			return proto.ResProcessChanged
		}
	}
	if startTime > 0 {
		out, err := n.ex.Output(ctx, "ps", "-o", "etimes=", "-p", strconv.Itoa(pid))
		if err != nil {
			return proto.ResProcessGone
		}
		et := parseEtimes(out)
		if et < 0 {
			return proto.ResProcessGone
		}
		liveStart := n.clk.Now().Unix() - et
		d := liveStart - startTime
		if d < 0 {
			d = -d
		}
		if d > killStartTimeToleranceSec {
			return proto.ResProcessChanged
		}
	}
	return ""
}

// procFieldInt — целое значение из строки /proc status «<field> N …».
func procFieldInt(data []byte, field string) (int, bool) {
	for _, ln := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(ln, field) {
			continue
		}
		f := strings.Fields(strings.TrimPrefix(ln, field))
		if len(f) == 0 {
			return 0, false
		}
		v, err := strconv.Atoi(f[0])
		if err != nil {
			return 0, false
		}
		return v, true
	}
	return 0, false
}

// parseEtimes — etimes (секунды) из вывода `ps -o etimes= -p <pid>`.
func parseEtimes(out []byte) int64 {
	f := strings.Fields(string(out))
	if len(f) == 0 {
		return -1
	}
	v, err := strconv.ParseInt(f[0], decimalBase, int64Bits)
	if err != nil {
		return -1
	}
	return v
}

// readProcEnvValue — значение переменной (key с «=») из /proc/<pid>/environ
// (null-разделён). macOS/отсутствие/нет ключа — пустая строка.
func readProcEnvValue(pid int, key string) string {
	b, err := os.ReadFile(procEnvironPath(pid))
	if err != nil {
		return ""
	}
	for _, kv := range strings.Split(string(b), procEnvSep) {
		if strings.HasPrefix(kv, key) {
			return strings.TrimPrefix(kv, key)
		}
	}
	return ""
}

// readOpenAIBaseURL — OPENAI_BASE_URL из /proc environ. macOS/отсутствие —
// пустая строка (target = unknown на координаторе).
func readOpenAIBaseURL(pid int) string { return readProcEnvValue(pid, openaiBaseURLKey) }
