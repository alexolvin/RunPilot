// Package orchestrators — встроенные профили сайтов оркестратора (ТЗ Relay §9).
// Переопределяются из ~/.config/runpilot/orchestrators/ (там же имя файла).
package orchestrators

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

//go:embed *.yaml *.js
var profiles embed.FS

// Names — имена встроенных профилей (без расширения).
func Names() []string {
	entries, err := profiles.ReadDir(".")
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".yaml") {
			out = append(out, strings.TrimSuffix(name, ".yaml"))
		}
	}
	return out
}

// YAML — YAML профиля. err — профиль не найден.
func YAML(name string) ([]byte, error) {
	return profiles.ReadFile(name + ".yaml")
}

// JS — JS-экстракторы профиля. err — файл не найден.
func JS(name string) ([]byte, error) {
	b, err := profiles.ReadFile(name + ".js")
	if err != nil {
		return nil, err
	}
	return b, nil
}

// ConfigDir — каталог переопределений (~/.config/runpilot/orchestrators).
func ConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "runpilot", "orchestrators")
}

// Override — переопределение из ~/.config (yaml или js). nil, если нет.
func Override(name, ext string) ([]byte, error) {
	dir := ConfigDir()
	if dir == "" {
		return nil, fs.ErrNotExist
	}
	return os.ReadFile(filepath.Join(dir, name+"."+ext))
}
