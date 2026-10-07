package node

import (
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"runpilot/internal/proto"
)

// reportDeadExecRoots — узел проверяет свои EXEC-корни (PID + starttime) и
// шлёт KindExecRootDead координатору, когда корень мёртв.
func TestReportDeadExecRoots(t *testing.T) {
	starts := func(m map[int]int64) func() {
		old := procStartTimeFn
		procStartTimeFn = func(pid int) int64 { return m[pid] }
		return func() { procStartTimeFn = old }
	}
	collect := func(n *Node) []proto.Msg {
		var got []proto.Msg
		n.reportDeadExecRoots(func(m proto.Msg) error {
			got = append(got, m)
			return nil
		})
		return got
	}
	root := func() *Node {
		n := &Node{}
		n.SetExecRoots(map[int]ExecRoot{500: {Starttime: 123456, SID: "sid501"}})
		return n
	}

	t.Run("живой корень — отчёта нет", func(t *testing.T) {
		defer starts(map[int]int64{500: 123456})()
		if got := collect(root()); len(got) != 0 {
			t.Fatalf("живой корень: %+v, хочу 0 отчётов", got)
		}
	})
	t.Run("PID исчез (starttime 0) — отчёт", func(t *testing.T) {
		defer starts(map[int]int64{})() // 500 нет → ProcStartTime=0
		got := collect(root())
		if len(got) != 1 || got[0].Type != proto.KindExecRootDead ||
			got[0].SID != "sid501" || got[0].PID != 500 {
			t.Fatalf("PID исчез: %+v", got)
		}
	})
	t.Run("PID переиспользован (starttime не совпал) — отчёт", func(t *testing.T) {
		defer starts(map[int]int64{500: 999})() // 999 != 123456
		got := collect(root())
		if len(got) != 1 || got[0].Type != proto.KindExecRootDead {
			t.Fatalf("PID переиспользован: %+v, хочу 1 отчёт", got)
		}
	})
	t.Run("корень без starttime (0) — не проверяем", func(t *testing.T) {
		n := &Node{}
		n.SetExecRoots(map[int]ExecRoot{500: {Starttime: 0, SID: "sid501"}})
		var got []proto.Msg
		n.reportDeadExecRoots(func(m proto.Msg) error {
			got = append(got, m)
			return nil
		})
		if len(got) != 0 {
			t.Fatalf("корень без starttime: %+v, хочу 0", got)
		}
	})
}

// killCoderGroup — выборочный kill: pgrp == PGID И RUNPILOT_SID == sid.
// Процесс в той же группе БЕЗ RUNPILOT_SID этой сессии — жив.
func TestKillCoderGroupSelective(t *testing.T) {
	const sid = "TESTSID001"

	// Запускаем процесс с Setpgid + RUNPILOT_SID=sid.
	cmd := exec.Command("sleep", "60")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = append(os.Environ(), "RUNPILOT_SID="+sid)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pgid := cmd.Process.Pid
	defer func() { _ = syscall.Kill(-pgid, syscall.SIGKILL) }()

	// Процесс в той же группе БЕЗ RUNPILOT_SID (не должен быть убит).
	cmd2 := exec.Command("sleep", "60")
	cmd2.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd2.Env = append(os.Environ(), "RUNPILOT_SID=OTHERSID")
	if err := cmd2.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = syscall.Kill(-cmd2.Process.Pid, syscall.SIGKILL) }()

	// Даем процессым время стартовать.
	time.Sleep(100 * time.Millisecond)

	// killCoderGroup: убивает только процессы с RUNPILOT_SID=sid в группе pgid.
	killCoderGroup(pgid, sid)

	// Ждём, пока процесс с RUNPILOT_SID умрёт (Z или исчез).
	deadline := time.Now().Add(3 * time.Second)
	for {
		if procState(pgid) != "S" {
			break // не в состоянии S (убит → Z или исчез)
		}
		if time.Now().After(deadline) {
			t.Fatal("процесс с RUNPILOT_SID не убит killCoderGroup")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Процесс БЕЗ RUNPILOT_SID должен быть жив (state=S).
	if st := procState(cmd2.Process.Pid); st != "S" {
		t.Fatalf("процесс без RUNPILOT_SID: state=%s, хочу S", st)
	}
}

// ProcStartTime: zombie (Z) — мёртв (возвращает 0).
func TestProcStartTimeZombie(t *testing.T) {
	// bash: потомок exit → zombie; disown — shell не reap'ит; sleep 30с.
	cmd := exec.Command("bash", "-c", "true & disown; sleep 30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	bashPID := cmd.Process.Pid

	// Ищем zombie-потомка: state=Z, ppid=bashPID.
	time.Sleep(300 * time.Millisecond)
	entries, err := os.ReadDir(procDir)
	if err != nil {
		t.Fatal(err)
	}
	var zombiePid int
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pid, ok := atoiSafe(e.Name())
		if !ok {
			continue
		}
		stat, err := os.ReadFile(procDir + "/" + e.Name() + "/stat")
		if err != nil {
			continue
		}
		s := statString(stat)
		i := lastIdx(s, ')')
		if i < 0 {
			continue
		}
		f := stringsFields(s[i+1:])
		if len(f) < 3 {
			continue
		}
		if f[0] == "Z" && f[1] == itoa(bashPID) {
			zombiePid = pid
			break
		}
	}
	if zombiePid == 0 {
		t.Skip("zombie не найден (race: уже reaped)")
	}
	if st := ProcStartTime(zombiePid); st != 0 {
		t.Fatalf("ProcStartTime(zombie) = %d, хочу 0", st)
	}
}

func statString(b []byte) string { return string(b) }

func lastIdx(s string, b byte) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == b {
			return i
		}
	}
	return -1
}

func stringsFields(s string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ' ' {
			if i > start {
				out = append(out, s[start:i])
			}
			start = i + 1
		}
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func atoiSafe(s string) (int, bool) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, true
}

// procState — state из /proc/<pid>/stat ("", "S", "Z", "R", …).
func procState(pid int) string {
	b, err := os.ReadFile(procDir + "/" + itoa(pid) + "/stat")
	if err != nil {
		return ""
	}
	s := string(b)
	i := lastIdx(s, ')')
	if i < 0 || i+2 >= len(s) {
		return ""
	}
	return string(s[i+2 : i+3])
}
