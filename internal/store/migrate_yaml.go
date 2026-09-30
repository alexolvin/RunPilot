package store

// Миграция с YAML (v2 раздел 4.8): первый старт v2 переносит рабочие
// секции из config.yaml в БД.
//
//   1. Копия БД и копия config.yaml (до изменений).
//   2. Рабочие секции → БД ревизией №1 (source="migration"); серверы → server.
//   3. config.yaml переписывается без перенесённых секций (остаётся bootstrap).
//   4. Ошибка любого шага → восстановление копий, старт отклоняется.
//
// Функция тестуема: принимает пути к БД и конфигу и часы (clock.Clock).

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"

	"runpilot/internal/clock"
	"runpilot/internal/config"

	"gopkg.in/yaml.v3"
)

// MigrateResult — результат миграции.
type MigrateResult struct {
	Skipped      bool
	MigratedKeys int
	DBBackup     string
	ConfigBackup string
}

// MigrateFromYAML — раздел 4.8. Если БД уже имеет таблицу setting или в
// конфиге нет рабочих секций — no-op (Skipped=true).
func MigrateFromYAML(dbPath, configPath, version string, clk clock.Clock) (*MigrateResult, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("migrate: чтение конфига: %w", err)
	}
	raw, err := parseRawMap(data)
	if err != nil {
		return nil, fmt.Errorf("migrate: разбор конфига: %w", err)
	}
	if exists, _ := settingTableExists(dbPath); exists {
		return &MigrateResult{Skipped: true}, nil
	}
	present := presentWorkingKeys(raw)
	if len(present) == 0 {
		return &MigrateResult{Skipped: true}, nil
	}

	now := clk.Now()
	stamp := now.Format("20060102-150405")
	dbBackup := ""
	dbExisted := false
	if _, err := os.Stat(dbPath); err == nil {
		dbExisted = true
		dbBackup = dbPath + ".bak-" + version + "-" + stamp
		if err := copyFile(dbPath, dbBackup); err != nil {
			return nil, fmt.Errorf("migrate: копия БД: %w", err)
		}
	}
	cfgBackup := configPath + ".bak-" + stamp
	if err := copyFile(configPath, cfgBackup); err != nil {
		return nil, fmt.Errorf("migrate: копия конфига: %w", err)
	}

	st, err := Open(dbPath)
	if err != nil {
		if rerr := restoreFiles(dbPath, configPath, dbBackup, cfgBackup, dbExisted); rerr != nil {
			return nil, fmt.Errorf("migrate: open БД: %w; откат: %v", err, rerr)
		}
		return nil, fmt.Errorf("migrate: open БД: %w", err)
	}

	var migErr error
	func() {
		defer func() {
			if migErr != nil {
				_ = st.Close()
				if rerr := restoreFiles(dbPath, configPath, dbBackup, cfgBackup, dbExisted); rerr != nil {
					migErr = fmt.Errorf("%w; откат: %v", migErr, rerr)
				}
			}
		}()

		cfg, err := config.Load(configPath)
		if err != nil {
			migErr = fmt.Errorf("migrate: load config: %w", err)
			return
		}
		fullSnap, err := cfg.WorkingSnapshot()
		if err != nil {
			migErr = fmt.Errorf("migrate: снимок: %w", err)
			return
		}
		diff := migrationDiff(present)
		if _, err := st.InsertWorkingFull(fullSnap, diff, "migration", "migration", now); err != nil {
			migErr = fmt.Errorf("migrate: запись настроек: %w", err)
			return
		}
		if err := st.ReplaceServers(cfg.Servers, now); err != nil {
			migErr = fmt.Errorf("migrate: серверы: %w", err)
			return
		}
		newCfg, err := rewriteBootstrap(raw)
		if err != nil {
			migErr = fmt.Errorf("migrate: переписать конфиг: %w", err)
			return
		}
		if err := os.WriteFile(configPath, newCfg, dbFileMode); err != nil {
			migErr = fmt.Errorf("migrate: запись конфига: %w", err)
			return
		}
	}()
	if migErr != nil {
		return nil, migErr
	}
	if err := st.Close(); err != nil {
		return nil, fmt.Errorf("migrate: close БД: %w", err)
	}
	return &MigrateResult{MigratedKeys: len(present), DBBackup: dbBackup, ConfigBackup: cfgBackup}, nil
}

