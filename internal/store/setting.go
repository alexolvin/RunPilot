package store

// Рабочие настройки и ревизии (v2 раздел 3.4, 4.6, 4.7).
//
// setting — текущее состояние (перекрывающий слой поверх defaults.go);
// setting_revision — история с полным снимком (точный откат, раздел 4.6).
// Секреты в таблицу не попадают: только имена переменных окружения (R5).

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"runpilot/internal/config"
)

// Revision — запись об изменении настроек (раздел 4.6).
type Revision struct {
	ID       int64
	TS       time.Time
	Author   string
	Source   string
	Diff     string                     // JSON: список {path, old, new}
	Snapshot map[string]json.RawMessage // полный рабочий снимок на момент ревизии
}

// LoadWorkingSettings — текущие рабочие настройки (путь→значение).
// Пустая таблица → пустая карта (значения остаются defaults).
func (s *Store) LoadWorkingSettings() (map[string]json.RawMessage, error) {
	rows, err := s.db.Query(`SELECT path, value FROM setting`)
	if err != nil {
		return nil, fmt.Errorf("store: settings: %w", err)
	}
	defer rows.Close()
	out := make(map[string]json.RawMessage)
	for rows.Next() {
		var p, v string
		if err := rows.Scan(&p, &v); err != nil {
			return nil, err
		}
		out[p] = json.RawMessage(v)
	}
	return out, rows.Err()
}

// LoadWorkingInto загружает рабочие настройки из БД в рабочую часть cfg.
// Если таблица пуста — cfg не изменяется (defaults).
func (s *Store) LoadWorkingInto(cfg *config.Config) error {
	snap, err := s.LoadWorkingSettings()
	if err != nil {
		return err
	}
	if len(snap) == 0 {
		return nil
	}
	return cfg.ApplyWorking(snap)
}

// SaveWorkingSettings сохраняет полный рабочий снимок snap и создаёт
// ревизию (автор, источник, diff «было → стало»). Возвращает id ревизии.
func (s *Store) SaveWorkingSettings(snap map[string]json.RawMessage, author, source string, now time.Time) (int64, error) {
	var id int64
	err := s.DoWrite(func(tx *sql.Tx) error {
		cur, err := currentSettingsTx(tx)
		if err != nil {
			return err
		}
		diff := computeSettingDiff(cur, snap)
		diffJSON, err := json.Marshal(diff)
		if err != nil {
			return fmt.Errorf("store: settings: diff: %w", err)
		}
		snapJSON, err := json.Marshal(snap)
		if err != nil {
			return fmt.Errorf("store: settings: snapshot: %w", err)
		}
		// Upsert только изменённых ключей (таблица после первого сохранения
		// полна, неизменённые строки уже на месте).
		for _, ch := range diff {
			if _, err := tx.Exec(
				`INSERT INTO setting (path, value, version, updated_at, updated_by)
				 VALUES (?, ?, 1, ?, ?)
				 ON CONFLICT(path) DO UPDATE SET value=excluded.value,
				    version=setting.version+1, updated_at=excluded.updated_at,
				    updated_by=excluded.updated_by`,
				ch.Path, string(ch.New), ts(now), author); err != nil {
				return err
			}
		}
		res, err := tx.Exec(
			`INSERT INTO setting_revision (ts, author, source, diff, snapshot)
			 VALUES (?, ?, ?, ?, ?)`,
			ts(now), author, source, string(diffJSON), string(snapJSON))
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		return err
	})
	return id, err
}

// InsertWorkingFull — upsert всех ключей полного снимка и создаёт ревизию с
// заданным diff. Используется миграцией (раздел 4.8): diff содержит только
// ключи, фактически перенесённые из YAML, а снимок полон (точный откат).
func (s *Store) InsertWorkingFull(snap map[string]json.RawMessage, diff []SettingChange, author, source string, now time.Time) (int64, error) {
	var id int64
	err := s.DoWrite(func(tx *sql.Tx) error {
		for p, v := range snap {
			if _, err := tx.Exec(
				`INSERT INTO setting (path, value, version, updated_at, updated_by)
				 VALUES (?, ?, 1, ?, ?)
				 ON CONFLICT(path) DO UPDATE SET value=excluded.value,
				    version=setting.version+1, updated_at=excluded.updated_at,
				    updated_by=excluded.updated_by`,
				p, string(v), ts(now), author); err != nil {
				return err
			}
		}
		diffJSON, err := json.Marshal(diff)
		if err != nil {
			return fmt.Errorf("store: settings: diff: %w", err)
		}
		snapJSON, err := json.Marshal(snap)
		if err != nil {
			return fmt.Errorf("store: settings: snapshot: %w", err)
		}
		res, err := tx.Exec(
			`INSERT INTO setting_revision (ts, author, source, diff, snapshot)
			 VALUES (?, ?, ?, ?, ?)`,
			ts(now), author, source, string(diffJSON), string(snapJSON))
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		return err
	})
	return id, err
}

