package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"runpilot/internal/model"
)

// LeaseCreate — новая аренда (раздел 4 ТЗ). Возвращает id.
func (s *Store) LeaseCreate(l model.Lease) (int64, error) {
	l.GrantedAt = l.GrantedAt.UTC()
	var id int64
	err := s.DoWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`
INSERT INTO lease (sid, server, slot, state, origin, granted_at, requests)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
			l.SID, l.Server, l.Slot, l.State, l.Origin,
			l.GrantedAt.Format(time.RFC3339Nano), l.Requests)
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		return err
	})
	return id, err
}

// LeaseCreateIfAbsent — атомарная выдача аренды (Э4): если у sid уже есть
// действующая аренда (её выдал другой участник — шлюз или планировщик),
// возвращается существующая; иначе создаётся новая. deleteQueue — удалить
// запись очереди в той же транзакции (неявные и held-выдачи; плановая
// выдача PENDING запись сохраняет для возврата в очередь).
func (s *Store) LeaseCreateIfAbsent(l model.Lease, deleteQueue bool) (*model.Lease, error) {
	l.GrantedAt = l.GrantedAt.UTC()
	var id int64
	err := s.DoWrite(func(tx *sql.Tx) error {
		err := tx.QueryRow(`
SELECT id FROM lease
WHERE sid = ? AND state IN ('PENDING', 'ACTIVE', 'RECOVERING')
ORDER BY granted_at DESC, id DESC LIMIT 1`, l.SID).Scan(&id)
		if err == nil {
			return nil // чужая действующая аренда — используем её
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		res, err := tx.Exec(`
INSERT INTO lease (sid, server, slot, state, origin, granted_at, requests)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
			l.SID, l.Server, l.Slot, l.State, l.Origin,
			l.GrantedAt.Format(time.RFC3339Nano), l.Requests)
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		if err != nil {
			return err
		}
		if deleteQueue {
			if _, err := tx.Exec(`DELETE FROM queue_entry WHERE sid = ?`, l.SID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("lease_create_if_absent: %w", err)
	}
	return s.LeaseGetByID(id)
}

// LeaseGet — действующая аренда sid: последняя не-RELEASED (PENDING /
// ACTIVE / RECOVERING); ErrNotFound, если её нет.
func (s *Store) LeaseGet(sid string) (*model.Lease, error) {
	row := s.db.QueryRow(`
SELECT id, sid, server, slot, state, origin, granted_at, released_at,
	release_reason, requests
FROM lease WHERE sid = ? AND state IN ('PENDING', 'ACTIVE', 'RECOVERING')
ORDER BY granted_at DESC, id DESC LIMIT 1`, sid)
	l, err := scanLease(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return l, nil
}

// LeaseGetByID — по id (для last_request_end и служебных операций).
func (s *Store) LeaseGetByID(id int64) (*model.Lease, error) {
	row := s.db.QueryRow(`
SELECT id, sid, server, slot, state, origin, granted_at, released_at,
	release_reason, requests FROM lease WHERE id = ?`, id)
	l, err := scanLease(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return l, err
}

// LeaseRelease — освобождение аренды (state → RELEASED).
// Повторное освобождение существующей RELEASED-строки — не ошибка (idempotent).
func (s *Store) LeaseRelease(sid, reason string, now time.Time) error {
	return s.DoWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`
UPDATE lease SET state = 'RELEASED', released_at = ?, release_reason = ?
WHERE id = (
	SELECT id FROM lease
	WHERE sid = ? AND state IN ('PENDING', 'ACTIVE', 'RECOVERING')
	ORDER BY granted_at DESC, id DESC LIMIT 1)`,
			ts(now), reason, sid)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		// 0 строк = аренды не было (идемпотентность).
		_ = n
		return nil
	})
}

// LeaseSetActive — PENDING → ACTIVE (подтверждение старта, раздел 6 ТЗ).
func (s *Store) LeaseSetActive(sid string) error {
	return s.DoWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`
UPDATE lease SET state = 'ACTIVE'
WHERE id = (
	SELECT id FROM lease
	WHERE sid = ? AND state = 'PENDING'
	ORDER BY granted_at DESC, id DESC LIMIT 1)`, sid)
		if err != nil {
			return err
		}
		return requireRows(res)
	})
}

