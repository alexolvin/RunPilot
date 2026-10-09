// Package doctor — каркас runpilot doctor (разделы 11, 13, 14 ТЗ):
// построчно PASS/FAIL/WARN, код выхода 1 при любом FAIL, 2 при ошибке конфига.
package doctor

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"runpilot/internal/config"
	"runpilot/internal/node"

	"github.com/gorilla/websocket"
)

// Level — результат проверки.
type Level int

const (
	PASS Level = iota
	WARN
	FAIL
)

func (l Level) String() string {
	switch l {
	case PASS:
		return "PASS"
	case WARN:
		return "WARN"
	case FAIL:
		return "FAIL"
	}
	return "?"
}

// Result — строка отчёта.
type Result struct {
	Check  string
	Level  Level
	Detail string
}

// String — «PASS <check> — <detail>».
func (r Result) String() string {
	return fmt.Sprintf("%s %s — %s", r.Level, r.Check, r.Detail)
}

// Context — данные для проверок.
type Context struct {
	Cfg     *config.Config
	CfgPath string
	// Cmdline — cmdline_regex профиля кодера (profiles.Qwen.Cmdline()); nil —
	// профиль не загружен, проверки деградируют до pane_pid.
	Cmdline *regexp.Regexp
	// ExecRoots — EXEC-корни живых сессий (PID процесса runpilot exec →
	// {время старта, sid}), из БД (store.LiveExecRoots). Для правила «кодер
	// вне очереди»: кодер управляем, если потомок корня и время старта совпадает.
	ExecRoots map[int]node.ExecRoot
	// StartTimeFn — время старта процесса (node.ProcStartTime); nil — default.
	// Подменяется в тестах.
	StartTimeFn func(pid int) int64
}

// Check — одна проверка.
type Check struct {
	Name string
	Run  func(*Context) Result
}

func result(name string, l Level, detail string) Result {
	return Result{Check: name, Level: l, Detail: detail}
}

// homeDir — переопределяется в тестах.
var homeDir = os.UserHomeDir

// tmuxListPanes / psOutput / readProcEnviron — точки инъекции для
// checkCoderTimeoutEnv / checkCoderOutOfQueue (в тестах подменяются без
// реального tmux, ps и /proc).
var (
	tmuxListPanes = func(socket string) ([]byte, error) {
		return exec.Command("tmux", "-L", socket, "list-panes", "-a",
			"-F", "#{pane_dead}\t#{pane_pid}\t#{session_name}\t#{@runpilot_sid}").Output()
	}
	// psOutput — таблица процессов «pid ppid args» (один вызов ps); из неё
	// строится дерево, чтобы найти ПРОЦЕСС КОДЕРА (потомок pane_pid, совпадающий
	// с cmdline_regex), а не обёртку.
	psOutput = func() ([]byte, error) {
		return exec.Command("ps", "-A", "-o", "pid=,ppid=,args=").Output()
	}
	readProcEnviron = func(pid string) ([]byte, error) {
		return os.ReadFile("/proc/" + pid + "/environ")
	}
	// procCwd — cwd процесса (readlink /proc/<pid>/cwd); для WARN «кодер вне
	// очереди» (checkCoderOutOfQueue).
	procCwd = func(pid string) string {
		b, err := os.Readlink("/proc/" + pid + "/cwd")
		if err != nil {
			return "?"
		}
		return b
	}
	// loginctlShowUser — вывод `loginctl show-user <user>`; для checkLinger.
	loginctlShowUser = func(u string) ([]byte, error) {
		return exec.Command("loginctl", "show-user", u).Output()
	}
	// currentUser — имя текущего пользователя; для checkLinger.
	currentUser = func() (string, error) {
		u, err := user.Current()
		if err != nil {
			return "", err
		}
		return u.Username, nil
	}
)

// Role — роль doctor: подмножество проверок, релевантное машине.
// runpilot doctor --role coordinator|node|client; без флага — все роли
// (DefaultChecks = объединение ролей + базовые config/secrets_env).
type Role string

const (
	RoleCoordinator Role = "coordinator"
	RoleNode        Role = "node"
	RoleClient      Role = "client"
)

