package store

import (
	"database/sql"
	"errors"
	"time"
)

// Notification — запись центра уведомлений (v2 раздел 3.4, 17).
// Дедупликация по Key (kind + сущность): одно активное на ключ.
type Notification struct {
	ID         int64     `json:"id"`
	Key        string    `json:"key"`
	Severity   string    `json:"severity"`
	Kind       string    `json:"kind"`
	SID        string    `json:"sid,omitempty"`
	Server     string    `json:"server,omitempty"`
	Host       string    `json:"host,omitempty"`
	Title      string    `json:"title"`
	Count      int       `json:"count"`
	FirstTS    time.Time `json:"first_ts"`
	LastTS     time.Time `json:"last_ts"`
	ReadAt     time.Time `json:"read_at,omitempty"`
	ResolvedAt time.Time `json:"resolved_at,omitempty"`
}

const notificationCols = `id, key, severity, kind, sid, server, host, title,
	count, first_ts, last_ts, read_at, resolved_at`

// scanNotification — строка notification из строки БД.
func scanNotification(row interface{ Scan(...any) error }) (*Notification, error) {
	var n Notification
	var first, last, read, resolved string
	if err := row.Scan(&n.ID, &n.Key, &n.Severity, &n.Kind, &n.SID, &n.Server,
		&n.Host, &n.Title, &n.Count, &first, &last, &read, &resolved); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	n.FirstTS, _ = time.Parse(time.RFC3339Nano, first)
	n.LastTS, _ = time.Parse(time.RFC3339Nano, last)
	if read != "" {
		n.ReadAt, _ = time.Parse(time.RFC3339Nano, read)
	}
	if resolved != "" {
		n.ResolvedAt, _ = time.Parse(time.RFC3339Nano, resolved)
	}
	return &n, nil
}

// UpsertNotification — синхронизация одного активного уведомления (раздел 17):
// активное с тем же ключом — обновить last_ts (count не растёт при повторной
// фиксации); не активное/отсутствующее — новая запись (count = 1).
func (s *Store) UpsertNotification(n Notification) error {
	return s.DoWrite(func(tx *sql.Tx) error {
		var exists int
		err := tx.QueryRow(`SELECT 1 FROM notification WHERE key = ? AND resolved_at = ''`,
			n.Key).Scan(&exists)
		if err == nil {
			_, err = tx.Exec(`UPDATE notification SET last_ts = ?, severity = ?, title = ?
				WHERE key = ? AND resolved_at = ''`,
				ts(n.LastTS), n.Severity, n.Title, n.Key)
			return err
		}
		_, err = tx.Exec(`
INSERT INTO notification (key, severity, kind, sid, server, host, title, count,
	first_ts, last_ts)
VALUES (?, ?, ?, ?, ?, ?, ?, 1, ?, ?)`,
			n.Key, n.Severity, n.Kind, n.SID, n.Server, n.Host, n.Title,
			ts(n.FirstTS), ts(n.LastTS))
		return err
	})
}

// ResolveInactiveNotifications — закрыть активные, чей ключ не в currentKeys
// (условие исчезло, раздел 17).
func (s *Store) ResolveInactiveNotifications(currentKeys []string, now time.Time) error {
	current := make(map[string]bool, len(currentKeys))
	for _, k := range currentKeys {
		current[k] = true
	}
	return s.DoWrite(func(tx *sql.Tx) error {
		rows, err := tx.Query(`SELECT id, key FROM notification WHERE resolved_at = ''`)
		if err != nil {
			return err
		}
		type idKey struct {
			id  int64
			key string
		}
		var active []idKey
		for rows.Next() {
			var ik idKey
			if err := rows.Scan(&ik.id, &ik.key); err != nil {
				rows.Close()
				return err
			}
			active = append(active, ik)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		for _, ik := range active {
			if current[ik.key] {
				continue
			}
			if _, err := tx.Exec(`UPDATE notification SET resolved_at = ?
				WHERE id = ? AND resolved_at = ''`, ts(now), ik.id); err != nil {
				return err
			}
		}
		return nil
	})
}

// ListNotifications — активные (all=false) или все (all=true), от новых.
func (s *Store) ListNotifications(all bool) ([]Notification, error) {
	cond := `WHERE resolved_at = ''`
	if all {
		cond = ``
	}
	rows, err := s.db.Query(`SELECT ` + notificationCols + ` FROM notification ` + cond +
		` ORDER BY last_ts DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Notification
	for rows.Next() {
		n, err := scanNotification(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *n)
	}
	return out, rows.Err()
}

// MarkNotificationRead — отметить одно прочитанным.
func (s *Store) MarkNotificationRead(id int64, now time.Time) error {
	return s.DoWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE notification SET read_at = ? WHERE id = ?`, ts(now), id)
		if err != nil {
			return err
		}
		return requireRows(res)
	})
}

// MarkAllNotificationsRead — отметить все активные прочитанными.
func (s *Store) MarkAllNotificationsRead(now time.Time) error {
	return s.DoWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE notification SET read_at = ?
			WHERE resolved_at = '' AND read_at = ''`, ts(now))
		return err
	})
}
