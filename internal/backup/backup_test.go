package backup

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func makeDB(t *testing.T, dir string) string {
	t.Helper()
	dbPath := filepath.Join(dir, "runpilot.db")
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE data (v TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO data VALUES ('before')"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	return dbPath
}

func countRows(t *testing.T, dbPath string) int {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow("SELECT count(*) FROM data").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestRollbackRestoresBinaryAndDB — CONTROL 8: `rollback` возвращает прежние
// бинарник (runpilot.prev) и БД (копия до последней миграции).
func TestRollbackRestoresBinaryAndDB(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	curBin := filepath.Join(binDir, "runpilot")
	prevBin := filepath.Join(binDir, "runpilot.prev")
	if err := os.WriteFile(curBin, []byte("NEW-BINARY"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(prevBin, []byte("OLD-BINARY"), 0o755); err != nil {
		t.Fatal(err)
	}

	dbPath := makeDB(t, dir)
	// копия перед миграцией.
	b, err := PreMigrationBackup(dbPath, "1.0.0", time.Now())
	if err != nil || b == "" {
		t.Fatalf("backup: %v %q", err, b)
	}
	// «миграция»: добавляем строку (БД меняется).
	db, _ := sql.Open("sqlite", "file:"+dbPath)
	_, _ = db.Exec("INSERT INTO data VALUES ('after')")
	db.Close()
	if n := countRows(t, dbPath); n != 2 {
		t.Fatalf("после миграции: %d строк, хочу 2", n)
	}

	bin, dbBackup, err := Rollback(prevBin, curBin, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if bin != curBin || dbBackup != b {
		t.Fatalf("возврат: bin=%q dbBackup=%q", bin, dbBackup)
	}
	// бинарник восстановлен из runpilot.prev.
	cur, _ := os.ReadFile(curBin)
	if string(cur) != "OLD-BINARY" {
		t.Fatalf("бинарник=%q, хочу OLD-BINARY", cur)
	}
	// БД восстановлена (только 'before').
	if n := countRows(t, dbPath); n != 1 {
		t.Fatalf("БД после rollback: %d строк, хочу 1", n)
	}
}

// TestRestoreAndQuickCheck — restore заменяет БД копией; повреждённая копия
// отклоняется (quick_check).
func TestRestoreAndQuickCheck(t *testing.T) {
	dir := t.TempDir()
	dbPath := makeDB(t, dir)
	b, _ := PreMigrationBackup(dbPath, "1.0.0", time.Now())

	db, _ := sql.Open("sqlite", "file:"+dbPath)
	_, _ = db.Exec("INSERT INTO data VALUES ('changed')")
	db.Close()

	if err := Restore(b, dbPath); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, dbPath); n != 1 {
		t.Fatalf("после restore: %d строк, хочу 1", n)
	}

	// Повреждённая копия отклоняется.
	corrupt := filepath.Join(dir, "corrupt.db")
	if err := os.WriteFile(corrupt, []byte("not a sqlite db"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := QuickCheck(corrupt); err == nil {
		t.Fatal("QuickCheck на повреждённом файле не ошибся")
	}
	if err := Restore(corrupt, dbPath); err == nil {
		t.Fatal("Restore с повреждённой копией не ошибся")
	}
}

// TestVacuumIntoAndPrune — ежедневная копия VACUUM INTO + PruneKeep.
func TestVacuumIntoAndPrune(t *testing.T) {
	dir := t.TempDir()
	dbPath := makeDB(t, dir)
	if _, err := PreMigrationBackup(dbPath, "1.0.0", time.Now()); err != nil {
		t.Fatal(err)
	}
	daily := filepath.Join(BackupDir(dbPath), "runpilot-daily.db")
	if err := VacuumInto(dbPath, daily); err != nil {
		t.Fatal(err)
	}
	if err := QuickCheck(daily); err != nil {
		t.Fatalf("ежедневная копия повреждена: %v", err)
	}
	// PruneKeep: оставить 1 → лишние удаляются.
	if _, err := PreMigrationBackup(dbPath, "1.0.1", time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := PruneKeep(dbPath, 1); err != nil {
		t.Fatal(err)
	}
	if list, _ := List(dbPath); len(list) != 1 {
		t.Fatalf("после PruneKeep(1): %d копий, хочу 1", len(list))
	}
}