// RoleChecks — проверки роли (раздел: роли doctor).
func RoleChecks(r Role) []Check {
	switch r {
	case RoleCoordinator:
		return []Check{
			{Name: "coordinator_bind", Run: checkBind},
			{Name: "allow_cidrs", Run: checkAllowCIDRs},
			{Name: "metrics", Run: checkMetrics},
			{Name: "require_direct", Run: checkRequireDirect},
			{Name: "log_rotation", Run: checkLogRotation},
		}
	case RoleClient:
		return []Check{
			{Name: "client_coordinator", Run: checkClientCoordinator},
		}
	case RoleNode:
		return []Check{
			{Name: "tmux", Run: checkTmux},
			{Name: "qwen_command", Run: checkQwenCommand},
			{Name: "qwen_settings", Run: checkQwenSettings},
			{Name: "qwen_timeout_guard", Run: checkQwenTimeoutGuard},
			{Name: "coder_timeout_env", Run: checkCoderTimeoutEnv},
			{Name: "coder_out_of_queue", Run: checkCoderOutOfQueue},
			{Name: "file_mode", Run: checkFileMode},
			{Name: "node_coordinator", Run: checkNodeCoordinator},
			{Name: "linger", Run: checkLinger},
		}
	}
	return nil
}

// DefaultChecks — все роли (без --role) + базовые config/secrets_env.
func DefaultChecks() []Check {
	out := []Check{
		{Name: "config", Run: checkConfig},
		{Name: "secrets_env", Run: checkSecretsEnv},
	}
	for _, r := range []Role{RoleCoordinator, RoleClient, RoleNode} {
		out = append(out, RoleChecks(r)...)
	}
	return out
}

// Run применяет проверки и собирает отчёт.
func Run(ctx *Context, checks []Check) []Result {
	out := make([]Result, 0, len(checks))
	for _, c := range checks {
		out = append(out, c.Run(ctx))
	}
	return out
}

// HasFail — есть ли хотя бы один FAIL.
func HasFail(rs []Result) bool {
	for _, r := range rs {
		if r.Level == FAIL {
			return true
		}
	}
	return false
}

func checkConfig(ctx *Context) Result {
	if ctx.Cfg == nil {
		return result("config", FAIL, "конфигурация не загружена")
	}
	return result("config", PASS, "загружен "+ctx.CfgPath)
}

func checkAllowCIDRs(ctx *Context) Result {
	if ctx.Cfg == nil {
		return result("allow_cidrs", FAIL, "нет конфигурации")
	}
	cidrs, err := config.ParseCIDRs(ctx.Cfg.Coordinator.AllowCIDRs)
	if err != nil {
		return result("allow_cidrs", FAIL, err.Error())
	}
	if len(cidrs) == 0 {
		return result("allow_cidrs", FAIL, "список пуст")
	}
	return result("allow_cidrs", PASS, fmt.Sprintf("%d CIDR", len(cidrs)))
}

// httpGet — HTTP-GET для проверок (client_coordinator): (status, error).
// Подменяется в тестах (без реального HTTP).
var httpGet = func(url string, token string) (int, error) {
	client := &http.Client{Timeout: clientCoordinatorTimeoutSec * time.Second}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode, nil
}

// checkClientCoordinator — client.coordinator (адрес координатора, который
// использует client: runpilot run/exec) отвечает и принимает токен. GET
// /api/v1/meta с операторским токеном: PASS только 2xx; 401/403 — FAIL
// «токен отклонён» (координатор жив, но токен плохой/не тот); прочие коды и
// ошибка соединения — FAIL с адресом и кодом.
func checkClientCoordinator(ctx *Context) Result {
	const name = "client_coordinator"
	if ctx.Cfg == nil {
		return result(name, FAIL, "нет конфигурации")
	}
	addr := strings.TrimRight(ctx.Cfg.Client.Coordinator, "/")
	if addr == "" {
		return result(name, FAIL, "client.coordinator не задан")
	}
	// «токен не задан» (переменная пуста) — отличаем от «токен отклонён»
	// (переданный токен отвергнут координатором).
	token := os.Getenv(ctx.Cfg.Client.TokenEnv)
	if token == "" {
		return result(name, FAIL, "токен не задан: "+ctx.Cfg.Client.TokenEnv)
	}
	status, err := httpGet(addr+"/api/v1/meta", token)
	if err != nil {
		return result(name, FAIL, "не отвечает "+addr+": "+err.Error())
	}
	switch {
	case status >= http2xxLow && status < http2xxHigh:
		return result(name, PASS, "отвечает "+addr)
	case status == httpUnauthorized || status == httpForbidden:
		return result(name, FAIL,
			"токен отклонён "+addr+" (HTTP "+strconv.Itoa(status)+")")
	default:
		return result(name, FAIL, fmt.Sprintf("HTTP %d: %s", status, addr))
	}
}

