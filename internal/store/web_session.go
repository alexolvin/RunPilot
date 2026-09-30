package store

import (
	"database/sql"
	"errors"
	"time"
)

// WebSession — сессия браузера (v2 раздел 3.4). В БД — SHA-256 идентификатора.
type WebSession struct {
	IDHash    string
	CSRFToken string
	CreatedAt time.Time
	LastSeen  time.Time
	UserAgent string
	TSLogin  time.Time
	RevokedAt time.Time
}

// CreateWebSession — новая браузерная сессия (вход).
func (s *Store) CreateWebSession(ws WebSession) error {
	return s.DoWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO web_session
			(id_hash, csrf_token, created_at, last_seen, user_agent, ts_login, revoked_at)
			VALUES (?, ?, ?, ?, ?, ?, '')`,
			ws.IDHash, ws.CSRFToken, ts(ws.CreatedAt), ts(ws.LastSeen),
			ws.UserAgent, ts(ws.TSLogin))
		return err
	})
}

// GetWebSession — сессия по id_hash (ErrNotFound если нет или отозвана).
func (s *Store) GetWebSession(idHash string) (WebSession, error) {
	row := s.db.QueryRow(`SELECT id_hash, csrf_token, created_at, last_seen,
		user_agent, ts_login, revoked_at FROM web_session WHERE id_hash = ?`, idHash)
	var ws WebSession
	var created, seen, login, revoked string
	err := row.Scan(&ws.IDHash, &ws.CSRFToken, &created, &seen, &ws.UserAgent, &login, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return WebSession{}, ErrNotFound
	}
	if err != nil {
		return WebSession{}, err
	}
	ws.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	ws.LastSeen, _ = time.Parse(time.RFC3339Nano, seen)
	ws.TSLogin, _ = time.Parse(time.RFC3339Nano, login)
	ws.RevokedAt, _ = time.Parse(time.RFC3339Nano, revoked)
	if revoked != "" {
		return WebSession{}, ErrNotFound
	}
	return ws, nil
}

// TouchWebSession — обновить last_seen (продление).
func (s *Store) TouchWebSession(idHash string, lastSeen time.Time) error {
	return s.DoWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE web_session SET last_seen = ? WHERE id_hash = ?`,
			ts(lastSeen), idHash)
		return err
	})
}

// ListWebSessions — все активные сессии (для «Доступ»).
func (s *Store) ListWebSessions() ([]WebSession, error) {
	rows, err := s.db.Query(`SELECT id_hash, csrf_token, created_at, last_seen,
		user_agent, ts_login, revoked_at FROM web_session
		WHERE revoked_at = '' ORDER BY last_seen DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WebSession
	for rows.Next() {
		var ws WebSession
		var created, seen, login, revoked string
		if err := rows.Scan(&ws.IDHash, &ws.CSRFToken, &created, &seen, &ws.UserAgent, &login, &revoked); err != nil {
			return nil, err
		}
		ws.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		ws.LastSeen, _ = time.Parse(time.RFC3339Nano, seen)
		ws.TSLogin, _ = time.Parse(time.RFC3339Nano, login)
		out = append(out, ws)
	}
	return out, rows.Err()
}

// RevokeWebSession — отозвать сессию (текущую отозвать нельзя — только «Выйти»).
func (s *Store) RevokeWebSession(idHash string, at time.Time) error {
	return s.DoWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE web_session SET revoked_at = ? WHERE id_hash = ? AND revoked_at = ''`,
			ts(at), idHash)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return nil
	})
}
