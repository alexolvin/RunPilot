package store

// Экспорт и импорт рабочих настроек (v2 раздел 4.7).
//
// Экспорт — YAML рабочих настроек (путь→значение) и серверов, без секретов
// (в config.Server хранятся только имена переменных окружения, не значения).
// Импорт — разбор YAML, проверка (раздел 4.5), разница и применение одной
// ревизией с источником "import".

import (
	"encoding/json"
	"fmt"
	"time"

	"runpilot/internal/config"

	"gopkg.in/yaml.v3"
)

// SettingsExport — формат экспорта/импорта (YAML).
type SettingsExport struct {
	Settings map[string]any `yaml:"settings"`
	Servers  []config.Server `yaml:"servers"`
}

// ExportSettings — YAML текущего рабочего состояния (настройки + серверы).
func (s *Store) ExportSettings() ([]byte, error) {
	cfg := config.Defaults()
	if err := s.LoadWorkingInto(cfg); err != nil {
		return nil, err
	}
	servers, err := s.ListServers()
	if err != nil {
		return nil, err
	}
	cfg.Servers = servers
	snap, err := cfg.WorkingSnapshot()
	if err != nil {
		return nil, err
	}
	m := make(map[string]any, len(snap))
	for p, v := range snap {
		var x any
		if err := json.Unmarshal(v, &x); err != nil {
			return nil, fmt.Errorf("store: export: %s: %w", p, err)
		}
		m[p] = x
	}
	return yaml.Marshal(SettingsExport{Settings: m, Servers: servers})
}

// ImportResult — результат импорта.
type ImportResult struct {
	Diff       []SettingChange `json:"diff"`
	Applied    bool            `json:"applied"`
	RevisionID int64           `json:"revision_id,omitempty"`
}

// ImportSettings разбирает YAML экспорта, валидирует (раздел 4.5) и, если не
// dryRun, применяет одной ревизией (source="import"): рабочие настройки и
// серверы. Возвращает разницу (для dryRun — то, что было бы изменено).
func (s *Store) ImportSettings(data []byte, dryRun bool, author string, now time.Time) (*ImportResult, error) {
	var exp SettingsExport
	if err := yaml.Unmarshal(data, &exp); err != nil {
		return nil, fmt.Errorf("store: import: разбор YAML: %w", err)
	}
	// Собираем конфиг: defaults + рабочие из экспорта + серверы.
	cfg := config.Defaults()
	snap := make(map[string]json.RawMessage, len(exp.Settings))
	for p, v := range exp.Settings {
		d, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("store: import: %s: %w", p, err)
		}
		snap[p] = d
	}
	if err := cfg.ApplyWorking(snap); err != nil {
		return nil, err
	}
	cfg.Servers = exp.Servers
	// Проверка раздела 4.5: невалидный импорт отклоняется.
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	cur, err := s.LoadWorkingSettings()
	if err != nil {
		return nil, err
	}
	// Разница по рабочим настройкам (текущие строки БД vs импорт).
	curStr := make(map[string]string, len(cur))
	for p, v := range cur {
		curStr[p] = string(v)
	}
	diff := computeSettingDiff(curStr, snap)

	if dryRun {
		return &ImportResult{Diff: diff, Applied: false}, nil
	}

	id, err := s.SaveWorkingSettings(snap, author, "import", now)
	if err != nil {
		return nil, err
	}
	if err := s.ReplaceServers(exp.Servers, now); err != nil {
		return nil, fmt.Errorf("store: import: серверы: %w", err)
	}
	return &ImportResult{Diff: diff, Applied: true, RevisionID: id}, nil
}