// wsDial — WS-dial /api/v1/node с Bearer-токеном; handshake (upgrade) +
// закрытие соединения. Возврат: (принят, http-статус, error). При отклонённом
// токене принят=false, статус=401/403. Подменяется в тестах.
var wsDial = func(wsURL, token string) (accepted bool, status int, err error) {
	d := websocket.Dialer{HandshakeTimeout: clientCoordinatorTimeoutSec * time.Second}
	hdr := http.Header{}
	if token != "" {
		hdr.Set("Authorization", "Bearer "+token)
	}
	conn, resp, derr := d.Dial(wsURL, hdr)
	if derr != nil {
		if resp != nil {
			return false, resp.StatusCode, derr
		}
		return false, 0, derr
	}
	_ = conn.Close() // токен принят (upgrade); hello не шлём — узел не регистрируется
	return true, httpSwitchingProtocols, nil
}

// coordWSURL — http(s)://host:port → ws(s)://host:port/api/v1/node.
func coordWSURL(coordURL string) string {
	u, err := url.Parse(coordURL)
	if err != nil {
		u = &url.URL{Scheme: "http", Host: coordURL}
	}
	scheme := "ws"
	if u.Scheme == "https" {
		scheme = "wss"
	}
	u.Scheme = scheme
	u.Path = "/api/v1/node"
	u.RawQuery = ""
	return u.String()
}

// nodeTokenValue — токен узла: env (EnvironmentFile службы, source'нут
// upgrade-node.sh) → ~/.config/runpilot/secrets.env → env.
func nodeTokenValue(envName string) string {
	if v := os.Getenv(envName); v != "" {
		return v
	}
	home, err := homeDir()
	if err != nil {
		return ""
	}
	for _, f := range []string{"secrets.env", "env"} {
		data, err := os.ReadFile(filepath.Join(home, ".config", "runpilot", f))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			if v, ok := strings.CutPrefix(line, envName+"="); ok {
				return strings.TrimSpace(v)
			}
		}
	}
	return ""
}

// checkNodeCoordinator — handshake узла: WS-dial /api/v1/node с Bearer-токеном
// узла (из EnvironmentFile службы узла). Токен принят (upgrade) → PASS;
// 401/403 → FAIL «токен отклонён»; токен пуст → FAIL «токен не задан: <var>»;
// ошибка соединения → FAIL «не отвечает».
func checkNodeCoordinator(ctx *Context) Result {
	const name = "node_coordinator"
	if ctx.Cfg == nil {
		return result(name, FAIL, "нет конфигурации")
	}
	addr := strings.TrimRight(ctx.Cfg.Client.Coordinator, "/")
	if addr == "" {
		return result(name, FAIL, "client.coordinator не задан")
	}
	envName := ctx.Cfg.Client.TokenEnv
	token := nodeTokenValue(envName)
	if token == "" {
		return result(name, FAIL, "токен не задан: "+envName)
	}
	accepted, status, err := wsDial(coordWSURL(addr), token)
	if err != nil {
		switch {
		case status == httpUnauthorized || status == httpForbidden:
			return result(name, FAIL,
				"токен отклонён "+addr+" (HTTP "+strconv.Itoa(status)+")")
		case status != 0:
			return result(name, FAIL, fmt.Sprintf("HTTP %d: %s", status, addr))
		default:
			return result(name, FAIL, "не отвечает "+addr+": "+err.Error())
		}
	}
	if !accepted {
		return result(name, FAIL, "handshake не принят "+addr)
	}
	return result(name, PASS, "handshake принят "+addr)
}

// checkLinger — узел работает как systemd user-сервис; без linger user@UID
// останавливается при logout и сервис узла умирает. `loginctl show-user
// <user>` → Linger=yes, иначе FAIL с командой включения.
func checkLinger(ctx *Context) Result {
	const name = "linger"
	user, err := currentUser()
	if err != nil {
		return result(name, WARN, "пользователь не определён")
	}
	out, err := loginctlShowUser(user)
	if err != nil {
		return result(name, FAIL, "loginctl show-user "+user+": "+err.Error())
	}
	if !strings.Contains(string(out), "Linger=yes") {
		return result(name, FAIL,
			"linger не включён для "+user+" (systemctl --enable-linger "+user+")")
	}
	return result(name, PASS, "linger включён для "+user)
}

