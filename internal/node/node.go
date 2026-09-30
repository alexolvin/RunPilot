// Package node — узловой демон runpilot (разделы 7, 11, 13, 14 ТЗ).
//
// Узел: сканер tmux-панелей, снимки управляемых панелей, WebSocket-канал
// к координатору (proto=1) и исполнение команд dispatch/send_keys/capture/
// set_status/set_summary. Узел запускает только tmux, ps и (Э6)
// nvidia-smi/rocm-smi через exec.Command без оболочки.
package node

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"runpilot/internal/clock"
	"runpilot/internal/config"
	"runpilot/internal/detect"
	"runpilot/internal/proto"
	"runpilot/profiles"
)

// Node — узловой демон.
type Node struct {
	cfg      *config.Config
	prof     *profiles.Qwen
	regexps  detect.Regexps
	ex       Execer
	clk      clock.Clock
	log      *slog.Logger
	agentVer string

	snap *snapshotter
	cmd  *CommandRunner

	// unmanaged — UNMANAGED-панели (8.3 X1) по pane_id: для adopt socketFor
	// находит сокет панели (managed — в snap). Заполняется cycle каждый цикл.
	umMu      sync.Mutex
	unmanaged map[string]PaneInfo

	// projectRoots — из config (14.4); валидация каталогов spawn (13.2).
	roots   []string
	rootsMu sync.Mutex

	// nodeToken — Bearer-токен узла для загрузки дистрибутива (14.2).
	nodeToken string
	// binPathOverride — путь бинарника узла (подмена в тестах self-update).
	binPathOverride string

	// sentExit — панели, по которым уже отправлен KindAgentExit (7.3 C1):
	// опцию @runpilot_exit снимает respawn, поэтому повтор не нужен (edge-trigger).
	sentExit map[string]bool

	// node_health (N9/N10, 14.4): период и последний снимок.
	healthIv   time.Duration
	lastHealth time.Time
	diskPath   string

	// Терминал в браузере (13.4 ТЗ): открытые PTY-каналы и обратные вызовы.
	// ptyMu держит ptySes; ptySender — вывод PTY в координатор (двоичные кадры),
	// sendMsg — proto-сообщения (pty_exit), ставятся до Run.
	ptyMu     sync.Mutex
	ptySes    map[int]*ptySession
	ptySender func(chan_ int, data []byte) error
	sendMsg   func(proto.Msg) error
}

// New — сборка узла из конфига и встроенного/override-профиля qwen.
func New(cfg *config.Config, prof *profiles.Qwen, ex Execer, clk clock.Clock, log *slog.Logger) (*Node, error) {
	re, err := prof.Regexps()
	if err != nil {
		return nil, err
	}
	n := &Node{
		cfg:      cfg,
		prof:     prof,
		regexps:  re,
		ex:       ex,
		clk:      clk,
		log:      log,
		agentVer: versionFromCmd(ctxBackground(), prof.VersionCmd),
		roots:    cfg.Node.ProjectRootsDefault,
		sentExit: map[string]bool{},
		unmanaged: map[string]PaneInfo{},
		healthIv: time.Duration(cfg.Node.HealthIntervalSec) * time.Second,
		diskPath: defaultDiskPath(),
		ptySes:   map[int]*ptySession{},
	}
	n.snap = newSnapshotter(clk, n.agentVer)
	n.cmd = NewCommandRunner(ex, re, Profile{
		SubmitKeys:            []string{prof.SubmitKeys},
		CancelKeys:            []string{prof.CancelKeys},
		ResumeText:            prof.ResumeText,
		ApprovalOptions:       profileApprovalOptions(prof),
		CompressText:          prof.CompressText,
		QuitText:              prof.QuitText,
		PastePlaceholderRegex: prof.PastePlaceholderRegex,
	}, time.Duration(cfg.Dispatch.ResumeKeyDelayMS)*time.Millisecond)
	return n, nil
}

// ctxBackground — фон для разовых вызовов вне цикла (version_cmd).
func ctxBackground() context.Context { return context.Background() }

// profileApprovalOptions — варианты подтверждения профиля → Profile узла.
func profileApprovalOptions(prof *profiles.Qwen) []ApprovalOption {
	out := make([]ApprovalOption, 0, len(prof.ApprovalOptions))
	for _, o := range prof.ApprovalOptions {
		out = append(out, ApprovalOption{ID: o.ID, Label: o.Label, Keys: o.Keys})
	}
	return out
}

// versionFromCmd — версия агента из version_cmd (последний токен вывода).
func versionFromCmd(ctx context.Context, versionCmd string) string {
	parts := strings.Fields(versionCmd)
	if len(parts) == 0 {
		return ""
	}
	out, err := exec.CommandContext(ctx, parts[0], parts[1:]...).Output()
	if err != nil {
		return ""
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return ""
	}
	return fields[len(fields)-1]
}

