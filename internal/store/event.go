package store

import (
	"database/sql"
	"time"

	"runpilot/internal/model"
)

// EventRecord — событие каталога приложения Б ТЗ. Асинхронно (аудит и
// SSE-лента TUI, не блокирует диспетчеризацию).
func (s *Store) EventRecord(ev model.Event) error {
	return s.QueueWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(`
INSERT INTO event (ts, kind, sid, server, payload) VALUES (?, ?, ?, ?, ?)`,
			ts(ev.TS), ev.Kind, ev.SID, ev.Server, string(ev.Payload))
		return err
	})
}

// EventList — события после id (лента SSE, Э5).
func (s *Store) EventList(afterID int64, limit int) ([]model.Event, error) {
	rows, err := s.db.Query(`
SELECT id, ts, kind, sid, server, payload FROM event
WHERE id > ? ORDER BY id LIMIT ?`, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Event
	for rows.Next() {
		var e model.Event
		var tsStr, payload string
		if err := rows.Scan(&e.ID, &tsStr, &e.Kind, &e.SID, &e.Server, &payload); err != nil {
			return nil, err
		}
		e.TS, _ = time.Parse(time.RFC3339Nano, tsStr)
		e.Payload = []byte(payload)
		out = append(out, e)
	}
	return out, rows.Err()
}

// EventMaxID — id последнего события (точка входа SSE).
func (s *Store) EventMaxID() (int64, error) {
	var id int64
	err := s.db.QueryRow(`SELECT COALESCE(MAX(id), 0) FROM event`).Scan(&id)
	return id, err
}
