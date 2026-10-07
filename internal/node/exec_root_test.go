package node

import (
	"testing"

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
