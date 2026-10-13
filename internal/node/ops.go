package node

// proto 2 — операции узла (разделы 13.2/13.5/14.2/5.3/14.4 ТЗ v2): spawn,
// list_dirs, kill_session, release_pane, respawn, kill_process, config,
// update, uninstall. Панельные (respawn/kill_session/release_pane) получают
// сокет из последнего скана; узловые (spawn/list_dirs/update/uninstall/config)
// работают без панели.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"runpilot/internal/detect"
	"runpilot/internal/proto"
)

// sortedEnvKeys — ключи env в сортированном порядке (детерминированная строка).
func sortedEnvKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// --- project_roots (14.4) ---

// SetNodeToken — Bearer-токен узла для загрузки дистрибутива (14.2).
func (n *Node) SetNodeToken(tok string) { n.nodeToken = tok }

// SetVersion — версия бинарника узла (W12: защита self-update).
func (n *Node) SetVersion(v string) { n.version = v }

// SetBuildTime — commit timestamp бинарника узла (W12: детект отката).
func (n *Node) SetBuildTime(t int64) { n.buildTimeUnix = t }

// SetBinPath — путь бинарника узла (self-update swap; подмена в тестах).
func (n *Node) SetBinPath(p string) { n.binPathOverride = p }

// SetProjectRoots — принять project_roots из config (14.4).
func (n *Node) SetProjectRoots(roots []string) {
	n.rootsMu.Lock()
	defer n.rootsMu.Unlock()
	n.roots = roots
}

func (n *Node) rootsList() []string {
	n.rootsMu.Lock()
	defer n.rootsMu.Unlock()
	if len(n.roots) > 0 {
		return n.roots
	}
	return n.cfg.Node.ProjectRootsDefault
}

// validateDir — 13.2: каталог существует и после filepath.EvalSymlinks лежит
// внутри project_roots узла. Возвращает "" (OK) или DIR_FORBIDDEN /
// DIR_NOT_FOUND. Симлинк вне корней → DIR_FORBIDDEN (результирующий путь).
func (n *Node) validateDir(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return proto.ResDirNotFound
	}
	if abs, err = filepath.EvalSymlinks(abs); err != nil {
		return proto.ResDirNotFound // каталог (или цель симлинка) не существует
	}
	if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
		return proto.ResDirNotFound
	}
	for _, root := range n.rootsList() {
		rroot, err := filepath.Abs(expandTilde(root))
		if err != nil {
			continue
		}
		if rroot, err = filepath.EvalSymlinks(rroot); err != nil {
			continue
		}
		if withinDir(rroot, abs) {
			return ""
		}
	}
	return proto.ResDirForbidden
}

func withinDir(root, p string) bool {
	root, p = filepath.Clean(root), filepath.Clean(p)
	if p == root {
		return true
	}
	return strings.HasPrefix(p, root+string(os.PathSeparator))
}

func expandTilde(s string) string {
	if s != "~" && !strings.HasPrefix(s, "~/") {
		return s
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return s
	}
	if s == "~" {
		return home
	}
	return home + strings.TrimPrefix(s, "~")
}

// --- spawn (13.2) ---

func (n *Node) spawnSocket() string {
	s := n.cfg.Node.TmuxSockets
	if len(s) > 0 {
		return s[0]
	}
	return ""
}