func checkBind(ctx *Context) Result {
	if ctx.Cfg == nil {
		return result("coordinator_bind", FAIL, "нет конфигурации")
	}
	if err := ctx.Cfg.ValidateBind(); err != nil {
		return result("coordinator_bind", FAIL, err.Error())
	}
	return result("coordinator_bind", PASS, "bind "+ctx.Cfg.Coordinator.Bind+" входит в allow_cidrs")
}

var envNameRe = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

// checkSecretsEnv — секреты только в переменных окружения (раздел 14):
// все *_env должны быть именами переменных, а не значениями.
func checkSecretsEnv(ctx *Context) Result {
	if ctx.Cfg == nil {
		return result("secrets_env", FAIL, "нет конфигурации")
	}
	var bad []string
	check := func(what, name string) {
		if name == "" || !envNameRe.MatchString(name) {
			bad = append(bad, what+"="+name)
		}
	}
	check("coordinator.token_env", ctx.Cfg.Coordinator.TokenEnv)
	check("client.token_env", ctx.Cfg.Client.TokenEnv)
	check("notify.telegram.bot_token_env", ctx.Cfg.Notify.Telegram.BotTokenEnv)
	for _, s := range ctx.Cfg.Servers {
		check("servers."+s.Name+".key_env", s.Upstreams.OpenAI.KeyEnv)
	}
	if len(bad) > 0 {
		return result("secrets_env", FAIL, "не имена переменных: "+strings.Join(bad, ", "))
	}
	return result("secrets_env", PASS, "все секреты — через переменные окружения")
}

// checkTmux — tmux ≥ минимальной версии (раздел 11: минимум 3.2).
func checkTmux(ctx *Context) Result {
	path, err := exec.LookPath("tmux")
	if err != nil {
		return result("tmux", WARN, "tmux не найден (на координаторе не обязателен)")
	}
	out, err := exec.Command(path, "-V").Output()
	if err != nil {
		return result("tmux", FAIL, "tmux -V: "+err.Error())
	}
	major, minor, ok := parseTmuxVersion(string(out))
	if !ok {
		return result("tmux", FAIL, "не удалось разобрать версию: "+strings.TrimSpace(string(out)))
	}
	if major > tmuxMinMajor || (major == tmuxMinMajor && minor >= tmuxMinMinor) {
		return result("tmux", PASS, strings.TrimSpace(string(out)))
	}
	return result("tmux", FAIL, fmt.Sprintf("%d.%d < 3.2", major, minor))
}

// parseTmuxVersion разбирает «tmux 3.2a» → (3, 2, true).
func parseTmuxVersion(out string) (int, int, bool) {
	fields := strings.Fields(out)
	if len(fields) < tmuxVersionFields {
		return 0, 0, false
	}
	parts := strings.SplitN(fields[len(fields)-1], ".", tmuxVersionFields)
	if len(parts) != tmuxVersionFields {
		return 0, 0, false
	}
	num := func(s string) int {
		i := 0
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if i == 0 {
			return -1
		}
		n, _ := strconv.Atoi(s[:i])
		return n
	}
	major, minor := num(parts[0]), num(parts[1])
	if major < 0 || minor < 0 {
		return 0, 0, false
	}
	return major, minor, true
}