// LeaseBumpRequest — запрос учтён в аренде (requests + 1).
func (s *Store) LeaseBumpRequest(sid string) error {
	return s.DoWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`
UPDATE lease SET requests = requests + 1
WHERE id = (
	SELECT id FROM lease
	WHERE sid = ? AND state IN ('PENDING', 'ACTIVE')
	ORDER BY granted_at DESC, id DESC LIMIT 1)`, sid)
		if err != nil {
			return err
		}
		return requireRows(res)
	})
}

// LeaseCountByServer — действующих аренд (PENDING + ACTIVE) на сервере.
func (s *Store) LeaseCountByServer(server string) (int, error) {
	var n int
	err := s.db.QueryRow(`
SELECT COUNT(*) FROM lease
WHERE server = ? AND state IN ('PENDING', 'ACTIVE')`, server).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("lease_count: %w", err)
	}
	return n, nil
}

// LeaseListByServer — действующие аренды сервера (занятые слоты,
// планировщик).
func (s *Store) LeaseListByServer(server string) ([]model.Lease, error) {
	rows, err := s.db.Query(`
SELECT id, sid, server, slot, state, origin, granted_at, released_at,
	release_reason, requests
FROM lease WHERE server = ? AND state IN ('PENDING', 'ACTIVE')
ORDER BY id`, server)
	if err != nil {
		return nil, fmt.Errorf("lease_list_by_server: %w", err)
	}
	defer rows.Close()
	var out []model.Lease
	for rows.Next() {
		l, err := scanLease(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *l)
	}
	return out, rows.Err()
}

// LatestGrantByServer — время последней выдачи аренды на сервере
// (тёплый кэш, раздел 5 ТЗ); excludeSID — пропустить сессию.
func (s *Store) LatestGrantByServer(server, excludeSID string) (time.Time, bool, error) {
	var granted string
	err := s.db.QueryRow(`
SELECT granted_at FROM lease
WHERE server = ? AND state IN ('PENDING', 'ACTIVE') AND sid <> ?
ORDER BY granted_at DESC, id DESC LIMIT 1`, server, excludeSID).Scan(&granted)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("latest_grant_by_server: %w", err)
	}
	t, err := time.Parse(time.RFC3339Nano, granted)
	return t, true, err
}

// LeaseListActive — все действующие аренды (state API, диагностика).
func (s *Store) LeaseListActive() ([]model.Lease, error) {
	rows, err := s.db.Query(`
SELECT id, sid, server, slot, state, origin, granted_at, released_at,
	release_reason, requests
FROM lease WHERE state IN ('PENDING', 'ACTIVE', 'RECOVERING')
ORDER BY granted_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Lease
	for rows.Next() {
		var l model.Lease
		var state, origin, granted, released, reason string
		if err := rows.Scan(&l.ID, &l.SID, &l.Server, &l.Slot, &state, &origin,
			&granted, &released, &reason, &l.Requests); err != nil {
			return nil, err
		}
		l.State = model.LeaseState(state)
		l.Origin = model.LeaseOrigin(origin)
		l.GrantedAt, _ = time.Parse(time.RFC3339Nano, granted)
		if released != "" {
			l.ReleasedAt, _ = time.Parse(time.RFC3339Nano, released)
		}
		l.ReleaseReason = reason
		out = append(out, l)
	}
	return out, rows.Err()
}

func scanLease(row interface{ Scan(...any) error }) (*model.Lease, error) {
	var l model.Lease
	var state, origin, granted, released, reason string
	if err := row.Scan(&l.ID, &l.SID, &l.Server, &l.Slot, &state, &origin,
		&granted, &released, &reason, &l.Requests); err != nil {
		return nil, err
	}
	l.State = model.LeaseState(state)
	l.Origin = model.LeaseOrigin(origin)
	l.GrantedAt, _ = time.Parse(time.RFC3339Nano, granted)
	if released != "" {
		l.ReleasedAt, _ = time.Parse(time.RFC3339Nano, released)
	}
	l.ReleaseReason = reason
	return &l, nil
}
