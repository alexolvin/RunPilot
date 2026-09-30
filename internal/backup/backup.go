// Package backup — резервные копии БД, откат и восстановление (раздел 6.6 ТЗ).
//
// Копия перед миграцией схемы, ежедневная VACUUM INTO, rollback (бинарник
// runpilot.prev + копия БД до последней миграции) и restore. Все функции принимают
// пути — тестуемы без реального сервиса.
package backup

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Backup — одна резервная копия для списка (web «Настройки → Хранение»).
type Backup struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
	MTime string `json:"mtime"` // RFC3339
}

// BackupDir — каталог копий: <каталог БД>/backups (раздел 6.6).
func BackupDir(dbPath string) string {
	return filepath.Join(filepath.Dir(dbPath), backupsName)
}

// PreMigrationName — имя копии перед миграцией: runpilot-<версия>-<время>.db.
func PreMigrationName(version string, now time.Time) string {
	return "runpilot-" + version + "-" + now.Format(timeStamp) + ".db"
}

// PreMigrationBackup — копия БД перед миграцией схемы (раздел 6.6). Если файла
// БД нет — копия не создаётся (пустая строка). Возвращает путь копии.
func PreMigrationBackup(dbPath, version string, now time.Time) (string, error) {
	if _, err := os.Stat(dbPath); err != nil {
		return "", nil
	}
	dir := BackupDir(dbPath)
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return "", fmt.Errorf("backup: mkdir: %w", err)
	}
	dst := filepath.Join(dir, PreMigrationName(version, now))
	if err := copyFile(dbPath, dst); err != nil {
		return "", fmt.Errorf("backup: копия: %w", err)
	}
	return dst, nil
}

// VacuumInto — ежедневная копия командой VACUUM INTO (раздел 6.6).
func VacuumInto(dbPath, destPath string) error {
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		return fmt.Errorf("backup: open: %w", err)
	}
	defer db.Close()
	if _, err := db.Exec("VACUUM INTO " + quoteSQL(destPath)); err != nil {
		return fmt.Errorf("backup: VACUUM INTO: %w", err)
	}
	return nil
}

// QuickCheck — PRAGMA quick_check (раздел 2.1/K6): ошибка → БД повреждена.
func QuickCheck(dbPath string) error {
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if err != nil {
		return fmt.Errorf("backup: open: %w", err)
	}
	defer db.Close()
	rows, err := db.Query("PRAGMA quick_check")
	if err != nil {
		return fmt.Errorf("backup: quick_check: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return err
		}
		if line != quickCheckOK {
			return fmt.Errorf("backup: БД повреждена: %s", line)
		}
	}
	return rows.Err()
}

// Restore — замена БД указанной копией после quick_check (раздел 6.6).
func Restore(backupPath, dbPath string) error {
	if err := QuickCheck(backupPath); err != nil {
		return fmt.Errorf("backup: копией не восстановить: %w", err)
	}
	// атомарно: во временный файл, затем rename поверх.
	tmp := dbPath + tmpSuffix
	if err := copyFile(backupPath, tmp); err != nil {
		return fmt.Errorf("backup: restore: %w", err)
	}
	for _, ext := range []string{walSuffix, shmSuffix} {
		_ = os.Remove(dbPath + ext)
	}
	if err := os.Rename(tmp, dbPath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("backup: rename: %w", err)
	}
	return nil
}

// RestoreBinary — вернуть бинарник runpilot.prev на место текущего (раздел 6.6).
// Замена атомарная и безопасна для запущенного бинарника: содержимое
// дописывается во временный файл, текущий уезжает через rename (не O_TRUNC —
// перезапись работающего исполняемого файла = ETXTBSY на Linux), затем tmp
// занимает место текущего. Запущенный процесс продолжает работать со своим
// отображением; файл по пути — прежняя версия.
func RestoreBinary(prevPath, targetPath string) error {
	if _, err := os.Stat(prevPath); err != nil {
		return fmt.Errorf("backup: runpilot.prev не найден: %w", err)
	}
	tmp := targetPath + binTmpSuffix
	if err := copyFile(prevPath, tmp); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("backup: restore бинарника: %w", err)
	}
	if err := os.Chmod(tmp, binFileMode); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("backup: chmod: %w", err)
	}
	old := targetPath + binOldSuffix
	if err := os.Rename(targetPath, old); err == nil {
		if err := os.Rename(tmp, targetPath); err != nil {
			_ = os.Rename(old, targetPath) // откат
			_ = os.Remove(tmp)
			return fmt.Errorf("backup: rename: %w", err)
		}
		_ = os.Remove(old)
	} else if err := os.Rename(tmp, targetPath); err != nil {
		// Текущего файла не было — просто устанавливаем новый.
		_ = os.Remove(tmp)
		return fmt.Errorf("backup: rename: %w", err)
	}
	return nil
}

// Rollback — вернуть runpilot.prev и последнюю копию БД (раздел 6.6, U2).
// prevBin — путь runpilot.prev, curBin — путь текущего бинарника, dbPath — БД.
// Возвращает пути восстановленного бинарника и копии БД.
func Rollback(prevBin, curBin, dbPath string) (binary, dbBackup string, err error) {
	if err := RestoreBinary(prevBin, curBin); err != nil {
		return "", "", err
	}
	backupPath, err := Latest(dbPath)
	if err != nil {
		return curBin, "", err
	}
	if err := Restore(backupPath, dbPath); err != nil {
		return curBin, "", err
	}
	return curBin, backupPath, nil
}

// List — копии в каталоге (по убыванию времени; для web/API).
func List(dbPath string) ([]Backup, error) {
	dir := BackupDir(dbPath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Backup
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), dbSuffix) {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, Backup{
			Name: e.Name(), Path: filepath.Join(dir, e.Name()),
			Bytes: fi.Size(), MTime: fi.ModTime().UTC().Format(time.RFC3339),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].MTime > out[j].MTime })
	return out, nil
}

// Latest — самая свежая копия (для rollback).
func Latest(dbPath string) (string, error) {
	list, err := List(dbPath)
	if err != nil {
		return "", err
	}
	if len(list) == 0 {
		return "", fmt.Errorf("backup: копий нет")
	}
	return list[0].Path, nil
}

// PruneKeep — оставить не больше keep последних копий (раздел 6.6).
func PruneKeep(dbPath string, keep int) error {
	if keep <= 0 {
		return nil
	}
	list, err := List(dbPath)
	if err != nil {
		return err
	}
	for i, b := range list {
		if i < keep {
			continue
		}
		if err := os.Remove(b.Path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// copyFile — побайтовая копия с правами dbFileMode.
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, fileMode)
}

// quoteSQL — строка для SQL (экранирование одинарных кавычек).
func quoteSQL(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