// checkQwenSettings — клиентская проверка (раздел 7 ТЗ): в
// ~/.qwen/settings.json задано security.auth.selectedType = "openai" и
// modelProviders не содержит id == model_alias (иначе кодер пойдёт на прямой
// baseUrl в обход шлюза). Проверяются пользовательский (~/.qwen/settings.json)
// И проектный (<cwd>/.qwen/settings.json) файлы.
func checkQwenSettings(ctx *Context) Result {
	alias := ""
	if ctx.Cfg != nil {
		alias = ctx.Cfg.Profiles.Qwen.ModelAlias
	}
	home, err := homeDir()
	if err != nil {
		return result("qwen_settings", WARN, "HOME не определён")
	}
	userPath := filepath.Join(home, ".qwen", "settings.json")
	data, err := os.ReadFile(userPath)
	if err != nil {
		if os.IsNotExist(err) {
			return result("qwen_settings", WARN, "нет "+userPath+" (не клиентская машина?)")
		}
		return result("qwen_settings", FAIL, userPath+": "+err.Error())
	}
	var d map[string]any
	if err := json.Unmarshal(data, &d); err != nil {
		return result("qwen_settings", FAIL, userPath+": не JSON: "+err.Error())
	}
	// security.auth.selectedType
	var sel any
	if sec, ok := d["security"].(map[string]any); ok {
		if auth, ok := sec["auth"].(map[string]any); ok {
			sel = auth["selectedType"]
		}
	}
	if sel != "openai" {
		return result("qwen_settings", FAIL,
			userPath+": security.auth.selectedType = "+fmt.Sprintf("%v", sel)+" (нужно openai)")
	}
	// modelProviders (политика 2026-10-02): записи оператора остаются
	// живыми — в /model кодера доступны; шлюз по умолчанию (model_alias из
	// env — Runtime Model, baseUrl из OPENAI_BASE_URL). Опасна только
	// коллизия: id записи == model_alias превращает текущую модель в
	// Provider Model — прямой baseUrl в обход шлюза.
	collisions, providers := settingsCollision(d, alias)
	if collisions > 0 {
		return result("qwen_settings", FAIL,
			fmt.Sprintf("%s: в modelProviders %d записей с id == model_alias — кодер пойдёт на прямой baseUrl (в обход шлюза); смените model_alias или id записей",
				userPath, collisions))
	}
	// Проектный settings.json (<cwd>/.qwen/settings.json): коллизия id ==
	// model_alias уводит кодера на прямой baseUrl в обход шлюза.
	projPath := filepath.Join(".qwen", "settings.json")
	if pData, perr := os.ReadFile(projPath); perr == nil {
		var pd map[string]any
		if err := json.Unmarshal(pData, &pd); err == nil {
			if pcollisions, _ := settingsCollision(pd, alias); pcollisions > 0 {
				return result("qwen_settings", FAIL,
					fmt.Sprintf("%s: в modelProviders %d записей с id == model_alias — кодер пойдёт на прямой baseUrl (в обход шлюза); смените model_alias или id записей",
						projPath, pcollisions))
			}
		}
	}
	if providers > 0 {
		return result("qwen_settings", PASS,
			fmt.Sprintf("selectedType=openai, modelProviders: %d эндов без коллизии (шлюз по умолчанию, переключение в /model)", providers))
	}
	return result("qwen_settings", PASS, "selectedType=openai, modelProviders пуст (кодер через шлюз)")
}

// checkQwenCommand — путь к кодеру (profiles.qwen.command): пусто — command
// профиля (PASS); задан — файл существует и `<command> --version` выполняется
// в окружении службы (PATH = каталог кодера + текущий). Файл отсутствует →
// FAIL с путём.
func checkQwenCommand(ctx *Context) Result {
	const check = "qwen_command"
	cmd := ""
	if ctx.Cfg != nil {
		cmd = ctx.Cfg.Profiles.Qwen.Command
	}
	if cmd == "" {
		return result(check, PASS, "profiles.qwen.command пуст (command профиля)")
	}
	if _, err := os.Stat(cmd); err != nil {
		return result(check, FAIL, cmd+": нет файла")
	}
	// PATH = каталог кодера (там node из nvm) + текущий PATH.
	c := exec.Command(cmd, "--version")
	c.Env = append(os.Environ(), "PATH="+filepath.Dir(cmd)+":"+os.Getenv("PATH"))
	out, err := c.Output()
	if err != nil {
		return result(check, FAIL, cmd+": "+err.Error())
	}
	return result(check, PASS, cmd+": "+strings.TrimSpace(string(out)))
}

// settingsCollision — считает коллизии (id == alias) и общее число записей
// modelProviders в одном settings.json-документе.
func settingsCollision(doc map[string]any, alias string) (collisions, providers int) {
	mp, ok := doc["modelProviders"].(map[string]any)
	if !ok {
		return 0, 0
	}
	for _, modelsAny := range mp {
		models, ok := modelsAny.([]any)
		if !ok {
			continue
		}
		for _, entryAny := range models {
			entry, ok := entryAny.(map[string]any)
			if !ok {
				continue
			}
			id, _ := entry["id"].(string)
			if id == "" {
				continue
			}
			providers++
			if alias != "" && id == alias {
				collisions++
			}
		}
	}
	return collisions, providers
}