// LatestSettingsRevisionID — id последней ревизии рабочих настроек (ETag /
// If-Match, раздел 15.3 O1: два окна меняют один объект → 409).
func (s *Store) LatestSettingsRevisionID() (int64, error) {
	var id int64
	err := s.db.QueryRow(`SELECT COALESCE(MAX(id), 0) FROM setting_revision`).Scan(&id)
	return id, err
}

// ListRevisions — ревизии по убыванию id (новые сверху).
func (s *Store) ListRevisions(limit int) ([]Revision, error) {
	rows, err := s.db.Query(`SELECT id, ts, author, source, diff, snapshot
		FROM setting_revision ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("store: revisions: %w", err)
	}
	defer rows.Close()
	var out []Revision
	for rows.Next() {
		var r Revision
		var tsv, diff, snap string
		if err := rows.Scan(&r.ID, &tsv, &r.Author, &r.Source, &diff, &snap); err != nil {
			return nil, err
		}
		r.TS, _ = time.Parse(time.RFC3339Nano, tsv)
		r.Diff = diff
		if err := json.Unmarshal([]byte(snap), &r.Snapshot); err != nil {
			return nil, fmt.Errorf("store: revision %d: snapshot: %w", r.ID, err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetRevision — ревизия по id (с полным снимком).
func (s *Store) GetRevision(id int64) (Revision, error) {
	var r Revision
	var tsv, diff, snap string
	err := s.db.QueryRow(`SELECT id, ts, author, source, diff, snapshot
		FROM setting_revision WHERE id = ?`, id).
		Scan(&r.ID, &tsv, &r.Author, &r.Source, &diff, &snap)
	if err != nil {
		if err == sql.ErrNoRows {
			return Revision{}, ErrNotFound
		}
		return Revision{}, err
	}
	r.TS, _ = time.Parse(time.RFC3339Nano, tsv)
	r.Diff = diff
	if err := json.Unmarshal([]byte(snap), &r.Snapshot); err != nil {
		return Revision{}, fmt.Errorf("store: revision %d: snapshot: %w", id, err)
	}
	return r, nil
}

// RevertToRevision откатывает к ревизии id: применяет её снимок как новую
// ревизию с источником "revert" (раздел 4.6). Возвращает id новой ревизии.
func (s *Store) RevertToRevision(id int64, author string, now time.Time) (int64, error) {
	target, err := s.GetRevision(id)
	if err != nil {
		return 0, err
	}
	return s.SaveWorkingSettings(target.Snapshot, author, "revert", now)
}

// SettingChange — одна запись diff «было → стало». Значения — сырой JSON:
// для отсутствующего ранее ключа Old = null (nil).
type SettingChange struct {
	Path string          `json:"path"`
	Old  json.RawMessage `json:"old"`
	New  json.RawMessage `json:"new"`
}

func currentSettingsTx(tx *sql.Tx) (map[string]string, error) {
	rows, err := tx.Query(`SELECT path, value FROM setting`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]string)
	for rows.Next() {
		var p, v string
		if err := rows.Scan(&p, &v); err != nil {
			return nil, err
		}
		out[p] = v
	}
	return out, rows.Err()
}

// computeSettingDiff — изменённые ключи (старое ≠ новое). Для отсутствующих
// ранее ключей old = null. Порядок по пути (стабильный вывод).
func computeSettingDiff(cur map[string]string, snap map[string]json.RawMessage) []SettingChange {
	var out []SettingChange
	for p, nv := range snap {
		oldStr, ok := cur[p]
		if ok && oldStr == string(nv) {
			continue
		}
		var old json.RawMessage
		if ok {
			old = json.RawMessage(oldStr)
		}
		out = append(out, SettingChange{Path: p, Old: old, New: nv})
	}
	SortSettingDiff(out)
	return out
}

// SortSettingDiff — стабильный порядок diff (для предсказуемого вывода).
func SortSettingDiff(d []SettingChange) {
	for i := 1; i < len(d); i++ {
		for j := i; j > 0 && d[j-1].Path > d[j].Path; j-- {
			d[j-1], d[j] = d[j], d[j-1]
		}
	}
}
