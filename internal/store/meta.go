package store

// Метаданные координатора (v2 разделы 6.2/6.5): meta.mode (режим) и
// meta.last_alive_at («сердцебиение»). Запись — на единственном писателе.

import (
	"database/sql"
	"time"
)

// Ключи meta (раздел 6.2/6.5 v2).
const (
	MetaKeyMode        = "mode"
	MetaKeyLastAliveAt = "last_alive_at"
)

// Mode — режим координатора (NORMAL/PAUSED/EMERGENCY/SAFE_MODE), "" если нет.
func (s *Store) Mode() (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM meta WHERE key = ?`, MetaKeyMode).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return v, nil
}

// SetMode — записать режим (6.2: при EMERGENCY — первым шагом).
func (s *Store) SetMode(mode string) error {
	return s.DoWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO meta (key, value) VALUES (?, ?)
			ON CONFLICT(key) DO UPDATE SET value=excluded.value`, MetaKeyMode, mode)
		return err
	})
}

// SetLastAliveAt — «сердцебиение» (6.5): координатор пишет каждые heartbeat_sec.
func (s *Store) SetLastAliveAt(t time.Time) error {
	return s.DoWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO meta (key, value) VALUES (?, ?)
			ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
			MetaKeyLastAliveAt, t.UTC().Format(time.RFC3339Nano))
		return err
	})
}

// LastAliveAt — время последнего «сердцебиения» (zero, если нет — первый старт).
func (s *Store) LastAliveAt() (time.Time, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM meta WHERE key = ?`, MetaKeyLastAliveAt).Scan(&v)
	if err == sql.ErrNoRows {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	if v == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339Nano, v)
}

// ProbeWrite — пробная запись (6.4): проверяет, что БД записываема
// (SQLITE_FULL/IOERR → ошибка). safe_probe вызывает её раз в
// safe_probe_sec; успех → выход из SAFE_MODE.
func (s *Store) ProbeWrite() error {
	return s.DoWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO meta (key, value) VALUES (?, ?)
			ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
			"__probe", time.Now().UTC().Format(time.RFC3339Nano))
		return err
	})
}