// HostIP — локальный адрес интерфейса, через который узел достаёт
// координатор (по нему координатор сверяет host_ip в hello, раздел 14 ТЗ).
func HostIP(remote string) string {
	conn, err := net.Dial("udp", remote)
	if err != nil {
		return ""
	}
	defer conn.Close()
	l, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		return ""
	}
	return l.IP.String()
}

// Run — главный цикл узла: сканер + снимки; send — отправка сообщений
// координатору. ctx отменяет узел.
func (n *Node) Run(ctx context.Context, send func(proto.Msg) error) error {
	n.sendMsg = send
	iv := time.Duration(n.cfg.Node.ScanIntervalSec) * time.Second
	t := time.NewTicker(iv)
	defer t.Stop()
	defer n.ptyCloseAll() // терминалы узла (13.4) — закрыть при остановке

	// node_health сразу при старте (N9/N10, 14.4).
	n.lastHealth = n.clk.Now()
	n.sendHealth(ctx, send)

	// Первый цикл сразу (обнаружение ≤ scan_interval_sec, раздел 7 ТЗ).
	if err := n.cycle(ctx, send); err != nil {
		n.log.Warn("node: первый цикл: " + err.Error())
	}
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("node: остановлен: %w", ctx.Err())
		case <-t.C:
			if err := n.cycle(ctx, send); err != nil {
				n.log.Warn("node: цикл: " + err.Error())
			}
			n.maybeHealth(ctx, send)
		}
	}
}

// cycle — один цикл: list-panes + ps (один), снимки управляемых, внешние
// кодеры (8.2), отправка.
func (n *Node) cycle(ctx context.Context, send func(proto.Msg) error) error {
	panes, table, err := ScanWithTable(ctx, n.ex, n.cfg.Node.TmuxSockets, n.prof.Cmdline())
	if err != nil {
		return err
	}
	now := n.clk.Now()
	panePIDs := map[int]bool{}
	seen := map[string]bool{}
	for _, p := range panes {
		if p.Dead {
			continue
		}
		panePIDs[p.PanePID] = true
		if !p.Managed() {
			continue
		}
		seen[p.PaneID] = true
		// 7.3 C1: кодер в панели завершился (@runpilot_exit не пусто).
		n.reportAgentExit(p, send)
		st, hash, input, err := capturePane(ctx, n.ex, n.regexps, p)
		if err != nil {
			continue // панель исчезла между list и capture
		}
		if pane, changed := n.snap.sync(p, st, hash, input, now); changed {
			if err := send(proto.PaneOf(pane)); err != nil {
				return err
			}
		} else if pane, ok := n.snap.pulse(p, now); ok {
			if err := send(proto.PaneOf(pane)); err != nil {
				return err
			}
		}
	}
	// Панель исчезла (убили tmux-сессию, кодер закрыл окно): дельта
	// pane_gone координатору — снимок снимается, сессия ведётся в GONE
	// (N2/N3). Без дельты координатор знает о гибели панели только при
	// полном списке на hello (переподключении узла).
	for id := range n.snap.panes {
		if !seen[id] {
			if ps := n.snap.panes[id]; ps != nil && ps.info.SID != "" {
				gm := proto.New(proto.KindPaneGone)
				gm.PaneID, gm.SID = id, ps.info.SID
				if err := send(gm); err != nil {
					return err
				}
			}
			n.snap.forget(id)
		}
	}
	// UNMANAGED-панели (8.3 X1): кодер без @runpilot_sid — полный список каждого
	// цикла (включая пустой — координатор сверяет живое множество). Реестр
	// по pane_id держит и узел: adopt находит сокет панели (socketFor).
	unmanaged := []proto.UnmanagedPane{}
	umSet := make(map[string]PaneInfo, len(panes))
	for _, p := range panes {
		if p.Dead || !p.Match || p.SID != "" {
			continue
		}
		umSet[p.PaneID] = p
		unmanaged = append(unmanaged, proto.UnmanagedPane{
			PaneID: p.PaneID, Session: p.Session, Dir: p.Dir,
			Cmd: p.Command, Socket: p.Socket,
		})
	}
	n.umMu.Lock()
	n.unmanaged = umSet
	n.umMu.Unlock()
	um := proto.New(proto.KindUnmanaged)
	um.Unmanaged = unmanaged
	if err := send(um); err != nil {
		return err
	}
	// Внешние кодеры вне tmux/runpilot (8.2 ТЗ): полный список каждого цикла
	// (координатор сверяет живое множество).
	ext := ScanExternal(table, panePIDs, n.prof.Cmdline(), readOpenAIBaseURL)
	if len(ext) > 0 {
		m := proto.New(proto.KindExternal)
		m.Externals = ext
		if err := send(m); err != nil {
			return err
		}
	}
	return nil
}