// errStopWalk — маркер раннего выхода из qwenPkgContains.
var errStopWalk = errors.New("stop")

// checkQwenTimeoutGuard — п.8, клиентская проверка: в пакете установленного
// qwen есть строка QWEN_CODE_API_TIMEOUT_MS (клиент поддерживает долгий
// таймаут генерирующего запроса — без него шлюз не удержит запрос
// hold_max_sec). Активная версия ищется по ~/.qwen/updates/npm/*/active.json
// (managed npm, как scripts/patch_qwen_runpilot.py). Нет строки → FAIL с
// версией qwen и путём пакета.
func checkQwenTimeoutGuard(ctx *Context) Result {
	const check = "qwen_timeout_guard"
	home, err := homeDir()
	if err != nil {
		return result(check, WARN, "HOME не определён")
	}
	updBase := filepath.Join(home, ".qwen", "updates", "npm")
	actives, _ := filepath.Glob(filepath.Join(updBase, "*", "active.json"))
	if len(actives) == 0 {
		return result(check, WARN,
			"active.json не найден (авто-обновление qwen не использовалось?)")
	}
	var act struct{ Version string }
	data, err := os.ReadFile(actives[0])
	if err != nil {
		return result(check, FAIL, actives[0]+": "+err.Error())
	}
	if err := json.Unmarshal(data, &act); err != nil {
		return result(check, FAIL, actives[0]+": не JSON: "+err.Error())
	}
	pkg := filepath.Join(filepath.Dir(actives[0]), "versions", act.Version,
		"node_modules", "@qwen-code", "qwen-code")
	if fi, err := os.Stat(pkg); err != nil || !fi.IsDir() {
		return result(check, FAIL,
			fmt.Sprintf("qwen %s: пакет не найден: %s", act.Version, pkg))
	}
	if !qwenPkgContains(pkg, "QWEN_CODE_API_TIMEOUT_MS") {
		return result(check, FAIL, fmt.Sprintf(
			"qwen %s: строки QWEN_CODE_API_TIMEOUT_MS нет в пакете %s — клиент не поддержит удержание запроса",
			act.Version, pkg))
	}
	return result(check, PASS,
		fmt.Sprintf("qwen %s: QWEN_CODE_API_TIMEOUT_MS присутствует (%s)", act.Version, pkg))
}

// qwenPkgContains — есть ли подстрока в каком-либо .js файле пакета
// (рекурсивно, не заглядывая в зависимости node_modules).
func qwenPkgContains(pkg, substr string) bool {
	found := false
	_ = filepath.WalkDir(pkg, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // нечитаемое пропускаем
		}
		if d.IsDir() {
			if d.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".js") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		if strings.Contains(string(data), substr) {
			found = true
			return errStopWalk
		}
		return nil
	})
	return found
}

// checkFileMode — файлы БД и конфигурации с режимом 0600 (раздел 14).
func checkFileMode(ctx *Context) Result {
	var checked, bad []string
	check := func(p string) {
		if p == "" {
			return
		}
		fi, err := os.Stat(p)
		if err != nil {
			return // файла ещё нет — создаст serve/node
		}
		checked = append(checked, p)
		if m := fi.Mode().Perm(); m != secretFileMode {
			bad = append(bad, fmt.Sprintf("%s=%o", p, m))
		}
	}
	check(ctx.CfgPath)
	if ctx.Cfg != nil {
		if db, err := ctx.Cfg.DBPath(); err == nil {
			check(db)
		}
	}
	if len(bad) > 0 {
		return result("file_mode", FAIL, "не 0600: "+strings.Join(bad, ", "))
	}
	if len(checked) == 0 {
		return result("file_mode", PASS, "файлы ещё не созданы")
	}
	return result("file_mode", PASS, "0600: "+strings.Join(checked, ", "))
}

