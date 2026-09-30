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

// ScanExternal — внешние кодеры (8.2 ТЗ): процессы, совпадающие с
// cmdline_regex, вне любой tmux-панели (внутри tmux — UNMANAGED, текущее
// поведение) и вне runpilot exec. environ(pid) — OPENAI_BASE_URL из /proc environ;
// по нему координатор выводит target. source — цепочка родителей до PID 1.
func ScanExternal(t *procTable, panePIDs map[int]bool, cmdlineRe *regexp.Regexp, environ func(pid int) string) []proto.ExternalProc {
	var out []proto.ExternalProc
	for pid, args := range t.args {
		if !cmdlineRe.MatchString(args) {
			continue
		}
		if t.isUnder(pid, panePIDs) {
			continue
		}
		if t.isRunpilot(pid) {
			continue
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
			OpenAIBaseURL: environ(pid),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PID < out[j].PID })
	return out
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

// isUnder — pid (или один из его родителей) входит в roots.
func (t *procTable) isUnder(pid int, roots map[int]bool) bool {
	seen := map[int]bool{}
	cur := pid
	for cur != 0 && !seen[cur] {
		seen[cur] = true
		if roots[cur] {
			return true
		}
		cur = t.ppid[cur]
	}
	return false
}

// isRunpilot — pid или его родитель — процесс runpilot (исключение runpilot exec).
func (t *procTable) isRunpilot(pid int) bool {
	seen := map[int]bool{}
	cur := pid
	for cur != 0 && !seen[cur] {
		seen[cur] = true
		if procName(t.args[cur]) == runpilotExe {
			return true
		}
		cur = t.ppid[cur]
	}
	return false
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

// readOpenAIBaseURL — OPENAI_BASE_URL из /proc/<pid>/environ (null-разделён).
// macOS/отсутствие — пустая строка (target = unknown на координаторе).
func readOpenAIBaseURL(pid int) string {
	b, err := os.ReadFile(procEnvironPath(pid))
	if err != nil {
		return ""
	}
	for _, kv := range strings.Split(string(b), procEnvSep) {
		if strings.HasPrefix(kv, openaiBaseURLKey) {
			return strings.TrimPrefix(kv, openaiBaseURLKey)
		}
	}
	return ""
}
