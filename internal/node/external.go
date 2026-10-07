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

// execCmdRe — cmdline процесса runpilot exec (JOB-сессия, 8.1 ТЗ): «runpilot
// exec …», с путём или без. Корень EXEC-сессии — PID этого процесса.
var execCmdRe = regexp.MustCompile(`(^|/)runpilot exec( |$)`)

// CoderOutOfQueue — единое правило «кодер вне очереди» (правило 2 ТЗ), общее
// для doctor (coder_out_of_queue) и веба (ScanExternal). Процесс кодера
// (cmdline_regex) УПРАВЛЯЕМ тогда и только тогда, когда выполнены оба условия:
//   (а) он процесс кодера живой сессии — потомок корневого процесса сессии
//       (sessionSID != ""): для PANE — панель с sid, для EXEC — процесс
//       runpilot exec;
//   (б) его OPENAI_BASE_URL = gatewayURL/s/<sessionSID>/v1.
// Всё остальное — вне очереди (true). Включает ручной запуск qwen (нет корня,
// нет/чужой URL), qwen под vLLM напрямую и qwen завершённой EXEC-сессии
// (exec умер → кодёр репарентирован в init → не потомок живого корня).
func CoderOutOfQueue(sessionSID, openAIBaseURL, gatewayURL string) bool {
	if sessionSID == "" {
		return true
	}
	return openAIBaseURL != GatewayCoderURL(gatewayURL, sessionSID)
}

// SessionSID — единый вывод sid сессии кодера (общий для doctor и веба). Идёт
// вверх по цепочке родителей кодера (pid):
//   - попадается панель из paneSID → PANE: возвращает sid панели;
//   - попадается процесс runpilot exec → EXEC: возвращает coderRunpilotSID
//     (RUNPILOT_SID самого кодера из /proc environ — ставит координатор в env);
//   - иначе (ручной запуск, завершённая сессия) → "".
// parentOf/argsOf — доступ к таблице процессов узла/doctor (общий интерфейс).
func SessionSID(pid int, paneSID map[int]string, coderRunpilotSID string, parentOf func(pid int) int, argsOf func(pid int) string) string {
	seen := map[int]bool{}
	cur := pid
	for cur != 0 && !seen[cur] {
		seen[cur] = true
		if s, ok := paneSID[cur]; ok {
			return s
		}
		if execCmdRe.MatchString(argsOf(cur)) {
			return coderRunpilotSID
		}
		cur = parentOf(cur)
	}
	return ""
}

// ScanExternal — внешние кодеры (8.2 ТЗ): процессы, совпадающие с
// cmdline_regex, вне очереди по единому правилу (SessionSID + CoderOutOfQueue).
// panePIDToSID — панель → sid (PANE-корни). gatewayURL — gateway_url
// координатора (условие (б)). openAIBaseURL(pid) — OPENAI_BASE_URL кодера из
// /proc environ; по нему координатор выводит target. runpilotSID(pid) —
// RUNPILOT_SID кодера (env) — sid для EXEC-корней. EXEC-корни (процессы
// runpilot exec) выводятся из таблицы процессов локально (execCmdRe).
// source — цепочка родителей до PID 1.
func ScanExternal(t *procTable, panePIDToSID map[int]string, cmdlineRe *regexp.Regexp, gatewayURL string, openAIBaseURL func(pid int) string, runpilotSID func(pid int) string, cwd func(pid int) string) []proto.ExternalProc {
	var out []proto.ExternalProc
	for pid, args := range t.args {
		if !cmdlineRe.MatchString(args) {
			continue
		}
		sessionSID := SessionSID(pid, panePIDToSID, runpilotSID(pid),
			func(p int) int { return t.ppid[p] },
			func(p int) string { return t.args[p] })
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

// readRunpilotSID — RUNPILOT_SID из /proc environ (sid EXEC-сессии кодера).
// Для EXEC-корней единого правила «кодер вне очереди».
func readRunpilotSID(pid int) string { return readProcEnvValue(pid, runpilotSIDKey) }