// checkCoderTimeoutEnv — у каждой живой УПРАВЛЯЕМОЙ сессии узла (панель с
// @runpilot_sid) в env ПРОЦЕССА КОДЕРА есть QWEN_CODE_API_TIMEOUT_MS. Кодер —
// потомок pane_pid, совпадающий с cmdline_regex профиля (BFS по ps-дереву; после
// bash→qwen exec кодером часто бывает сам pane_pid — тогда совпадение на корне).
// Нет переменной → WARN (не FAIL): предпроверка upgrade-node.sh не должна
// блокировать обновление узла со СТАРЫМИ сессиями, запущенными до фикса spawnEnv.
func checkCoderTimeoutEnv(ctx *Context) Result {
	const name = "coder_timeout_env"
	if ctx.Cfg == nil {
		return result(name, WARN, "нет конфигурации — проверку пропускаем")
	}
	sockets := ctx.Cfg.Node.TmuxSockets
	if len(sockets) == 0 {
		return result(name, PASS, "нет управляемых сессий (tmux_sockets пуст)")
	}
	type managedPane struct{ pid, sessName, sid string }
	var panes []managedPane
	for _, socket := range sockets {
		out, err := tmuxListPanes(socket)
		if err != nil {
			continue // нет tmux-сервера на сокете — управляемых сессий нет
		}
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if line == "" {
				continue
			}
			f := strings.Split(line, "\t")
			if len(f) < paneFieldCount || f[paneFieldSID] == "" || f[paneFieldDead] == paneDeadFlag {
				continue // не управляемая (нет @runpilot_sid) или мёртвая панель
			}
			panes = append(panes, managedPane{pid: f[paneFieldPID], sessName: f[paneFieldName], sid: f[paneFieldSID]})
		}
	}
	if len(panes) == 0 {
		return result(name, PASS, "нет живых управляемых сессий")
	}
	psOut, _ := psOutput()
	tree := buildProcTree(psOut)
	var missing []string
	for _, p := range panes {
		pid, err := strconv.Atoi(p.pid)
		if err != nil {
			missing = append(missing, fmt.Sprintf("%s (%s)", p.sid, p.sessName))
			continue
		}
		coder := tree.coderPID(pid, ctx.Cmdline)
		if coder == 0 {
			coder = pid // дерево пустое/нет совпадения — деградируем до pane_pid
		}
		environ, eerr := readProcEnviron(strconv.Itoa(coder))
		if eerr != nil || !procEnvHas(environ, "QWEN_CODE_API_TIMEOUT_MS") {
			missing = append(missing, fmt.Sprintf("%s (%s)", p.sid, p.sessName))
		}
	}
	if len(missing) > 0 {
		return result(name, WARN, "нет QWEN_CODE_API_TIMEOUT_MS: "+strings.Join(missing, ", "))
	}
	return result(name, PASS, fmt.Sprintf("все %d управляемых сессий: переменная на месте", len(panes)))
}

// procTree — дерево процессов из одного вывода `ps -A -o pid=,ppid=,args=`.
type procTree struct {
	args     map[int]string
	children map[int][]int
	ppid     map[int]int // parent PID (для обхода ВВЕРХ — единое правило SessionSID)
}

// buildProcTree парсит вывод ps в дерево (args — всё после ppid, spaces схлопнуты).
func buildProcTree(psOut []byte) *procTree {
	t := &procTree{args: map[int]string{}, children: map[int][]int{}, ppid: map[int]int{}}
	for _, line := range strings.Split(strings.TrimSpace(string(psOut)), "\n") {
		if line == "" {
			continue
		}
		f := strings.Fields(line)
		if len(f) < psMinFields {
			continue
		}
		pid, err1 := strconv.Atoi(f[psFieldPID])
		ppid, err2 := strconv.Atoi(f[psFieldPPID])
		if err1 != nil || err2 != nil {
			continue
		}
		t.args[pid] = strings.Join(f[psFieldArgsStart:], " ")
		t.ppid[pid] = ppid
		t.children[ppid] = append(t.children[ppid], pid)
	}
	return t
}

// coderPID — первый в BFS-порядке процесс в поддереве под root (включая root),
// чей args совпадает с re. re==nil → root; нет совпадения → 0.
func (t *procTree) coderPID(root int, re *regexp.Regexp) int {
	if re == nil {
		return root
	}
	if re.MatchString(t.args[root]) {
		return root
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
				return c
			}
		}
	}
	return 0
}

