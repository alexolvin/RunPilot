package doctor

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"runpilot/internal/config"
)

func runAll(t *testing.T, cfg *config.Config, path string) []Result {
	t.Helper()
	// client_coordinator не ходит в реальный HTTP в тестах.
	oldGet := httpGet
	httpGet = func(url, token string) (int, error) { return 200, nil }
	t.Cleanup(func() { httpGet = oldGet })
	return Run(&Context{Cfg: cfg, CfgPath: path}, DefaultChecks())
}

// client_coordinator: отвечает (2xx) -> PASS; не отвечает (dial) -> FAIL с адресом.
func TestClientCoordinator(t *testing.T) {
	cfg := config.Defaults()
	cfg.Client.Coordinator = "http://127.0.0.1:18788"

	t.Run("отвечает", func(t *testing.T) {
		old := httpGet
		httpGet = func(url, token string) (int, error) { return 200, nil }
		defer func() { httpGet = old }()
		r := checkClientCoordinator(&Context{Cfg: cfg})
		if r.Level != PASS {
			t.Fatalf("отвечает: %s", r)
		}
	})
	t.Run("не отвечает", func(t *testing.T) {
		old := httpGet
		httpGet = func(url, token string) (int, error) { return 0, errors.New("connection refused") }
		defer func() { httpGet = old }()
		r := checkClientCoordinator(&Context{Cfg: cfg})
		if r.Level != FAIL {
			t.Fatalf("не отвечает: %s", r)
		}
		if !strings.Contains(r.Detail, "127.0.0.1:18788") {
			t.Errorf("в FAIL должен быть адрес: %s", r.Detail)
		}
	})
}

func TestDefaultChecksValid(t *testing.T) {
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("defaults должны быть валидны: %v", err)
	}
	// Изолированный HOME со здоровым settings.json: тест не зависит от
	// реальной ~/.qwen/settings.json машины (doctor обязан ловить коллизию
	// id == model_alias — см. TestQwenSettingsFailAliasCollision).
	home := t.TempDir()
	writeSettings(t, home, `{"security": {"auth": {"selectedType": "openai"}}}`)
	withHome(t, home)
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
	// OK: selectedType=openai и modelProviders нет (кодер ходит через шлюз).
	writeSettings(t, home, `{
		"security": {"auth": {"selectedType": "openai"}}
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

func TestQwenSettingsPassDirectProvider(t *testing.T) {
	home := t.TempDir()
	// Прямой провайдер с id, НЕ равным ModelAlias (qwen3.6-27b в defaults),
	// — это выбор оператора из /model, а не обход: PASS.
	writeSettings(t, home, `{
		"security": {"auth": {"selectedType": "openai"}},
		"modelProviders": {"openai": [{"id": "qwen3.8-27b", "baseUrl": "http://127.0.0.1:8004/v1"}]}
	}`)
	withHome(t, home)
	rs := runAll(t, config.Defaults(), "defaults")
	for _, r := range rs {
		if r.Check == "qwen_settings" {
			if r.Level != PASS {
				t.Fatalf("провайдер без коллизии: %s", r)
			}
			return
		}
	}
	t.Fatal("нет строки qwen_settings")
}

func TestQwenSettingsFailAliasCollision(t *testing.T) {
	home := t.TempDir()
	// id == ModelAlias — текущая (шлюзовая) модель становится Provider
	// Model: прямой baseUrl в обход шлюза — FAIL.
	writeSettings(t, home, `{
		"security": {"auth": {"selectedType": "openai"}},
		"modelProviders": {"openai": [{"id": "qwen3.6-27b", "baseUrl": "http://127.0.0.1:8004/v1"}]}
	}`)
	withHome(t, home)
	rs := runAll(t, config.Defaults(), "defaults")
	for _, r := range rs {
		if r.Check == "qwen_settings" {
			if r.Level != FAIL {
				t.Fatalf("коллизия id == alias: %s", r)
			}
			if !strings.Contains(r.Detail, "model_alias") {
				t.Fatalf("в detail должна быть причина: %q", r.Detail)
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

// writeQwenPkg — фейковый установленный qwen (managed npm): active.json +
// versions/<ver>/node_modules/@qwen-code/qwen-code/chunks/<chunk>. Возвращает
// путь пакета.
func writeQwenPkg(t *testing.T, home, version, chunkName, chunkBody string) {
	t.Helper()
	upd := filepath.Join(home, ".qwen", "updates", "npm", "npm")
	if err := os.MkdirAll(upd, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(upd, "active.json"),
		[]byte(`{"version":"`+version+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(upd, "versions", version, "node_modules", "@qwen-code", "qwen-code", "chunks")
	if err := os.MkdirAll(pkg, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, chunkName), []byte(chunkBody), 0o600); err != nil {
		t.Fatal(err)
	}
}

// п.8: строка QWEN_CODE_API_TIMEOUT_MS в пакете qwen → PASS (версия + путь).
func TestQwenTimeoutGuardPass(t *testing.T) {
	home := t.TempDir()
	writeQwenPkg(t, home, "1.2.3", "api-123.js",
		`const t = Number(process.env.QWEN_CODE_API_TIMEOUT_MS || 120000);`)
	withHome(t, home)
	rs := runAll(t, config.Defaults(), "defaults")
	for _, r := range rs {
		if r.Check == "qwen_timeout_guard" {
			if r.Level != PASS {
				t.Fatalf("с строкой: %s", r)
			}
			if !strings.Contains(r.Detail, "1.2.3") || !strings.Contains(r.Detail, "qwen-code") {
				t.Fatalf("в detail версия и путь: %q", r.Detail)
			}
			return
		}
	}
	t.Fatal("нет строки qwen_timeout_guard")
}

// п.8: без строки QWEN_CODE_API_TIMEOUT_MS → FAIL (версия + путь пакета).
func TestQwenTimeoutGuardFail(t *testing.T) {
	home := t.TempDir()
	writeQwenPkg(t, home, "0.9.0", "api-1.js", `const t = 120000; // без строки`)
	withHome(t, home)
	rs := runAll(t, config.Defaults(), "defaults")
	for _, r := range rs {
		if r.Check == "qwen_timeout_guard" {
			if r.Level != FAIL {
				t.Fatalf("без строки: %s", r)
			}
			if !strings.Contains(r.Detail, "0.9.0") || !strings.Contains(r.Detail, "qwen-code") {
				t.Fatalf("в detail версия и путь: %q", r.Detail)
			}
			return
		}
	}
	t.Fatal("нет строки qwen_timeout_guard")
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
