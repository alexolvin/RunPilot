package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"runpilot/internal/config"
)

func runAll(t *testing.T, cfg *config.Config, path string) []Result {
	t.Helper()
	return Run(&Context{Cfg: cfg, CfgPath: path}, DefaultChecks())
}

func TestDefaultChecksValid(t *testing.T) {
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("defaults должны быть валидны: %v", err)
	}
	rs := runAll(t, cfg, "defaults")
	for _, r := range rs {
		if r.Level == FAIL {
			t.Fatalf("ожидалось без FAIL: %s", r)
		}
	}
}

func TestSecretsEnvFail(t *testing.T) {
	cfg := config.Defaults()
	cfg.Coordinator.TokenEnv = "sk-abcdefghijklmnop"
	rs := runAll(t, cfg, "defaults")
	found := false
	for _, r := range rs {
		if r.Check == "secrets_env" && r.Level == FAIL {
			found = true
		}
	}
	if !found {
		t.Fatal("secret строкой в YAML должен давать FAIL secrets_env")
	}
	if !HasFail(rs) {
		t.Fatal("HasFail не видит FAIL")
	}
}

func TestBindFail(t *testing.T) {
	cfg := config.Defaults()
	cfg.Coordinator.Bind = "8.8.8.8"
	rs := runAll(t, cfg, "defaults")
	for _, r := range rs {
		if r.Check == "coordinator_bind" && r.Level != FAIL {
			t.Fatalf("bind вне CIDR: %s", r)
		}
	}
}

func withHome(t *testing.T, home string) {
	t.Helper()
	old := homeDir
	homeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { homeDir = old })
}

func TestQwenSettingsOK(t *testing.T) {
	home := t.TempDir()
	writeSettings(t, home, `{
		"security": {"auth": {"selectedType": "openai"}},
		"modelProviders": {"openai": [{"id": "qwen3.8-27b"}]}
	}`)
	withHome(t, home)
	cfg := config.Defaults()
	rs := runAll(t, cfg, "defaults")
	for _, r := range rs {
		if r.Check == "qwen_settings" && r.Level != PASS {
			t.Fatalf("qwen_settings: %s", r)
		}
	}
}

func TestQwenSettingsFailSelectedType(t *testing.T) {
	home := t.TempDir()
	writeSettings(t, home, `{"security": {"auth": {"selectedType": "qwen-oauth"}}}`)
	withHome(t, home)
	rs := runAll(t, config.Defaults(), "defaults")
	for _, r := range rs {
		if r.Check == "qwen_settings" {
			if r.Level != FAIL {
				t.Fatalf("selectedType != openai: %s", r)
			}
			return
		}
	}
	t.Fatal("нет строки qwen_settings")
}

func TestQwenSettingsFailModelAlias(t *testing.T) {
	home := t.TempDir()
	cfg := config.Defaults()
	cfg.Profiles.Qwen.ModelAlias = "e2-fake-model"
	writeSettings(t, home, `{
		"security": {"auth": {"selectedType": "openai"}},
		"modelProviders": {"openai": [{"id": "other"}, {"id": "e2-fake-model"}]}
	}`)
	withHome(t, home)
	rs := runAll(t, cfg, "defaults")
	for _, r := range rs {
		if r.Check == "qwen_settings" {
			if r.Level != FAIL {
				t.Fatalf("ModelAlias в modelProviders: %s", r)
			}
			if !strings.Contains(r.Detail, "modelProviders") {
				t.Fatalf("в detail должен быть ключ: %q", r.Detail)
			}
			return
		}
	}
	t.Fatal("нет строки qwen_settings")
}

func writeSettings(t *testing.T, home, json string) {
	t.Helper()
	dir := filepath.Join(home, ".qwen")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(json), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestParseTmuxVersion(t *testing.T) {
	cases := []struct {
		in  string
		maj int
		min int
		ok  bool
	}{
		{"tmux 3.2a", 3, 2, true},
		{"tmux 3.3a", 3, 3, true},
		{"tmux 2.7", 2, 7, true},
		{"tmux 4.0", 4, 0, true},
		{"garbage", 0, 0, false},
		{"tmux", 0, 0, false},
		{"tmux 3x2a", 0, 0, false},
	}
	for _, c := range cases {
		m, n, got := parseTmuxVersion(c.in)
		if m != c.maj || n != c.min || got != c.ok {
			t.Fatalf("parseTmuxVersion(%q) = %d %d %v, ждём %d %d %v",
				c.in, m, n, got, c.maj, c.min, c.ok)
		}
	}
}
