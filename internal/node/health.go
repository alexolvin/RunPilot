// node_health узла (N9/N10, раздел 14.4 ТЗ): раз в node.health_interval_sec
// узел шлёт координатору {tmux_version, qwen_path, qwen_version, disk_free_mb,
// time}. По пустым tmux_version/qwen_path координатор выводит причину
// неактивности функций узла (N9); по расхождению time — индикацию N10.
package node

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"runpilot/internal/proto"
)

// defaultDiskPath — каталог бинарника узла (для statfs); без binPathOverride
// берётся текущий каталог.
func defaultDiskPath() string {
	if b, err := os.Executable(); err == nil {
		return filepath.Dir(b)
	}
	return "."
}

// diskFreeMB — свободное место на диске (МБ) по пути (statfs).
func diskFreeMB(path string) int {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0
	}
	return int(st.Bavail * uint64(st.Bsize) / mibInBytes)
}

// whichPath — путь бинарника через `which` (пусто — не найден, N9 no_qwen).
func whichPath(ctx context.Context, ex Execer, bin string) string {
	if bin == "" {
		return ""
	}
	out, err := ex.Output(ctx, "which", bin)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// healthMsg — снимок node_health узла (14.4).
func (n *Node) healthMsg(ctx context.Context) proto.Msg {
	m := proto.New(proto.KindNodeHealth)
	m.TmuxVersion = TmuxVersion(ctx, n.ex)
	m.QwenPath = whichPath(ctx, n.ex, n.prof.Command)
	m.QwenVersion = n.agentVer
	m.DiskFreeMB = diskFreeMB(n.diskPath)
	m.NodeTimeMS = n.clk.Now().UnixMilli()
	return m
}

// sendHealth — отправить node_health координатору (N9/N10, 14.4).
func (n *Node) sendHealth(ctx context.Context, send func(proto.Msg) error) {
	if err := send(n.healthMsg(ctx)); err != nil {
		n.log.Warn("node: health: " + err.Error())
	}
}

// maybeHealth — раз в healthIv отправить node_health (14.4).
func (n *Node) maybeHealth(ctx context.Context, send func(proto.Msg) error) {
	now := n.clk.Now()
	if now.Sub(n.lastHealth) < n.healthIv {
		return
	}
	n.lastHealth = now
	n.sendHealth(ctx, send)
}