// settingTableExists — есть ли таблица setting (без применения миграций).
func settingTableExists(dbPath string) (bool, error) {
	if _, err := os.Stat(dbPath); err != nil {
		return false, nil // файла нет — fresh, таблицы нет
	}
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if err != nil {
		return false, err
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(
		`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='setting'`,
	).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

// parseRawMap — YAML в map[string]any (без строгих полей).
func parseRawMap(data []byte) (map[string]any, error) {
	var m map[string]any
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(false)
	if err := dec.Decode(&m); err != nil {
		return nil, err
	}
	return m, nil
}

// walkRaw — листья вложенной карты как путь→значение.
func walkRaw(v any, prefix string, out map[string]any) {
	if m, ok := v.(map[string]any); ok {
		for k, val := range m {
			p := k
			if prefix != "" {
				p = prefix + "." + k
			}
			walkRaw(val, p, out)
		}
		return
	}
	if prefix != "" {
		out[prefix] = v
	}
}

// presentWorkingKeys — рабочие листья, присутствующие в исходном YAML.
func presentWorkingKeys(raw map[string]any) map[string]any {
	workSet := make(map[string]bool)
	for _, p := range config.WorkingLeafPaths() {
		workSet[p] = true
	}
	leaves := map[string]any{}
	walkRaw(raw, "", leaves)
	out := make(map[string]any)
	for p, v := range leaves {
		if workSet[p] {
			out[p] = v
		}
	}
	return out
}

// migrationDiff — diff миграции: только перенесённые ключи (old=null).
func migrationDiff(present map[string]any) []SettingChange {
	var out []SettingChange
	for p, v := range present {
		data, err := json.Marshal(v)
		if err != nil {
			continue
		}
		out = append(out, SettingChange{Path: p, Old: nil, New: data})
	}
	SortSettingDiff(out)
	return out
}

// rewriteBootstrap — YAML без рабочих секций (остаётся bootstrap).
func rewriteBootstrap(raw map[string]any) ([]byte, error) {
	// Топ-уровневые рабочие секции удаляются целиком.
	for _, k := range []string{
		"scheduler", "dispatch", "turn", "gateway", "monitor", "limits",
		"retention", "profiles", "servers", "faults", "enroll", "external",
		"jobs", "backup",
	} {
		delete(raw, k)
	}
	// web: остаются только bootstrap-ключи.
	if web, ok := raw["web"].(map[string]any); ok {
		keep := make(map[string]any)
		for _, k := range []string{"bind", "port", "public_url"} {
			if v, ok := web[k]; ok {
				keep[k] = v
			}
		}
		raw["web"] = keep
	}
	// node: остаются только bootstrap-ключи.
	if node, ok := raw["node"].(map[string]any); ok {
		keep := make(map[string]any)
		for _, k := range []string{"host", "tmux_sockets", "gpu", "gpu_server"} {
			if v, ok := node[k]; ok {
				keep[k] = v
			}
		}
		raw["node"] = keep
	}
	// notify.telegram: остаётся только bot_token_env; alerts удаляется.
	if notify, ok := raw["notify"].(map[string]any); ok {
		if tg, ok := notify["telegram"].(map[string]any); ok {
			keep := make(map[string]any)
			if v, ok := tg["bot_token_env"]; ok {
				keep["bot_token_env"] = v
			}
			notify["telegram"] = keep
		}
		delete(notify, "alerts")
	}
	return yaml.Marshal(raw)
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, dbFileMode)
}

// restoreFiles — возврат БД и конфига к состоянию до миграции. Если файла
// БД не было (свежая установка) — созданное БД-файлы убираются.
func restoreFiles(dbPath, configPath, dbBackup, cfgBackup string, dbExisted bool) error {
	if dbExisted {
		if err := copyFile(dbBackup, dbPath); err != nil {
			return err
		}
	} else {
		for _, p := range []string{dbPath, dbPath + "-wal", dbPath + "-shm"} {
			if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	return copyFile(cfgBackup, configPath)
}
