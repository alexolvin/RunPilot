package store

// Операторские состояния (п.1): персистятся при изменении и восстанавливаются
// при старте координатора. Per-name (сервер/узел) drain/pause. Режим/пауза
// планировщика — в meta (см. SchedulerPaused/SetSchedulerPaused).

import "database/sql"

// Виды операторских состояний (per-name).
const (
	OperatorStateServerDrain  = "server_drain"
	OperatorStateServerPaused = "server_paused"
	OperatorStateNodeDrain    = "node_drain"
)

// SetOperatorState — записать (on=true) или снять (on=false) операторское
// состояние по имени (сервер/узел) и виду.
func (s *Store) SetOperatorState(name, kind string, on bool) error {
	return s.DoWrite(func(tx *sql.Tx) error {
		if on {
			_, err := tx.Exec(`INSERT INTO operator_state (name, kind) VALUES (?, ?)
				ON CONFLICT(name, kind) DO NOTHING`, name, kind)
			return err
		}
		_, err := tx.Exec(`DELETE FROM operator_state WHERE name = ? AND kind = ?`, name, kind)
		return err
	})
}

// OperatorStates — отсортированные имена с заданным видом (пусто, если нет).
func (s *Store) OperatorStates(kind string) ([]string, error) {
	rows, err := s.db.Query(`SELECT name FROM operator_state WHERE kind = ? ORDER BY name`, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
