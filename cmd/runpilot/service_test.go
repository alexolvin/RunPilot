package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInstallSystemdNodeUnit — юнит узла (14.1): user-схема —
// default.target, EnvironmentFile секрета, PATH с user-bin (окружение
// панелей наследуется от клиента — W9 доп-3e), Restart=always.
func TestInstallSystemdNodeUnit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg := filepath.Join(home, ".config", "runpilot", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, []byte("client: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := installSystemd("/usr/local/bin/runpilot", cfg, "node"); err != nil {
		t.Fatal(err)
	}
	unitPath := filepath.Join(home, ".config", "systemd", "user", "runpilot-node.service")
	data, err := os.ReadFile(unitPath)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	for _, want := range []string{
		"WantedBy=default.target",
		"EnvironmentFile=-%h/.config/runpilot/secrets.env",
		"Environment=PATH=%h/.local/bin:/usr/local/bin:/usr/bin:/bin",
		"Restart=always",
		"ExecStart=/usr/local/bin/runpilot node --config",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("юнит узла не содержит %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "multi-user.target") {
		t.Errorf("user-юнит не должен вешаться на multi-user.target:\n%s", s)
	}
}

// TestInstallSystemdServeUnit — роль serve: EnvironmentFile из env-файла
// координатора.
func TestInstallSystemdServeUnit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := installSystemd("/usr/local/bin/runpilot", "/x/config.yaml", "serve"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(home, ".config", "systemd", "user", "runpilot-serve.service"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "EnvironmentFile=-%h/.config/runpilot/env") {
		t.Errorf("юнит serve без EnvironmentFile env:\n%s", data)
	}
}
