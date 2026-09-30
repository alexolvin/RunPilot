package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"text/template"
	"time"

	"github.com/spf13/cobra"

	"runpilot/internal/node"
	"runpilot/profiles"
)

// Коды коллизий runpilot run (раздел 8 ТЗ). Печатает клиент; HTTP-статус 409.
const (
	codeNameInUse          = "NAME_IN_USE"
	codeTmuxSessionExists  = "TMUX_SESSION_EXISTS"
	codePaneAlreadyManaged = "PANE_ALREADY_MANAGED"
)

// RunCollision — ошибка коллизии: код + 409-семантика.
type RunCollision struct {
	Code   string
	Detail string
}

func (e *RunCollision) Error() string { return "409 " + e.Code + ": " + e.Detail }

// newRunCmd — `runpilot run` (раздел 11 ТЗ): запуск кодера через координатор.
func newRunCmd() *cobra.Command {
	var (
		agent       string
		name        string
		dir         string
		prio        string
		pin         string
		prefer      string
		autoEnqueue bool
		detach      bool
		here        bool
	)
	cmd := &cobra.Command{
		Use:   "run [--agent qwen] [--name N] [--dir PATH] [--prio C] [--pin S|--prefer S] [--auto-enqueue] [--detach] [--here] [-- ARGS]",
		Short: "Запустить кодера под управлением runpilot",
		RunE: func(cmd *cobra.Command, args []string) error {
			if agent != "" && agent != "qwen" {
				return fmt.Errorf("--agent принимает только qwen (получено %q)", agent)
			}
			if pin != "" && prefer != "" {
				return fmt.Errorf("--pin и --prefer взаимоисключающие")
			}
			if here && detach {
				return fmt.Errorf("--here и --detach взаимоисключающие")
			}
			return runAgent(cmd, runOpts{
				name: name, dir: dir, prio: prio, pin: pin, prefer: prefer,
				autoEnqueue: autoEnqueue, detach: detach, here: here, args: args,
			})
		},
	}
	cmd.Flags().StringVar(&agent, "agent", "qwen", "агент (только qwen)")
	cmd.Flags().StringVar(&name, "name", "", "имя сессии (по умолчанию — имя каталога)")
	cmd.Flags().StringVar(&dir, "dir", ".", "рабочий каталог")
	cmd.Flags().StringVar(&prio, "prio", "normal", "класс очереди: resume|high|normal|low")
	cmd.Flags().StringVar(&pin, "pin", "", "привязка к серверу")
	cmd.Flags().StringVar(&prefer, "prefer", "", "предпочтение сервера")
	cmd.Flags().BoolVar(&autoEnqueue, "auto-enqueue", false, "автопостановка в очередь")
	cmd.Flags().BoolVar(&detach, "detach", false, "не присоединяться к панели")
	cmd.Flags().BoolVar(&here, "here", false, "текущая панель (exec агента)")
	return cmd
}

type runOpts struct {
	name        string
	dir         string
	prio        string
	pin         string
	prefer      string
	autoEnqueue bool
	detach      bool
	here        bool
	args        []string
}

// runAgent — полный сценарий runpilot run.
func runAgent(cmd *cobra.Command, o runOpts) error {
	prof, err := profiles.LoadQwen()
	if err != nil {
		return err
	}
	absDir, err := filepath.Abs(o.dir)
	if err != nil {
		return err
	}
	if o.name == "" {
		o.name = filepath.Base(absDir)
	}

	// 1) Регистрация в координаторе (коллизии имён — раздел 8 ТЗ).
	sid, err := registerRun(o)
	if err != nil {
		if errors.Is(err, errNoCoordinator) {
			// Координатор недоступен до старта qwen → 69 (X4 ТЗ).
			fmt.Fprintln(cmd.ErrOrStderr(), "runpilot run:", err)
			os.Exit(exitNoCoordinator)
		}
		return err
	}

	// 2) Окружение из шаблона профиля (раздел 7 ТЗ).
	env, err := profileEnv(prof, sid)
	if err != nil {
		return err
	}

	// 3) tmux.
	var paneID string
	if o.here {
		paneID, err = runHere(prof, sid, env, o.args)
	} else {
		paneID, err = runNewSession(o, prof, sid, env)
	}
	if err != nil {
		return err
	}

	// 4) Версия агента (узел сообщает её при runpilot run, раздел 7 ТЗ).
	agentVer := agentVersionLocal(prof.VersionCmd)

	// 5) Координатору — панель сессии.
	tmuxSession := o.name
	if o.here {
		tmuxSession = tmuxSessionOfPane(paneID)
	}
	if err := reportPane(sid, tmuxSession, paneID, agentVer); err != nil {
		return err
	}

	if !o.here && !o.detach {
		if os.Getenv("TMUX") != "" {
			tmuxMust("switch-client", "-t", o.name)
		} else {
			fmt.Fprintln(cmd.ErrOrStderr(), "runpilot run: присоединение к сессии", o.name)
			c := exec.Command("tmux", "attach-session", "-t", o.name)
			c.Stdout, c.Stderr, c.Stdin = os.Stdout, os.Stderr, os.Stdin
			_ = c.Run() // оператор может отстегнуться — это не ошибка
		}
	}
	fmt.Printf("sid %s  имя %s  панель %s\n", sid, o.name, paneID)
	return nil
}