func (n *Node) spawn(ctx context.Context, m proto.Msg) (*Reply, error) {
	// Агент: только профиль узла (qwen). Пусто = профиль узла.
	if m.Agent != "" && m.Agent != n.prof.Name {
		return &Reply{CmdID: m.CmdID, Result: proto.ResAgentNotFound, Detail: m.Agent}, nil
	}
	if code := n.validateDir(m.Dir); code != "" {
		return &Reply{CmdID: m.CmdID, Result: code, Detail: m.Dir}, nil
	}
	socket := n.spawnSocket()
	if tmuxSessionExists(ctx, n.ex, socket, m.Name) {
		return &Reply{CmdID: m.CmdID, Result: proto.ResTmuxSessionExists, Detail: m.Name}, nil
	}
	// W13 этап 7: предпроверка ПЕРЕД запуском — только проверяет, ничего не
	// правит. Коллизия model_alias с id modelProviders (кодер пойдёт мимо
	// шлюза) или неподдерживаемые аргументы qwen → PRECHECK_FAILED (409).
	if alias := m.Env["OPENAI_MODEL"]; alias != "" {
		if err := n.PreCheckCoder(alias, m.Dir, n.coderCommand()); err != nil {
			return &Reply{CmdID: m.CmdID, Result: proto.ResPrecheckFailed, Detail: err.Error()}, nil
		}
	}
	env := n.coderEnv(m.Env)
	// W15 этап 3: per-сессии system-settings.json (модальности пула + контекст)
	// → кодер разрешает read_file медиа и объявляет реальный контекст.
	n.applySystemSettingsEnv(env, m.SID, m.InputModalities, m.ContextWindow)
	args := []string{"new-session", "-d", "-s", m.Name, "-c", m.Dir}
	for _, k := range sortedEnvKeys(env) {
		args = append(args, "-e", k+"="+env[k])
	}
	args = append(args, n.coderCommand())
	args = append(args, CoderArgsFromEnv(env)...)
	// R0 ТЗ Relay §3: --session-id <uuid> --chat-recording (запись хода).
	if m.QwenSessionID != "" {
		args = append(args, "--session-id", m.QwenSessionID, "--chat-recording")
	}
	args = append(args, m.Args...)
	if _, err := n.ex.Output(ctx, "tmux", tmuxArgs(socket, args...)...); err != nil {
		return &Reply{CmdID: m.CmdID, Result: "SPAWN_FAILED", Detail: err.Error()}, nil
	}
	pane := firstPaneID(ctx, n.ex, socket, m.Name)
	if pane == "" {
		return &Reply{CmdID: m.CmdID, Result: "SPAWN_FAILED", Detail: "нет панели"}, nil
	}
	if _, err := n.ex.Output(ctx, "tmux", tmuxArgs(socket, "set-option", "-p", "-t", pane, "@runpilot_sid", m.SID)...); err != nil {
		return &Reply{CmdID: m.CmdID, Result: "SPAWN_FAILED", Detail: err.Error()}, nil
	}
	return &Reply{CmdID: m.CmdID, Result: proto.ResSpawned, Detail: pane}, nil
}

func tmuxSessionExists(ctx context.Context, ex Execer, socket, name string) bool {
	_, err := ex.Output(ctx, "tmux", tmuxArgs(socket, "has-session", "-t", "="+name)...)
	return err == nil
}

func firstPaneID(ctx context.Context, ex Execer, socket, session string) string {
	out, err := ex.Output(ctx, "tmux", tmuxArgs(socket, "list-panes", "-t", session, "-F", "#{pane_id}")...)
	if err != nil {
		return ""
	}
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if l != "" {
			return l
		}
	}
	return ""
}

// --- list_dirs (13.2): автодополнение каталогов из project_roots ---

func (n *Node) listDirs(ctx context.Context, m proto.Msg) (*Reply, error) {
	max := n.cfg.Web.DirsMax // web.dirs_max, default 100
	var dirs []string
	// Roots для подсказки веба: существующие корни, абсолютные пути
	// (тильды раскрыты, симлинки разрешены), дубликаты убраны.
	seen := map[string]bool{}
	var rootOut []string
	for _, root := range n.rootsList() {
		rroot, err := filepath.Abs(expandTilde(root))
		if err != nil {
			continue
		}
		if rroot, err = filepath.EvalSymlinks(rroot); err != nil {
			continue
		}
		if !seen[rroot] {
			seen[rroot] = true
			rootOut = append(rootOut, rroot)
		}
		entries, err := os.ReadDir(rroot)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				dirs = append(dirs, rroot+"/"+e.Name())
			}
		}
	}
	sort.Strings(dirs)
	if len(dirs) > max {
		dirs = dirs[:max]
	}
	return &Reply{CmdID: m.CmdID, Result: ResSent, Detail: strings.Join(dirs, "\n"),
		Roots: rootOut, Home: n.homeDir()}, nil
}

