// Package doctor — каркас runpilot doctor (разделы 11, 13, 14 ТЗ):
// построчно PASS/FAIL/WARN, код выхода 1 при любом FAIL, 2 при ошибке конфига.
package doctor

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"runpilot/internal/config"
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

// DefaultChecks — проверки каркаса (Э0/Э2) + мониторинг (Э6).
func DefaultChecks() []Check {
	return []Check{
		{Name: "config", Run: checkConfig},
		{Name: "allow_cidrs", Run: checkAllowCIDRs},
		{Name: "coordinator_bind", Run: checkBind},
		{Name: "secrets_env", Run: checkSecretsEnv},
		{Name: "tmux", Run: checkTmux},
		{Name: "qwen_settings", Run: checkQwenSettings},
		{Name: "file_mode", Run: checkFileMode},
		{Name: "metrics", Run: checkMetrics},
		{Name: "require_direct", Run: checkRequireDirect},
	}
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
// ~/.qwen/settings.json задано security.auth.selectedType = "openai" и нет
// modelProviders с id, равным ModelAlias. Нарушение — FAIL с путём файла
// и ключом.
func checkQwenSettings(ctx *Context) Result {
	home, err := homeDir()
	if err != nil {
		return result("qwen_settings", WARN, "HOME не определён")
	}
	path := filepath.Join(home, ".qwen", "settings.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return result("qwen_settings", WARN, "нет "+path+" (не клиентская машина?)")
		}
		return result("qwen_settings", FAIL, path+": "+err.Error())
	}
	var d map[string]any
	if err := json.Unmarshal(data, &d); err != nil {
		return result("qwen_settings", FAIL, path+": не JSON: "+err.Error())
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
			path+": security.auth.selectedType = "+fmt.Sprintf("%v", sel)+" (нужно openai)")
	}
	// modelProviders: provider → []{id: …}; id, равный ModelAlias, запрещён.
	alias := ""
	if ctx.Cfg != nil {
		alias = ctx.Cfg.Profiles.Qwen.ModelAlias
	}
	if mp, ok := d["modelProviders"].(map[string]any); ok {
		for prov, modelsAny := range mp {
			models, ok := modelsAny.([]any)
			if !ok {
				continue
			}
			for i, mAny := range models {
				m, ok := mAny.(map[string]any)
				if !ok {
					continue
				}
				if id, _ := m["id"].(string); id == alias && alias != "" {
					return result("qwen_settings", FAIL,
						fmt.Sprintf("%s: modelProviders.%s[%d].id = %s совпадает с ModelAlias",
							path, prov, i, id))
				}
			}
		}
	}
	return result("qwen_settings", PASS, "selectedType=openai, ModelAlias в modelProviders отсутствует")
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
