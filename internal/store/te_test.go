package store

// TE-K7 — строка раздела 7 ТЗ (CONTROL W7): ошибка миграции схемы.
// Миграция выполняется в транзакции: при ошибке — откат (без частичной
// схемы) и ошибка (старт координатора отклонён). Копия БД до миграции
// делается в serve/upgrade (раздел 6.6) и проверяется TestTE_K6/U2 (backup).

import (
	"path/filepath"
	"testing"
	"time"
)

func TestTE_K7(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "runpilot.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	now := time.Now()

	// Ошибка на этапе записи schema_version (PK-конфликт version=1, уже
	// применённый) → откат транзакции: таблица из тела миграции НЕ остаётся.
	bad := []byte(`CREATE TABLE k7_rollback_probe (x INTEGER)`)
	if err := st.applyOneMigration("001_init", 1, bad, now); err == nil {
		t.Fatal("ожидал ошибку миграции (PK-конфликт → откат, старт отклонён)")
	}
	var n int
	if err := st.db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='k7_rollback_probe'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("частичная схема после отката: таблица k7_rollback_probe существует")
	}

	// Успешная миграция → commit: таблица есть, schema_version обновлён.
	good := []byte(`CREATE TABLE k7_ok_probe (x INTEGER)`)
	if err := st.applyOneMigration("901_ok", 901, good, now); err != nil {
		t.Fatalf("успешная миграция: %v", err)
	}
	if err := st.db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='k7_ok_probe'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("таблица k7_ok_probe после commit: %d, хочу 1", n)
	}
	var ver int
	if err := st.db.QueryRow(`SELECT version FROM schema_version WHERE name='901_ok'`).Scan(&ver); err != nil {
		t.Fatal(err)
	}
	if ver != 901 {
		t.Fatalf("schema_version: %d, хочу 901", ver)
	}
}
