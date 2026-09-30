package node

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// Execer — выполнение внешних команд (tmux, ps) без оболочки.
// Единственная точка exec в узле — узел запускает только фиксированный
// набор программ (раздел 14 ТЗ).
type Execer interface {
	Output(ctx context.Context, name string, args ...string) ([]byte, error)
	// OutputInput — команда со stdin (tmux load-buffer -b … -; раздел 13.1 v2).
	OutputInput(ctx context.Context, name, input string, args ...string) ([]byte, error)
}

// CmdExecer — исполнение через exec.Command.
type CmdExecer struct{}

// Output реализует Execer.
func (CmdExecer) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

// OutputInput реализует Execer: текст передаётся через stdin (не на строке
// команды — раздел 13.1 ТЗ: «текст через stdin, без оболочки»).
func (CmdExecer) OutputInput(ctx context.Context, name, input string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = strings.NewReader(input)
	return cmd.Output()
}

// paneFormat — формат list-panes (раздел 7 ТЗ). @runpilot_exit — код завершения
// кодера (7.3 C1): обёртка agent-run ставит его при выходе qwen.
// pane_current_path — cwd панели (8.3 X1): каталог UNMANAGED-панели в вебе.
const paneFormat = "#{session_name}\t#{window_index}\t#{pane_index}\t#{pane_id}\t#{pane_pid}\t#{pane_current_command}\t#{pane_dead}\t#{@runpilot_sid}\t#{@runpilot_exit}\t#{pane_current_path}"

// PaneInfo — панель из list-panes -a.
type PaneInfo struct {
	Socket    string
	Session   string
	Window    int
	PaneIndex int
	PaneID    string
	PanePID   int
	Command   string
	Dead      bool
	SID       string // опция панели @runpilot_sid (пусто — не управляемая runpilot)
	Exit      string // опция панели @runpilot_exit (пусто — кодер жив)
	Dir       string // cwd панели (#{pane_current_path}, 8.3 X1)

	Match bool // потомок pane_pid совпал с cmdline_regex профиля
}

// Managed — панель управляется runpilot (совпадение cmdline и @runpilot_sid).
func (p PaneInfo) Managed() bool { return p.Match && p.SID != "" }

// Unmanaged — кодер запущен не через runpilot run (раздел 4 ТЗ).
func (p PaneInfo) Unmanaged() bool { return p.Match && p.SID == "" }

// procTable — снимок процессов за цикл: потомки, родители, uid, время
// запуска и командные строки (8.2: source + target для внешних кодеров).
type procTable struct {
	children map[int][]int
	ppid     map[int]int
	uid      map[int]int
	etimes   map[int]int64
	args     map[int]string
}

// matchesTree — совпал ли с cmdline_regex процесс панели (root) или один
// из его потомков. ТЗ формулирует «потомки pane_pid»; проверка включает
// и сам процесс панели, потому что реальная qwen-сессия после exec-цепочки
// bash→qwen даёт именно pane_pid = `node /…/qwen` (проверено на машине
// оператора, ps 2026-09-25), а её потомки (node cli.js) совпадений не дают.
func (t *procTable) matchesTree(root int, re *regexp.Regexp) bool {
	if re.MatchString(t.args[root]) {
		return true
	}
	seen := map[int]bool{root: true}
	queue := []int{root}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		for _, c := range t.children[p] {
			if seen[c] {
				continue
			}
			seen[c] = true
			queue = append(queue, c)
			if re.MatchString(t.args[c]) {
				return true
			}
		}
	}
	return false
}

// Scan сканирует все панели заданных сокетов (раздел 7 ТЗ). Неработающий
// сокет пропускается.
func Scan(ctx context.Context, ex Execer, sockets []string, cmdlineRe *regexp.Regexp) ([]PaneInfo, error) {
	panes, _, err := ScanWithTable(ctx, ex, sockets, cmdlineRe)
	return panes, err
}