// reportAgentExit — 7.3 C1: управляемая панель с не-пустым @runpilot_exit — кодер
// завершился/убит. Шлёт KindAgentExit{sid, exit_code} координатору один раз
// (edge-trigger): опцию снимает respawn. exit_code > 128 — сигнал (код-128).
func (n *Node) reportAgentExit(p PaneInfo, send func(proto.Msg) error) {
	if p.Exit == "" {
		delete(n.sentExit, p.PaneID)
		return
	}
	if n.sentExit[p.PaneID] {
		return
	}
	code, _ := strconv.Atoi(p.Exit)
	m := proto.New(proto.KindAgentExit)
	m.SID = p.SID
	m.ExitCode = code
	if err := send(m); err != nil {
		return // не помечено — повторим в следующем цикле
	}
	n.sentExit[p.PaneID] = true
}

// Prefill — первичный скан до стартового «полного списка панелей» (KindPanes):
// наполняет снапшот управляемых панелей. Без него узел шлёт ПУСТОЙ список
// (цикл ещё не отработал), а координатор сверкой с БД помечает живые сессии
// GONE — ложная потеря на чистом рестарте (панель жива, а список пуст).
func (n *Node) Prefill(ctx context.Context) error {
	panes, _, err := ScanWithTable(ctx, n.ex, n.cfg.Node.TmuxSockets, n.prof.Cmdline())
	if err != nil {
		return err
	}
	now := n.clk.Now()
	for _, p := range panes {
		if p.Dead || !p.Managed() {
			continue
		}
		st, hash, input, err := capturePane(ctx, n.ex, n.regexps, p)
		if err != nil {
			continue // панель исчезла между list и capture
		}
		n.snap.sync(p, st, hash, input, now)
	}
	return nil
}

// PaneList — полный список панелей последнего скана (сообщение panes).
func (n *Node) PaneList() []proto.Pane {
	var list []proto.Pane
	for _, ps := range n.snap.panes {
		list = append(list, paneMsg(ps.info, ps.state, ps.hash, ps.input, ps.sentAt, n.agentVer))
	}
	return list
}

// Handle — команда координатора. Узловые операции (spawn/list_dirs/update/
// uninstall/config/kill_process) работают без панели; панельные (respawn/
// kill_session/release_pane) получают сокет из последнего скана; остальное —
// в CommandRunner (dispatch/send_keys/capture/…).
func (n *Node) Handle(ctx context.Context, m proto.Msg) (*Reply, error) {
	switch m.Type {
	case proto.KindSpawn:
		return n.spawn(ctx, m)
	case proto.KindListDirs:
		return n.listDirs(ctx, m)
	case proto.KindUpdate:
		return n.update(ctx, m)
	case proto.KindUninstall:
		return n.uninstall(ctx, m)
	case proto.KindConfig:
		return n.config(ctx, m)
	case proto.KindQwenSettings:
		return n.qwenSettings(ctx, m)
	case proto.KindAdopt:
		return n.adopt(ctx, m)
	case proto.KindKillProcess:
		return n.killProcess(ctx, m)
	case proto.KindPtyOpen:
		return n.ptyOpen(ctx, m)
	case proto.KindPtyClose:
		return n.ptyClose(m)
	case proto.KindRespawn, proto.KindKillSession, proto.KindReleasePane:
		socket, ok := n.socketFor(m.PaneID)
		if !ok {
			return &Reply{CmdID: m.CmdID, Result: ResPaneGone}, nil
		}
		switch m.Type {
		case proto.KindRespawn:
			return n.respawn(ctx, socket, m)
		case proto.KindKillSession:
			return n.killSession(ctx, socket, m)
		case proto.KindReleasePane:
			return n.releasePane(ctx, socket, m)
		}
	}
	socket, ok := n.socketFor(m.PaneID)
	if !ok {
		return &Reply{CmdID: m.CmdID, Result: ResPaneGone}, nil
	}
	return n.cmd.HandleCommand(ctx, socket, m)
}

// HandleAndSend — команда координатора: обработать и отправить ответ через
// send (v2 раздел 2.5: встроенный узел использует это вместо WS-out).
func (n *Node) HandleAndSend(ctx context.Context, m proto.Msg, send func(proto.Msg) error) error {
	rep, err := n.Handle(ctx, m)
	if err != nil {
		return err
	}
	return send(rep.msg())
}

// socketFor — сокет панели: managed — по последнему скану (snap), unmanaged —
// из реестра (adopt, 8.3 X1). Реестр заполняет cycle; без него adopt всегда
// падал в PANE_GONE (unmanaged-панелей не было в snap).
func (n *Node) socketFor(paneID string) (string, bool) {
	if ps := n.snap.panes[paneID]; ps != nil {
		return ps.info.Socket, true
	}
	n.umMu.Lock()
	defer n.umMu.Unlock()
	if p, ok := n.unmanaged[paneID]; ok {
		return p.Socket, true
	}
	return "", false
}

// TmuxVersion — версия tmux (для hello).
func TmuxVersion(ctx context.Context, ex Execer) string {
	out, err := ex.Output(ctx, "tmux", "-V")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// OS — операционная система (для hello).
func OS() string { return runtime.GOOS }

// Hostname — имя машины (для hello).
func Hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return h
}