// homeDir — домашний каталог пользователя узла (default-каталог формы
// «Новая сессия»). Определяет узел (ос.UserHomeDir).
func (n *Node) homeDir() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return h
}

// checkSettings — doctor (проверка ~/.qwen/settings.json узла, ТЗ «запуск
// сессий на узле выбранного пользователя»): read-only, ничего не правит.
// Проверки: security.auth.selectedType = openai и нет коллизии modelProviders
// (id == model_alias — кодер пойдёт на прямой baseUrl в обход шлюза).
// Result = PASS/FAIL/WARN, Detail — описание.
func (n *Node) checkSettings(ctx context.Context, m proto.Msg) (*Reply, error) {
	alias := n.cfg.Profiles.Qwen.ModelAlias
	userPath, err := qwenSettingsPath()
	if err != nil {
		return &Reply{CmdID: m.CmdID, Result: "WARN", Detail: "HOME не определён"}, nil
	}
	doc, err := readSettingsDoc(userPath)
	if err != nil {
		return &Reply{CmdID: m.CmdID, Result: "FAIL", Detail: userPath + ": " + err.Error()}, nil
	}
	if doc == nil {
		return &Reply{CmdID: m.CmdID, Result: "WARN", Detail: "нет " + userPath + " (не клиентская машина?)"}, nil
	}
	// security.auth.selectedType
	var sel any
	if sec, ok := doc["security"].(map[string]any); ok {
		if auth, ok := sec["auth"].(map[string]any); ok {
			sel = auth["selectedType"]
		}
	}
	if sel != "openai" {
		return &Reply{CmdID: m.CmdID, Result: "FAIL",
			Detail: userPath + ": security.auth.selectedType = " + fmt.Sprintf("%v", sel) + " (нужно openai)"}, nil
	}
	// modelProviders: коллизия id == model_alias → кодер в обход шлюза.
	if coll := providerIDCollision(doc, alias); len(coll) > 0 {
		return &Reply{CmdID: m.CmdID, Result: "FAIL",
			Detail: userPath + ": " + strings.Join(coll, "; ") + " — кодер пойдёт на прямой baseUrl (в обход шлюза)"}, nil
	}
	return &Reply{CmdID: m.CmdID, Result: "PASS", Detail: "selectedType=openai, modelProviders без коллизии"}, nil
}

// --- панельные операции (respawn / kill_session / release_pane / kill_process) ---

func (n *Node) respawn(ctx context.Context, socket string, m proto.Msg) (*Reply, error) {
	env := n.coderEnv(m.Env)
	// W15 этап 3: per-сессии system-settings.json (respawn — тот же SID).
	n.applySystemSettingsEnv(env, m.SID, m.InputModalities, m.ContextWindow)
	args := []string{"respawn-pane", "-k", "-t", m.PaneID}
	for _, k := range sortedEnvKeys(env) {
		args = append(args, "-e", k+"="+env[k])
	}
	args = append(args, n.coderCommand())
	args = append(args, CoderArgsFromEnv(env)...)
	// R0 ТЗ Relay §3: --resume <uuid> вместо --continue (кодер возобновляет запись).
	if m.QwenSessionID != "" {
		args = append(args, "--resume", m.QwenSessionID)
	}
	args = append(args, m.Args...)
	if _, err := n.ex.Output(ctx, "tmux", tmuxArgs(socket, args...)...); err != nil {
		return &Reply{CmdID: m.CmdID, Result: ResPaneGone}, nil
	}
	// очистить @runpilot_exit (13.5): сессия снова IDLE.
	_, _ = n.ex.Output(ctx, "tmux", tmuxArgs(socket, "set-option", "-p", "-u", "-t", m.PaneID, "@runpilot_exit")...)
	return &Reply{CmdID: m.CmdID, Result: proto.ResRespawned, Detail: m.PaneID}, nil
}