// registerRun — POST /api/v1/run; 409-коды проходят как RunCollision.
func registerRun(o runOpts) (string, error) {
	host, _ := os.Hostname()
	body := map[string]any{
		"name": o.name, "host": host,
		"host_ip": node.HostIP(coordHostPort(cfg.Client.Coordinator)),
		"profile": "qwen",
		"prio":    o.prio, "pin": o.pin, "prefer": o.prefer,
		"auto_enqueue": o.autoEnqueue,
	}
	var out struct {
		SID string `json:"sid"`
	}
	code, detail, status, err := clientDo("POST", "/api/v1/run", body, &out)
	if err != nil {
		return "", err
	}
	if status == http.StatusConflict {
		return "", &RunCollision{Code: code, Detail: detail}
	}
	return out.SID, nil
}

// reportPane — PATCH /api/v1/sessions/<sid>/pane.
func reportPane(sid, tmuxSession, paneID, agentVer string) error {
	body := map[string]any{
		"tmux_session": tmuxSession, "pane_id": paneID,
		"agent_version": agentVer,
	}
	_, _, status, err := clientDo("PATCH", "/api/v1/sessions/"+sid+"/pane", body, nil)
	if err != nil {
		return err
	}
	if status >= httpNonOK {
		return fmt.Errorf("runpilot run: reportPane: HTTP %d", status)
	}
	return nil
}

// profileEnv — переменные окружения агента из шаблона профиля.
// Переменные оператора сохраняются (раздел 7 ТЗ).
func profileEnv(prof *profiles.Qwen, sid string) (map[string]string, error) {
	data := map[string]any{
		"Gateway":    cfg.Coordinator.GatewayURL,
		"SID":        sid,
		"ModelAlias": cfg.Profiles.Qwen.ModelAlias,
	}
	out := map[string]string{}
	for k, v := range prof.Env {
		var buf bytes.Buffer
		if err := template.Must(template.New("env").Parse(v)).Execute(&buf, data); err != nil {
			return nil, fmt.Errorf("runpilot run: шаблон env %s: %w", k, err)
		}
		out[k] = buf.String()
	}
	return out, nil
}

// runHere — `--here`: текущая панель, exec агента.
func runHere(prof *profiles.Qwen, sid string, env map[string]string, args []string) (string, error) {
	pane := os.Getenv("TMUX_PANE")
	if pane == "" {
		return "", errors.New("--here можно использовать только внутри tmux")
	}
	// Панель уже управляется runpilot → PANE_ALREADY_MANAGED.
	if cur := tmuxPaneOption(pane, "@runpilot_sid"); cur != "" {
		return "", &RunCollision{Code: codePaneAlreadyManaged,
			Detail: fmt.Sprintf("панель %s уже управляется сессией %s", pane, cur)}
	}
	// Кодер уже запущен без @runpilot_sid — не захватывать молча: печать
	// команды перезапуска (раздел 8 ТЗ).
	if tmuxPaneHasCoder(pane, prof.Cmdline()) {
		return "", &RunCollision{Code: codePaneAlreadyManaged,
			Detail: fmt.Sprintf("панель %s: кодер уже запущен без @runpilot_sid; перезапустите: tmux kill-pane -t %s && runpilot run --here", pane, pane)}
	}
	envPrefix := ""
	if len(env) > 0 {
		pairs := make([]string, 0, len(env))
		for k, v := range env {
			pairs = append(pairs, k+"="+v)
		}
		sort.Strings(pairs)
		envPrefix = strings.Join(pairs, " ") + " "
	}
	cmdline := envPrefix + prof.Command
	if len(args) > 0 {
		cmdline += " " + strings.Join(args, " ")
	}
	tmuxMust("send-keys", "-t", pane, "exec "+cmdline, "Enter")
	tmuxMust("set-option", "-p", "-t", pane, "@runpilot_sid", sid)
	return pane, nil
}

// runNewSession — новая tmux-сессия с агентом.
func runNewSession(o runOpts, prof *profiles.Qwen, sid string, env map[string]string) (string, error) {
	if tmuxSessionExists(o.name) {
		return "", &RunCollision{Code: codeTmuxSessionExists,
			Detail: "tmux-сессия с этим именем уже существует"}
	}
	args := []string{"new-session", "-d", "-s", o.name, "-c", o.dir}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "-e", k+"="+env[k])
	}
	args = append(args, prof.Command)
	args = append(args, o.args...)
	tmuxMust(args...)
	pane := tmuxFirstPane(o.name)
	tmuxMust("set-option", "-p", "-t", pane, "@runpilot_sid", sid)
	return pane, nil
}

