package backup

// TE-<ID> — строки раздела 7 ТЗ (CONTROL W7), которые решаются в
// подсистеме копий БД и rollback: quick_check (K6), откат (U2).

import (
	"os"
	"path/filepath"
	"testing"
)

// TestTE_K6 — БД повреждена: quick_check при старте → ошибка (старт отклонён);
// здоровая БД → quick_check проходит.
func TestTE_K6(t *testing.T) {
	dir := t.TempDir()
	dbPath := makeDB(t, dir)
	if err := QuickCheck(dbPath); err != nil {
		t.Fatalf("здоровая БД: quick_check: %v, хочу nil", err)
	}
	corrupt := filepath.Join(dir, "corrupt.db")
	if err := os.WriteFile(corrupt, []byte("not a sqlite db"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := QuickCheck(corrupt); err == nil {
		t.Fatal("повреждённая БД: quick_check не ошибся (старт не отклонён)")
	}
}

// TestTE_U2 — откат: `runpilot rollback` возвращает прежний бинарник и БД
// (копию до последней миграции).
func TestTE_U2(t *testing.T) {
	TestRollbackRestoresBinaryAndDB(t)
}