func (n *Node) killSession(ctx context.Context, socket string, m proto.Msg) (*Reply, error) {
	session := sessionOfPane(ctx, n.ex, socket, m.PaneID)
	if session == "" {
		return &Reply{CmdID: m.CmdID, Result: ResPaneGone}, nil
	}
	if _, err := n.ex.Output(ctx, "tmux", tmuxArgs(socket, "kill-session", "-t", "="+session)...); err != nil {
		return &Reply{CmdID: m.CmdID, Result: ResPaneGone}, nil
	}
	return &Reply{CmdID: m.CmdID, Result: proto.ResKilled, Detail: session}, nil
}

// releasePane — снять @runpilot_sid: панель остаётся живой как UNMANAGED (5.4).
func (n *Node) releasePane(ctx context.Context, socket string, m proto.Msg) (*Reply, error) {
	if _, err := n.ex.Output(ctx, "tmux", tmuxArgs(socket, "set-option", "-p", "-u", "-t", m.PaneID, "@runpilot_sid")...); err != nil {
		return &Reply{CmdID: m.CmdID, Result: ResPaneGone}, nil
	}
	return &Reply{CmdID: m.CmdID, Result: ResSent, Detail: m.PaneID}, nil
}

// --- adopt (8.3 X1): «Перезапустить под runpilot» ---

// coderInPane — жив ли в панели кодер профиля (совпадение cmdline_regex в
// дереве процессов). Неопределённо (сбой скана) → считаем живым (безопасно:
// respawn-pane -k закроет вопрос принудительно).
func (n *Node) coderInPane(ctx context.Context, socket, paneID string) bool {
	panes, _, err := ScanWithTable(ctx, n.ex, []string{socket}, n.prof.Cmdline())
	if err != nil {
		return true
	}
	for _, p := range panes {
		if p.PaneID == paneID {
			return p.Match && !p.Dead
		}
	}
	return false // панель исчезла — кодера нет
}

// adopt — принять панель с кодером, запущенным вне runpilot, в управление
// (8.3 X1, «Перезапустить под runpilot»): 1) панель UNMANAGED (нет @runpilot_sid)
// и IDLE (ТЗ: только IDLE); 2) quit_text + submit (операторский ввод) —
// штатный выход кодера, контекст разговора теряется (вебо предупреждает);
// 3) ожидание выхода (таймаут → принудительно); 4) respawn-pane -k с
// окружением профиля + @runpilot_sid.
func (n *Node) adopt(ctx context.Context, m proto.Msg) (*Reply, error) {
	socket, ok := n.socketFor(m.PaneID)
	if !ok {
		return &Reply{CmdID: m.CmdID, Result: ResPaneGone}, nil
	}
	info, err := n.cmd.paneInfo(ctx, socket, m.PaneID)
	if err != nil {
		return &Reply{CmdID: m.CmdID, Result: ResPaneGone}, nil
	}
	if info.SID != "" {
		// Уже управляется другой сессией.
		return &Reply{CmdID: m.CmdID, Result: proto.ResManaged, Detail: info.SID}, nil
	}
	st, _, _, err := capturePane(ctx, n.ex, n.regexps, info)
	if err != nil {
		return &Reply{CmdID: m.CmdID, Result: ResPaneGone}, nil
	}
	if st != detect.Idle {
		return &Reply{CmdID: m.CmdID, Result: proto.ResNotIdle, Detail: st.String()}, nil
	}
	// Штатный выход кодера: quit_text (побайто) + submit-клавиша профиля.
	if _, err := n.ex.Output(ctx, "tmux", tmuxArgs(socket, "send-keys", "-t", m.PaneID, "-l", n.prof.QuitText)...); err != nil {
		return &Reply{CmdID: m.CmdID, Result: "ADOPT_FAILED", Detail: "send-keys: " + err.Error()}, nil
	}
	if _, err := n.ex.Output(ctx, "tmux", tmuxArgs(socket, "send-keys", "-t", m.PaneID, n.prof.SubmitKeys)...); err != nil {
		return &Reply{CmdID: m.CmdID, Result: "ADOPT_FAILED", Detail: "submit: " + err.Error()}, nil
	}
	deadline := n.clk.Now().Add(adoptQuitWait)
	for n.clk.Now().Before(deadline) {
		if !n.coderInPane(ctx, socket, m.PaneID) {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(adoptPoll):
		}
	}
	// Перезапуск панели с окружением профиля (та же механика, что respawn).
	rep, err := n.respawn(ctx, socket, m)
	if err != nil {
		return nil, err
	}
	if rep.Result != proto.ResRespawned {
		return rep, nil
	}
	if _, err := n.ex.Output(ctx, "tmux", tmuxArgs(socket, "set-option", "-p", "-t", m.PaneID, "@runpilot_sid", m.SID)...); err != nil {
		return &Reply{CmdID: m.CmdID, Result: "ADOPT_FAILED", Detail: "set-option: " + err.Error()}, nil
	}
	return &Reply{CmdID: m.CmdID, Result: proto.ResAdopted, Detail: m.PaneID}, nil
}