// ScanWithTable — как Scan, но возвращает и таблицу процессов: один ps за
// цикл используется и для панелей, и для внешних кодеров (8.2 ТЗ).
func ScanWithTable(ctx context.Context, ex Execer, sockets []string, cmdlineRe *regexp.Regexp) ([]PaneInfo, *procTable, error) {
	table, err := processTable(ctx, ex)
	if err != nil {
		return nil, nil, err
	}
	var panes []PaneInfo
	for _, sock := range sockets {
		args := []string{}
		if sock != "" {
			args = append(args, "-L", sock)
		}
		args = append(args, "list-panes", "-a", "-F", paneFormat)
		out, err := ex.Output(ctx, "tmux", args...)
		if err != nil {
			continue // сокета нет — это не ошибка цикла
		}
		panes = append(panes, parsePanes(sock, out, table, cmdlineRe)...)
	}
	return panes, table, nil
}

// processTable — `ps -A -o pid=,ppid=,uid=,etimes=,args=` один раз за цикл.
func processTable(ctx context.Context, ex Execer) (*procTable, error) {
	out, err := ex.Output(ctx, "ps", "-A", "-o", "pid=,ppid=,uid=,etimes=,args=")
	if err != nil {
		return nil, fmt.Errorf("node: ps: %w", err)
	}
	t := &procTable{
		children: map[int][]int{},
		ppid:     map[int]int{},
		uid:      map[int]int{},
		etimes:   map[int]int64{},
		args:     map[int]string{},
	}
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	sc.Buffer(make([]byte, 0, scanInitBuf), procScanMaxBuf)
	for sc.Scan() {
		line := sc.Text()
		fields := strings.Fields(line)
		if len(fields) < psLeadingFields+1 {
			continue
		}
		pid, err1 := strconv.Atoi(fields[0])
		ppid, err2 := strconv.Atoi(fields[1])
		uid, err3 := strconv.Atoi(fields[psFieldUID])
		et, err4 := strconv.Atoi(fields[psFieldEtimes])
		if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
			continue
		}
		t.args[pid] = strings.Join(fields[psLeadingFields:], " ")
		t.children[ppid] = append(t.children[ppid], pid)
		t.ppid[pid] = ppid
		t.uid[pid] = uid
		t.etimes[pid] = int64(et)
	}
	return t, sc.Err()
}

// parsePanes разбирает вывод list-panes и помечает панели с кодером.
func parsePanes(sock string, out []byte, table *procTable, cmdlineRe *regexp.Regexp) []PaneInfo {
	var res []PaneInfo
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	sc.Buffer(make([]byte, 0, scanInitBuf), paneScanMaxBuf)
	for sc.Scan() {
		f := strings.SplitN(sc.Text(), "\t", paneFieldCount)
		// Требуем базовые поля (до @runpilot_sid включительно); отсутствующее
		// завершающее @runpilot_exit (пустая опция) допускаем и дополняем.
		if len(f) < paneFieldSID+1 {
			continue
		}
		for len(f) < paneFieldCount {
			f = append(f, "")
		}
		pi := PaneInfo{
			Socket:  sock,
			Session: f[0],
			PaneID:  f[paneFieldPaneID],
			Command: f[paneFieldCommand],
			SID:     f[paneFieldSID],
			Exit:    f[paneFieldExit],
			Dir:     f[paneFieldDir],
		}
		pi.Window, _ = strconv.Atoi(f[1])
		pi.PaneIndex, _ = strconv.Atoi(f[paneFieldPaneIndex])
		pi.PanePID, _ = strconv.Atoi(f[paneFieldPanePID])
		pi.Dead = f[paneFieldDead] == "1"
		if !pi.Dead && cmdlineRe != nil {
			pi.Match = table.matchesTree(pi.PanePID, cmdlineRe)
		}
		res = append(res, pi)
	}
	return res
}
