package store

// Серверы (v2 раздел 3.4): переезжают из YAML в БД.
// config.Server хранится как JSON в столбце data; имя — PK (неизменяемое).

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"runpilot/internal/config"
)

// ListServers — все серверы по имени.
func (s *Store) ListServers() ([]config.Server, error) {
	rows, err := s.db.Query(`SELECT name, data FROM server ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("store: servers: %w", err)
	}
	defer rows.Close()
	var out []config.Server
	for rows.Next() {
		var name, data string
		if err := rows.Scan(&name, &data); err != nil {
			return nil, err
		}
		var srv config.Server
		if err := json.Unmarshal([]byte(data), &srv); err != nil {
			return nil, fmt.Errorf("store: server %s: разбор: %w", name, err)
		}
		out = append(out, srv)
	}
	return out, rows.Err()
}

// GetServer — сервер по имени.
func (s *Store) GetServer(name string) (config.Server, error) {
	var data string
	err := s.db.QueryRow(`SELECT data FROM server WHERE name = ?`, name).Scan(&data)
	if err != nil {
		if err == sql.ErrNoRows {
			return config.Server{}, ErrNotFound
		}
		return config.Server{}, err
	}
	var srv config.Server
	if err := json.Unmarshal([]byte(data), &srv); err != nil {
		return config.Server{}, fmt.Errorf("store: server %s: разбор: %w", name, err)
	}
	return srv, nil
}

// UpsertServer — создать или обновить сервер (целиком, по JSON).
func (s *Store) UpsertServer(srv config.Server, now time.Time) error {
	data, err := json.Marshal(srv)
	if err != nil {
		return fmt.Errorf("store: server %s: сериализация: %w", srv.Name, err)
	}
	return s.DoWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(
			`INSERT INTO server (name, data, version, created_at, updated_at)
			 VALUES (?, ?, 1, ?, ?)
			 ON CONFLICT(name) DO UPDATE SET data=excluded.data,
			    version=server.version+1, updated_at=excluded.updated_at`,
			srv.Name, string(data), ts(now), ts(now))
		return err
	})
}

// DeleteServer — удалить сервер по имени (ErrNotFound, если нет).
func (s *Store) DeleteServer(name string) error {
	return s.DoWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`DELETE FROM server WHERE name = ?`, name)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// ReplaceServers — заменить весь список серверов (для миграции/импорта):
// удаляет отсутствующие в new, вставляет/обновляет присутствующие.
func (s *Store) ReplaceServers(new []config.Server, now time.Time) error {
	return s.DoWrite(func(tx *sql.Tx) error {
		rows, err := tx.Query(`SELECT name FROM server`)
		if err != nil {
			return err
		}
		var existing []string
		for rows.Next() {
			var n string
			if err := rows.Scan(&n); err != nil {
				rows.Close()
				return err
			}
			existing = append(existing, n)
		}
		rows.Close()
		present := make(map[string]bool, len(new))
		for _, srv := range new {
			present[srv.Name] = true
		}
		for _, n := range existing {
			if !present[n] {
				if _, err := tx.Exec(`DELETE FROM server WHERE name = ?`, n); err != nil {
					return err
				}
			}
		}
		for _, srv := range new {
			data, err := json.Marshal(srv)
			if err != nil {
				return fmt.Errorf("store: server %s: сериализация: %w", srv.Name, err)
			}
			if _, err := tx.Exec(
				`INSERT INTO server (name, data, version, created_at, updated_at)
				 VALUES (?, ?, 1, ?, ?)
				 ON CONFLICT(name) DO UPDATE SET data=excluded.data,
				    version=server.version+1, updated_at=excluded.updated_at`,
				srv.Name, string(data), ts(now), ts(now)); err != nil {
				return err
			}
		}
		return nil
	})
}