func (n *Node) killProcess(ctx context.Context, m proto.Msg) (*Reply, error) {
	if m.PID <= 0 {
		return &Reply{CmdID: m.CmdID, Result: ResBadKeys, Detail: "pid<=0"}, nil
	}
	// 8.2: сверка идентичности (UID + start_time), иначе PROCESS_CHANGED/GONE.
	if bad := n.verifyKillTarget(ctx, m.PID, m.KProcUID, m.StartTime); bad != "" {
		return &Reply{CmdID: m.CmdID, Result: bad, Detail: "pid:" + strconv.Itoa(m.PID)}, nil
	}
	sig := m.Signal
	if sig == "" {
		sig = "TERM"
	}
	if _, err := n.ex.Output(ctx, "kill", sig, strconv.Itoa(m.PID)); err != nil {
		return &Reply{CmdID: m.CmdID, Result: ResBadKeys, Detail: err.Error()}, nil
	}
	return &Reply{CmdID: m.CmdID, Result: proto.ResKilled, Detail: sig + ":" + strconv.Itoa(m.PID)}, nil
}

func sessionOfPane(ctx context.Context, ex Execer, socket, paneID string) string {
	out, err := ex.Output(ctx, "tmux", tmuxArgs(socket, "display-message", "-p", "-t", paneID, "#{session_name}")...)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// --- config (14.4): project_roots + эндпоинт qwen code ---

func (n *Node) config(ctx context.Context, m proto.Msg) (*Reply, error) {
	if len(m.ProjectRoots) > 0 {
		n.SetProjectRoots(m.ProjectRoots)
	}
	// gateway_url координатора: единое правило «кодер вне очереди»
	// (ScanExternal). Координатор пушит реальное значение — удалённый узел с
	// минимальным конфигом иначе бы видел дефолт 127.0.0.1 и ложно сажал бы
	// кодеров панелей (условие (б) правила).
	if m.GatewayURL != "" {
		n.SetGatewayURL(m.GatewayURL)
	}
	// W15 этап 3: смена пула (модальности/контекст) → переписать per-сессии
	// system-settings.json живых сессий (перезапуск кодера — пользователю).
	if m.InputModalities != nil || m.ContextWindow > 0 {
		for _, sid := range n.snap.LiveSIDs() {
			n.writeSystemSettings(sid, m.InputModalities, m.ContextWindow)
		}
	}
	return &Reply{CmdID: m.CmdID, Result: ResSent}, nil
}

// --- uninstall (5.3): снять узел, не трогая tmux/кодеры ---

// tmuxBlockStart/End — маркеры блока runpilot в ~/.tmux.conf (дубликат
// cmd/runpilot/tmuxsetup.go: package main не импортируется из internal/node).
const (
	tmuxBlockStart = "# >>> runpilot >>>"
	tmuxBlockEnd   = "# <<< runpilot <<<"
)

// systemctlNodeArgs — аргументы systemctl для runpilot-node: user-юнит
// (дефолт) или system-юнит (root).
func systemctlNodeArgs(subcmd string) []string {
	if os.Geteuid() == 0 {
		return []string{subcmd, "runpilot-node"}
	}
	return []string{"--user", subcmd, "runpilot-node"}
}

func (n *Node) uninstall(ctx context.Context, m proto.Msg) (*Reply, error) {
	home, _ := os.UserHomeDir()
	// Синхронная очистка (до reply): disable сервиса (Restart=always → иначе
	// systemd перезапустит) + удаление конфига/secrets/tmux-блока/юнит-файла/
	// бинарника. На Linux удаление работающего ELF и unit-файла безопасно.
	_ = exec.Command("systemctl", systemctlNodeArgs("disable")...).Run()
	cfgDir := filepath.Join(home, ".config", "runpilot")
	_ = os.Remove(filepath.Join(cfgDir, "config.yaml"))
	_ = os.Remove(filepath.Join(cfgDir, "secrets.env"))
	_ = removeTmuxBlock(home)
	_ = os.Remove(filepath.Join(home, ".config", "systemd", "user", "runpilot-node.service"))
	_ = os.Remove("/etc/systemd/system/runpilot-node.service")
	if bin := n.selfBinPath(); bin != "" {
		_ = os.Remove(bin)
	}
	// reply уходит координатору в handleCommand после возврата. stop с
	// задержкой: он завершает процесс узла → только после reply.
	go func() {
		time.Sleep(uninstallStopDelay)
		cctx, cancel := context.WithTimeout(context.Background(), uninstallStopTimeout)
		defer cancel()
		_, _ = exec.CommandContext(cctx, "systemctl", systemctlNodeArgs("stop")...).CombinedOutput()
	}()
	return &Reply{CmdID: m.CmdID, Result: proto.ResUninstalled}, nil
}

// removeTmuxBlock — убрать блок runpilot (между маркерами) из ~/.tmux.conf.
func removeTmuxBlock(home string) error {
	path := filepath.Join(home, ".tmux.conf")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	s := string(data)
	start := strings.Index(s, tmuxBlockStart)
	end := strings.Index(s, tmuxBlockEnd)
	if start < 0 || end < 0 || end < start {
		return nil // блока нет
	}
	tail := s[end+len(tmuxBlockEnd):]
	if i := strings.IndexByte(tail, '\n'); i >= 0 {
		tail = tail[i+1:]
	}
	return os.WriteFile(path, []byte(s[:start]+tail), tmuxConfFilePerm)
}

// --- update (14.2): самообновление ---

// selftestFunc — точка подмены в тестах (runpilot.new selftest).
var selftestFunc = func(ctx context.Context, bin string, timeout time.Duration) error {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	_, err := execOutput(cctx, bin, "selftest")
	return err
}

// update — скачать бинарник по URL (Bearer-токен узла), сверить SHA-256,
// selftest, при успехе сохранить runpilot.prev + атомарно заменить и завершиться
// кодом 75 (systemd перезапустит); при неудаче — NODE_UPDATE_FAILED.
//
// W12 (ТЗ 14: «Автообновления aqm нет»): защита ручного self-update:
//   - целевая версия = текущей → NOOP (узел ничего не делает, не рестартится);
//   - откат на более старую версию (build_time target < current) → REFUSED,
//     допускается только при м.Downgrade=true;
//   - версия скачанного бинарника ≠ ожидаемой (m.Version) → REFUSED.
func (n *Node) update(ctx context.Context, m proto.Msg) (*Reply, error) {
	bin := n.selfBinPath()
	if bin == "" || m.URL == "" {
		return &Reply{CmdID: m.CmdID, Result: proto.ResUpdateFailed, Detail: "нет bin/url"}, nil
	}
	// Та же версия → no-op (не скачивать, не рестартироваться).
	if m.Version != "" && n.version != "" && m.Version == n.version {
		return &Reply{CmdID: m.CmdID, Result: proto.ResUpdateNoop, Detail: m.Version}, nil
	}
	// Откат на более старую версию (build_time target < current) — только с
	// downgrade=true. build_time=0 (e2e без git) — сравнение пропускается.
	if n.buildTimeUnix > 0 && m.BuildTime > 0 && m.BuildTime < n.buildTimeUnix && !m.Downgrade {
		return &Reply{CmdID: m.CmdID, Result: proto.ResUpdateRefused,
			Detail: "откат на более старую версию без downgrade=true"}, nil
	}
	newPath := bin + ".new"
	if err := n.download(ctx, m.URL, newPath); err != nil {
		_ = os.Remove(newPath)
		return &Reply{CmdID: m.CmdID, Result: proto.ResUpdateFailed, Detail: "download: " + err.Error()}, nil
	}
	// SHA-256 скачанного бинарника = ожидаемому.
	if m.SHA256 != "" {
		if h, err := sha256File(newPath); err != nil || hex.EncodeToString(h) != strings.ToLower(m.SHA256) {
			_ = os.Remove(newPath)
			return &Reply{CmdID: m.CmdID, Result: proto.ResUpdateFailed, Detail: "sha256 mismatch"}, nil
		}
	}
	if err := selftestFunc(ctx, newPath, n.selftestTimeout()); err != nil {
		_ = os.Remove(newPath)
		return &Reply{CmdID: m.CmdID, Result: proto.ResUpdateFailed, Detail: "selftest: " + err.Error()}, nil
	}
	// Версия скачанного бинарника = ожидаемой (защита от чужого/stale bинарника).
	// Если бинарник не runpilot (version не отвечает) — check пропускаем:
	// его ловит selftest (ниже). Валидный runpilot-бинарник с чужой версией —
	// REFUSED.
	if m.Version != "" {
		if v := binVersion(ctx, newPath); v != "" && v != m.Version {
			_ = os.Remove(newPath)
			return &Reply{CmdID: m.CmdID, Result: proto.ResUpdateRefused,
				Detail: fmt.Sprintf("версия бинарника %q != ожидаемой %q", v, m.Version)}, nil
		}
	}
	// успех: текущий → .prev, новый → bin, exit 75 (systemd/launchd перезапустит).
	if err := os.Rename(bin, bin+".prev"); err == nil {
		if err := os.Rename(newPath, bin); err != nil {
			_ = os.Rename(bin+".prev", bin) // откат
			return &Reply{CmdID: m.CmdID, Result: proto.ResUpdateFailed, Detail: "swap: " + err.Error()}, nil
		}
	}
	return &Reply{CmdID: m.CmdID, Result: proto.ResUpdated, Detail: m.Version}, nil
}

// binVersion — <bin> version (вывод). Для защиты self-update (W12): сверка
// версии скачанного бинарника с ожидаемой. Ошибка — "" (≠ m.Version → refused).
func binVersion(ctx context.Context, bin string) string {
	cctx, cancel := context.WithTimeout(ctx, binVersionTimeout)
	defer cancel()
	out, err := execOutput(cctx, bin, "version")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func (n *Node) download(ctx context.Context, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if n.nodeToken != "" {
		req.Header.Set("Authorization", "Bearer "+n.nodeToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("http %d", resp.StatusCode)
	}
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, binFilePerm)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}

func (n *Node) selftestTimeout() time.Duration {
	// enroll.selftest_timeout_sec, default 30.
	return time.Duration(n.cfg.Enroll.SelftestTimeoutSec) * time.Second
}

// selfBinPath — путь текущего бинарника узла (для swap при self-update).
func (n *Node) selfBinPath() string {
	if n.binPathOverride != "" {
		return n.binPathOverride
	}
	b, err := os.Executable()
	if err != nil {
		return ""
	}
	if rb, err := filepath.EvalSymlinks(b); err == nil {
		return rb
	}
	return b
}

// execOutput —CombinedOutput для selftest собственного бинарника (не через
// n.ex: selftest — это runpilot.new selftest, фиксированная своя команда).
func execOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

func sha256File(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}
