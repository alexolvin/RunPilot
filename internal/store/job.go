package store

import (
	"database/sql"
	"errors"
	"time"
)

// JobBeat — пульс задания runpilot exec (раздел 8.1, строка X5). Создаёт запись
// при первом пульсе, обновляет last_beat дальше.
func (s *Store) JobBeat(sid string, at time.Time) error {
	return s.DoWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(`
INSERT INTO job_heartbeat (sid, last_beat) VALUES (?, ?)
ON CONFLICT(sid) DO UPDATE SET last_beat = excluded.last_beat`, sid, ts(at))
		return err
	})
}

// JobBeatTime — время последнего пульса; (zero, false), если записи нет
// (задание ещё не получило аренду / не запустило qwen).
func (s *Store) JobBeatTime(sid string) (time.Time, bool) {
	var beat string
	err := s.db.QueryRow(`SELECT last_beat FROM job_heartbeat WHERE sid = ?`, sid).Scan(&beat)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false
	}
	if err != nil {
		return time.Time{}, false
	}
	t, _ := time.Parse(time.RFC3339Nano, beat)
	return t, true
}

// JobSetCancel — флаг «Прервать» из веба (SIGTERM процессу qwen).
func (s *Store) JobSetCancel(sid string, on bool) error {
	v := 0
	if on {
		v = 1
	}
	return s.DoWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(`
INSERT INTO job_heartbeat (sid, last_beat, cancel) VALUES (?, ?, ?)
ON CONFLICT(sid) DO UPDATE SET cancel = excluded.cancel`, sid, ts(time.Time{}), v)
		return err
	})
}

// JobCancel — задан ли флаг «Прервать» для задания.
func (s *Store) JobCancel(sid string) bool {
	var c int
	if err := s.db.QueryRow(`SELECT cancel FROM job_heartbeat WHERE sid = ?`, sid).Scan(&c); err != nil {
		return false
	}
	return c == 1
}

// JobDelete — удалить запись (при завершении задания).
func (s *Store) JobDelete(sid string) error {
	return s.DoWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(`DELETE FROM job_heartbeat WHERE sid = ?`, sid)
		return err
	})
}

// JobStale — sid заданий с просроченным пульсом (last_beat < cutoff).
// Используется планировщиком (X5): аренда снимается (LOST), без перепостановки.
func (s *Store) JobStale(cutoff time.Time) ([]string, error) {
	rows, err := s.db.Query(
		`SELECT sid FROM job_heartbeat WHERE last_beat < ?`, ts(cutoff))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var sid string
		if err := rows.Scan(&sid); err != nil {
			return nil, err
		}
		out = append(out, sid)
	}
	return out, rows.Err()
}