// --- tmux-помощники клиента ---

func tmuxMust(args ...string) {
	if err := exec.Command("tmux", args...).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "runpilot run: tmux %v: %v\n", args, err)
		os.Exit(1)
	}
}

// tmuxSessionExists — есть ли сессия с именем.
func tmuxSessionExists(name string) bool {
	return exec.Command("tmux", "has-session", "-t", "="+name).Run() == nil
}

// tmuxFirstPane — id первой панели сессии.
func tmuxFirstPane(session string) string {
	out, err := exec.Command("tmux", "list-panes", "-t", session+":0", "-F", "#{pane_id}").Output()
	if err != nil {
		fmt.Fprintln(os.Stderr, "runpilot run: нет панели в сессии", session)
		os.Exit(1)
	}
	return strings.TrimSpace(string(out))
}

// tmuxSessionOfPane — имя tmux-сессии панели.
func tmuxSessionOfPane(pane string) string {
	out, err := exec.Command("tmux", "display-message", "-p", "-t", pane, "#{session_name}").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// tmuxPaneOption — значение опции панели.
func tmuxPaneOption(pane, opt string) string {
	out, err := exec.Command("tmux", "show-option", "-p", "-v", "-t", pane, opt).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// tmuxPaneHasCoder — запущен ли в панели кодер (cmdline_regex профиля).
func tmuxPaneHasCoder(pane string, cmdline interface{ MatchString(string) bool }) bool {
	pid := tmuxPanePID(pane)
	if pid == "" {
		return false
	}
	out, err := exec.Command("ps", "-A", "-o", "pid=", "-o", "ppid=", "-o", "args=").Output()
	if err != nil {
		return false
	}
	children := map[string][]string{}
	type proc struct{ pid, ppid, args string }
	var procs []proc
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < procMinFields {
			continue
		}
		p := proc{pid: f[0], ppid: f[1], args: strings.Join(f[procArgsStart:], " ")}
		procs = append(procs, p)
		children[p.ppid] = append(children[p.ppid], p.pid)
	}
	byID := map[string]proc{}
	for _, p := range procs {
		byID[p.pid] = p
	}
	// Сам процесс панели (production: `node /…/qwen`) и его потомки.
	if p, ok := byID[pid]; ok && cmdline.MatchString(p.args) {
		return true
	}
	queue := []string{pid}
	seen := map[string]bool{pid: true}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, c := range children[cur] {
			if seen[c] {
				continue
			}
			seen[c] = true
			queue = append(queue, c)
			if cmdline.MatchString(byID[c].args) {
				return true
			}
		}
	}
	return false
}

func tmuxPanePID(pane string) string {
	out, err := exec.Command("tmux", "display-message", "-p", "-t", pane, "#{pane_pid}").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// agentVersionLocal — версия агента (последний токен version_cmd).
func agentVersionLocal(versionCmd string) string {
	parts := strings.Fields(versionCmd)
	if len(parts) == 0 {
		return ""
	}
	out, err := exec.Command(parts[0], parts[1:]...).Output()
	if err != nil {
		return ""
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return ""
	}
	return fields[len(fields)-1]
}

// coordHostPort — host:port из адреса координатора.
func coordHostPort(u string) string {
	s := strings.TrimPrefix(u, httpScheme)
	return strings.TrimPrefix(s, httpsScheme)
}

// clientDo — запрос к координатору с Bearer-токеном.
// Возвращает code/detail из тела (для 409) и HTTP-статус.
func clientDo(method, path string, body, out any) (code, detail string, status int, err error) {
	var rd io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		rd = bytes.NewReader(data)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, cfg.Client.Coordinator+path, rd)
	if err != nil {
		return "", "", 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if tok := os.Getenv(cfg.Client.TokenEnv); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	client := &http.Client{Timeout: httpTimeoutSec * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		// Сеть/таймаут → координатор недоступен (→ код 69, X4 ТЗ).
		return "", "", 0, fmt.Errorf("runpilot run: координатор %s: %w", cfg.Client.Coordinator, errNoCoordinator)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if out != nil && resp.StatusCode < httpNonOK {
		if err := json.Unmarshal(data, out); err != nil {
			return "", "", resp.StatusCode, fmt.Errorf("runpilot run: разбор ответа: %w", err)
		}
		return "", "", resp.StatusCode, nil
	}
	var errBody struct {
		Code   string `json:"code"`
		Detail string `json:"detail"`
	}
	json.Unmarshal(data, &errBody)
	return errBody.Code, errBody.Detail, resp.StatusCode, nil
}