// checkCoderOutOfQueue — «кодер вне очереди» (единое правило node.CoderOutOfQueue,
// то же, что веб через node.ScanExternal): qwen-процесс (cmdline_regex), НЕ
// (потомок панели живой сессии И OPENAI_BASE_URL = gateway_url/s/<sid>/v1).
// Ручной запуск (нет панели), qwen под vLLM, URL с чужим sid — вне очереди.
// WARN (не FAIL) с PID и cwd.
func checkCoderOutOfQueue(ctx *Context) Result {
	const name = "coder_out_of_queue"
	if ctx.Cfg == nil {
		return result(name, WARN, "нет конфигурации — проверку пропускаем")
	}
	if ctx.Cmdline == nil {
		return result(name, PASS, "cmdline_regex не загружен — проверку пропускаем")
	}
	gatewayURL := ctx.Cfg.Coordinator.GatewayURL
	// Панели живых сессий: pane_pid(int) → sid (PANE-корни).
	panePIDToSID := map[int]string{}
	for _, socket := range ctx.Cfg.Node.TmuxSockets {
		out, err := tmuxListPanes(socket)
		if err != nil {
			continue // нет tmux-сервера на сокете — управляемых сессий нет
		}
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if line == "" {
				continue
			}
			f := strings.Split(line, "\t")
			if len(f) < paneFieldCount || f[paneFieldSID] == "" || f[paneFieldDead] == paneDeadFlag {
				continue // не управляемая (нет @runpilot_sid) или мёртвая панель
			}
			pid, err := strconv.Atoi(f[paneFieldPID])
			if err != nil {
				continue
			}
			panePIDToSID[pid] = f[paneFieldSID]
		}
	}
	psOut, _ := psOutput()
	tree := buildProcTree(psOut)
	type ooq struct{ pid int; cwd string }
	var found []ooq
	for pid, args := range tree.args {
		if !ctx.Cmdline.MatchString(args) {
			continue
		}
		var openAIBaseURL string
		if environ, err := readProcEnviron(strconv.Itoa(pid)); err == nil {
			openAIBaseURL = procEnvValue(environ, "OPENAI_BASE_URL")
		}
		// Единый вывод sid (PANE-панель / EXEC-корень PID+время старта) + правило
		// — тот же node.SessionSID + node.CoderOutOfQueue, что и веб (ScanExternal).
		// EXEC-корни — из БД (ctx.ExecRoots); время старта — StartTimeFn (default
		// node.ProcStartTime).
		starttimeOf := ctx.StartTimeFn
		if starttimeOf == nil {
			starttimeOf = node.ProcStartTime
		}
		sid := node.SessionSID(pid, panePIDToSID, ctx.ExecRoots, starttimeOf,
			func(p int) int { return tree.ppid[p] })
		if !node.CoderOutOfQueue(sid, openAIBaseURL, gatewayURL) {
			continue // кодер живой сессии с верным URL шлюза — управляемый
		}
		found = append(found, ooq{pid: pid, cwd: procCwd(strconv.Itoa(pid))})
	}
	if len(found) == 0 {
		return result(name, PASS, "кодеров вне очереди нет")
	}
	sort.Slice(found, func(i, j int) bool { return found[i].pid < found[j].pid })
	parts := make([]string, 0, len(found))
	for _, f := range found {
		parts = append(parts, fmt.Sprintf("%d (cwd=%s)", f.pid, f.cwd))
	}
	return result(name, WARN, "кодер вне очереди: "+strings.Join(parts, ", "))
}

// procEnvHas — есть ли переменная в NUL-разделённом /proc/<pid>/environ.
func procEnvHas(environ []byte, key string) bool {
	for _, kv := range strings.Split(string(environ), "\x00") {
		if strings.HasPrefix(kv, key+"=") {
			return true
		}
	}
	return false
}

// procEnvValue — значение переменной из NUL-разделённого /proc/<pid>/environ
// ("" если нет). Для условия (б) единого правила «кодер вне очереди».
func procEnvValue(environ []byte, key string) string {
	for _, kv := range strings.Split(string(environ), "\x00") {
		if strings.HasPrefix(kv, key+"=") {
			return strings.TrimPrefix(kv, key+"=")
		}
	}
	return ""
}

// checkLogRotation — ротация лога координатора настроена (log.max_mb > 0).
func checkLogRotation(ctx *Context) Result {
	if ctx.Cfg.Log.MaxMB <= 0 {
		return result("log_rotation", WARN, "log.max_mb не задан — лог растёт без ограничений")
	}
	detail := fmt.Sprintf("max_mb=%d, backups=%d", ctx.Cfg.Log.MaxMB, ctx.Cfg.Log.Backups)
	if ctx.Cfg.Log.Path == "" {
		detail += ", path=stderr"
	}
	return result("log_rotation", PASS, detail)
}
