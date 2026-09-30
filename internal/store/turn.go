package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"runpilot/internal/model"
)

// TurnStart — новый ход (подтверждение старта, раздел 8 ТЗ). Возвращает id.
// Имя сессии снимается в строку хода: «Ходы» журнала показывают его после
// удаления сессии (из таблицы session его уже не восстановить).
func (s *Store) TurnStart(sid string, startedAt time.Time) (int64, error) {
	var id int64
	err := s.DoWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`
INSERT INTO turn (sid, started_at, session_name)
VALUES (?, ?, COALESCE((SELECT name FROM session WHERE sid = ?), ''))`,
			sid, ts(startedAt), sid)
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		return err
	})
	return id, err
}

// OpenTurn — незакрытый ход сессии (ended_at пуст); ErrNotFound, если нет.
func (s *Store) OpenTurn(sid string) (*model.Turn, error) {
	row := s.db.QueryRow(`
SELECT id, sid, session_name, started_at, ended_at, servers, outcome, requests
FROM turn WHERE sid = ? AND ended_at = '' ORDER BY id DESC LIMIT 1`, sid)
	return scanTurn(row)
}

// TurnOKSince — есть ли у сессии ход с исходом OK, завершившийся после
// since (зависимости --after, раздел 5/9 ТЗ).
func (s *Store) TurnOKSince(sid string, since time.Time) (bool, error) {
	var n int
	err := s.db.QueryRow(`
SELECT COUNT(*) FROM turn
WHERE sid = ? AND outcome = 'OK' AND ended_at >= ?`, sid, ts(since)).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("turn_ok_since: %w", err)
	}
	return n > 0, nil
}

// TurnClose — закрыть ход (раздел 8 ТЗ: длительность, число запросов,
// исход, серверы > 1 при миграции — через serversCSV).
func (s *Store) TurnClose(id int64, endedAt time.Time, outcome model.TurnOutcome, requests int, serversCSV string) error {
	return s.DoWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`
UPDATE turn SET ended_at = ?, outcome = ?, requests = ?, servers = ?
WHERE id = ?`, ts(endedAt), string(outcome), requests, serversCSV, id)
		if err != nil {
			return err
		}
		return requireRows(res)
	})
}

// LastGenerative — последний генерирующий запрос сессии после from
// (исход хода по разделу 8 ТЗ).
func (s *Store) LastGenerative(sid string, from time.Time) (*model.Request, error) {
	row := s.db.QueryRow(`
SELECT `+requestCols+` FROM request
WHERE sid = ? AND t_start >= ?
  AND path IN ('v1/chat/completions', 'v1/completions', 'v1/responses')
ORDER BY t_start DESC, id DESC LIMIT 1`, sid, ts(from))
	return scanRequest(row)
}

// CountRequestsSince — число запросов сессии с момента (для счётчика хода).
func (s *Store) CountRequestsSince(sid string, since time.Time) (int, error) {
	var n int
	err := s.db.QueryRow(`
SELECT COUNT(*) FROM request WHERE sid = ? AND t_start >= ?`, sid, ts(since)).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count_requests: %w", err)
	}
	return n, nil
}

// ServersSince — множество серверов запросов сессии с момента (CSV,
// по порядку первого запроса).
func (s *Store) ServersSince(sid string, since time.Time) (string, error) {
	rows, err := s.db.Query(`
SELECT server FROM (
	SELECT server, MIN(t_start) AS m FROM request
	WHERE sid = ? AND t_start >= ? GROUP BY server)
ORDER BY m`, sid, ts(since))
	if err != nil {
		return "", fmt.Errorf("servers_since: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var srv string
		if err := rows.Scan(&srv); err != nil {
			return "", err
		}
		out = append(out, srv)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return joinCSV(out), nil
}

// ListTurnsSince — закрытые ходы, завершившиеся после since (runpilot stats).
func (s *Store) ListTurnsSince(since time.Time) ([]model.Turn, error) {
	rows, err := s.db.Query(`
SELECT id, sid, session_name, started_at, ended_at, servers, outcome, requests
FROM turn WHERE ended_at != '' AND ended_at >= ? ORDER BY ended_at`, ts(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Turn
	for rows.Next() {
		t, err := scanTurn(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

func joinCSV(list []string) string {
	out := ""
	for i, v := range list {
		if i > 0 {
			out += ","
		}
		out += v
	}
	return out
}

func scanTurn(row interface{ Scan(...any) error }) (*model.Turn, error) {
	var t model.Turn
	var started, ended, servers, outcome string
	if err := row.Scan(&t.ID, &t.SID, &t.SessionName, &started, &ended, &servers, &outcome,
		&t.Requests); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("turn: %w", err)
	}
	t.StartedAt, _ = time.Parse(time.RFC3339Nano, started)
	if ended != "" {
		t.EndedAt, _ = time.Parse(time.RFC3339Nano, ended)
	}
	t.Outcome = model.TurnOutcome(outcome)
	t.Servers = splitCSVServers(servers)
	return &t, nil
}

// splitCSVServers — CSV-строка серверов хода → []string (пусто, если нет).
func splitCSVServers(csv string) []string {
	if csv == "" {
		return nil
	}
	var out []string
	start := 0
	for i := 0; i < len(csv); i++ {
		if csv[i] == ',' {
			if start < i {
				out = append(out, csv[start:i])
			}
			start = i + 1
		}
	}
	if start < len(csv) {
		out = append(out, csv[start:])
	}
	return out
}
