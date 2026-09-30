package store

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	"runpilot/internal/model"
)

// RequestRecord — строка request без токенов + счётчики (раздел 6 ТЗ):
// requests аренды и last_server сессии в том же пакете. Асинхронно
// (аудит, не блокирует запрос).
func (s *Store) RequestRecord(r model.Request) error {
	first := ""
	if !r.TFirstByte.IsZero() {
		first = r.TFirstByte.UTC().Format(time.RFC3339Nano)
	}
	return s.QueueWrite(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`
INSERT INTO request (sid, turn_id, server, path, status, t_start,
	t_first_byte, t_end, held_ms, error)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.SID, r.TurnID, r.Server, r.Path, r.Status,
			r.TStart.UTC().Format(time.RFC3339Nano), first,
			r.TEnd.UTC().Format(time.RFC3339Nano), r.HeldMS, r.Error); err != nil {
			return err
		}
		if _, err := tx.Exec(`
UPDATE lease SET requests = requests + 1
WHERE sid = ? AND state IN ('PENDING', 'ACTIVE')`, r.SID); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE session SET last_server = ? WHERE sid = ?`,
			r.Server, r.SID); err != nil {
			return err
		}
		return nil
	})
}

// RequestList — последние строки request сессии (диагностика, runpilot why Э5).
func (s *Store) RequestList(sid string, limit int) ([]model.Request, error) {
	rows, err := s.db.Query(`
SELECT id, sid, turn_id, server, path, status, t_start, t_first_byte,
	t_end, held_ms, error
FROM request WHERE sid = ?
ORDER BY t_start DESC, id DESC LIMIT ?`, sid, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Request
	for rows.Next() {
		var r model.Request
		var tStart, tFirst, tEnd string
		if err := rows.Scan(&r.ID, &r.SID, &r.TurnID, &r.Server, &r.Path,
			&r.Status, &tStart, &tFirst, &tEnd, &r.HeldMS, &r.Error); err != nil {
			return nil, err
		}
		r.TStart, _ = time.Parse(time.RFC3339Nano, tStart)
		if tFirst != "" {
			r.TFirstByte, _ = time.Parse(time.RFC3339Nano, tFirst)
		}
		r.TEnd, _ = time.Parse(time.RFC3339Nano, tEnd)
		out = append(out, r)
	}
	return out, rows.Err()
}

// RequestTTFBP95MS — p95 времени до первого байта (t_first_byte - t_start)
// в мс за период (раздел 11.9: p95 накладных расходов шлюза). 0, если нет
// запросов с первым байтом за период.
func (s *Store) RequestTTFBP95MS(since time.Time) (int64, error) {
	rows, err := s.db.Query(`
SELECT t_start, t_first_byte FROM request
WHERE t_start >= ? AND t_first_byte != ''
ORDER BY t_start`, ts(since))
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var ms []int64
	for rows.Next() {
		var tStart, tFirst string
		if err := rows.Scan(&tStart, &tFirst); err != nil {
			return 0, err
		}
		start, e1 := time.Parse(time.RFC3339Nano, tStart)
		first, e2 := time.Parse(time.RFC3339Nano, tFirst)
		if e1 != nil || e2 != nil || first.Before(start) {
			continue
		}
		ms = append(ms, first.Sub(start).Milliseconds())
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(ms) == 0 {
		return 0, nil
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i] < ms[j] })
	idx := len(ms) * ttfbP95Percent / percentWhole100
	if idx >= len(ms) {
		idx = len(ms) - 1
	}
	return ms[idx], nil
}

const requestCols = `id, sid, turn_id, server, path, status, t_start, t_first_byte,
t_end, held_ms, error`

// scanRequest — строка request из строки БД (ErrNotFound при отсутствии).
func scanRequest(row interface{ Scan(...any) error }) (*model.Request, error) {
	var r model.Request
	var tStart, tFirst, tEnd string
	if err := row.Scan(&r.ID, &r.SID, &r.TurnID, &r.Server, &r.Path, &r.Status,
		&tStart, &tFirst, &tEnd, &r.HeldMS, &r.Error); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("request: %w", err)
	}
	r.TStart, _ = time.Parse(time.RFC3339Nano, tStart)
	if tFirst != "" {
		r.TFirstByte, _ = time.Parse(time.RFC3339Nano, tFirst)
	}
	r.TEnd, _ = time.Parse(time.RFC3339Nano, tEnd)
	return &r, nil
}
