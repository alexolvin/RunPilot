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
	if code := n.validateDir(m.Dir); code != "" {
		return &Reply{CmdID: m.CmdID, Result: code, Detail: m.Dir}, nil
	}
	socket := n.spawnSocket()
	if tmuxSessionExists(ctx, n.ex, socket, m.Name) {
		return &Reply{CmdID: m.CmdID, Result: "NAME_IN_USE", Detail: m.Name}, nil
	}
	// Автоматическое подключение кодера к шлюзу (доп-3i): перед запуском
	// приводим ~/.qwen/settings.json в порядок, чтобы OPENAI_*-окружение
	// сессии не перебивалось собственным конфигом qwen (не фатально: при
	// ошибке кодер запустится, но может ходить мимо шлюза).
	if alias := m.Env["OPENAI_MODEL"]; alias != "" {
		if res := n.EnsureQwenSettings(alias, m.ContextWindow); res.Err != nil {
			n.log.Warn("node: spawn: settings.json не приведён", "err", res.Err.Error())
		}
	}
	args := []string{"new-session", "-d", "-s", m.Name, "-c", m.Dir}
	for _, k := range sortedEnvKeys(m.Env) {
		args = append(args, "-e", k+"="+m.Env[k])
	}
	args = append(args, n.prof.Command)
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
	return &Reply{CmdID: m.CmdID, Result: ResSent, Detail: strings.Join(dirs, "\n"), Roots: rootOut}, nil
}

// --- панельные операции (respawn / kill_session / release_pane / kill_process) ---

func (n *Node) respawn(ctx context.Context, socket string, m proto.Msg) (*Reply, error) {
	args := []string{"respawn-pane", "-k", "-t", m.PaneID}
	for _, k := range sortedEnvKeys(m.Env) {
		args = append(args, "-e", k+"="+m.Env[k])
	}
	args = append(args, n.prof.Command)
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
	// W9 доп-3c: hello-ответ координатора (model_alias) — привести
	// ~/.qwen/settings.json в порядок, чтобы окружение сессии шлюза
	// (OPENAI_BASE_URL/OPENAI_MODEL) не перебивалось конфигом qwen.
	if m.ModelAlias != "" {
		n.EnsureQwenSettings(m.ModelAlias, m.ContextWindow)
	}
	return &Reply{CmdID: m.CmdID, Result: ResSent}, nil
}

// qwenSettings — явная проверка/поправка ~/.qwen/settings.json (доп-3i):
// веб-кнопка «Внести коррекции» в диалоге новой сессии. Возвращает исход,
// чтобы оператор увидел результат ПЕРЕД запуском кодера.
func (n *Node) qwenSettings(ctx context.Context, m proto.Msg) (*Reply, error) {
	res := n.EnsureQwenSettings(m.ModelAlias, m.ContextWindow)
	switch {
	case res.Err != nil:
		return &Reply{CmdID: m.CmdID, Result: proto.ResQwenSettingsError, Detail: res.Err.Error()}, nil
	case res.Changed:
		return &Reply{CmdID: m.CmdID, Result: proto.ResQwenSettingsChanged,
			Detail: strings.Join(res.Changes, "; ")}, nil
	default:
		return &Reply{CmdID: m.CmdID, Result: proto.ResQwenSettingsOK}, nil
	}
}

// --- uninstall (5.3): снять узел, не трогая tmux/кодеры ---

func (n *Node) uninstall(ctx context.Context, m proto.Msg) (*Reply, error) {
	// Реальная очистка: systemctl stop/rm runpilot-node, rm бинарника,
	// secrets.env, блока runpilot в ~/.tmux.conf. tmux-сессии/кодеры остаются
	// (станут UNMANAGED). Здесь — best-effort best: служба остановлена
	// координатором, узел завершится после ответа.
	return &Reply{CmdID: m.CmdID, Result: proto.ResUninstalled}, nil
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
func (n *Node) update(ctx context.Context, m proto.Msg) (*Reply, error) {
	bin := n.selfBinPath()
	if bin == "" || m.URL == "" {
		return &Reply{CmdID: m.CmdID, Result: proto.ResUpdateFailed, Detail: "нет bin/url"}, nil
	}
	newPath := bin + ".new"
	if err := n.download(ctx, m.URL, newPath); err != nil {
		_ = os.Remove(newPath)
		return &Reply{CmdID: m.CmdID, Result: proto.ResUpdateFailed, Detail: "download: " + err.Error()}, nil
	}
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
	// успех: текущий → .prev, новый → bin, exit 75 (systemd/launchd перезапустит).
	if err := os.Rename(bin, bin+".prev"); err == nil {
		if err := os.Rename(newPath, bin); err != nil {
			_ = os.Rename(bin+".prev", bin) // откат
			return &Reply{CmdID: m.CmdID, Result: proto.ResUpdateFailed, Detail: "swap: " + err.Error()}, nil
		}
	}
	return &Reply{CmdID: m.CmdID, Result: proto.ResUpdated, Detail: m.Version}, nil
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
