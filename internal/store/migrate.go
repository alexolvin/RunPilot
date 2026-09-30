package store

import (
	"database/sql"
	"embed"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// migrate применяет неотработанные миграции по порядку имён.
func (s *Store) migrate() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS schema_version (
		version    INTEGER PRIMARY KEY,
		name       TEXT NOT NULL,
		applied_at TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("store: schema_version: %w", err)
	}
	var current int
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_version`).Scan(&current); err != nil {
		return fmt.Errorf("store: читать schema_version: %w", err)
	}

	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		n, err := strconv.Atoi(strings.SplitN(name, "_", migrationNameFields)[0])
		if err != nil {
			return fmt.Errorf("store: имя миграции %q: %w", name, err)
		}
		if n <= current {
			continue
		}
		sqlText, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		if err := s.applyOneMigration(name, n, sqlText, time.Now().UTC()); err != nil {
			return err
		}
		current = n
	}
	return nil
}

// applyOneMigration — одна миграция в транзакции (K7): успех → запись в
// schema_version + commit; ошибка → rollback (без частичной схемы) и
// ошибка (старт отклонён).
func (s *Store) applyOneMigration(name string, n int, sqlText []byte, now time.Time) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(string(sqlText)); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("store: миграция %s: %w", name, err)
	}
	if _, err := tx.Exec(
		`INSERT INTO schema_version (version, name, applied_at) VALUES (?, ?, ?)`,
		n, name, now.UTC().Format(time.RFC3339Nano),
	); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

// SchemaVersion — номер применённой схемы.
func (s *Store) SchemaVersion() (int, error) {
	var v int
	err := s.db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_version`).Scan(&v)
	return v, err
}

// LatestMigrationVersion — максимальный номер миграции в каталоге (раздел 6.6:
// решение о копии БД перед миграцией).
func LatestMigrationVersion() int {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return 0
	}
	max := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		n, _ := strconv.Atoi(strings.SplitN(e.Name(), "_", migrationNameFields)[0])
		if n > max {
			max = n
		}
	}
	return max
}

// SchemaVersionOf — версия схемы файла (ro, без открытия на запись); exists —
// есть ли файл (или таблица schema_version).
func SchemaVersionOf(dbPath string) (version int, exists bool, err error) {
	if _, err := os.Stat(dbPath); err != nil {
		return 0, false, nil
	}
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if err != nil {
		return 0, false, err
	}
	defer db.Close()
	var has int
	if err := db.QueryRow(
		`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='schema_version'`).Scan(&has); err != nil {
		return 0, false, err
	}
	if has == 0 {
		return 0, true, nil
	}
	if err := db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_version`).Scan(&version); err != nil {
		return 0, true, err
	}
	return version, true, nil
}
