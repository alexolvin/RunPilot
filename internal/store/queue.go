package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"runpilot/internal/model"
)

// QueueUpsert — запись очереди (queue_entry, раздел 4 ТЗ).
// Обновляет существующую запись с тем же sid (перепостановка).
func (s *Store) QueueUpsert(e model.QueueEntry) error {
	return s.DoWrite(func(tx *sql.Tx) error {
		kind := string(e.Constraint.Kind)
		server := e.Constraint.Server
		if e.Constraint.Kind == model.ConstraintNone {
			kind = "none"
		}
		_, err := tx.Exec(`
INSERT INTO queue_entry
	(sid, class, enqueued_at, not_before, constraint_kind, constraint_server,
	 after_sid, mode, held_request, ineligible_reason, ineligible_since)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(sid) DO UPDATE SET
	class = excluded.class,
	enqueued_at = excluded.enqueued_at,
	not_before = excluded.not_before,
	constraint_kind = excluded.constraint_kind,
	constraint_server = excluded.constraint_server,
	after_sid = excluded.after_sid,
	mode = excluded.mode,
	held_request = excluded.held_request,
	ineligible_reason = excluded.ineligible_reason,
	ineligible_since = excluded.ineligible_since`,
			e.SID, int(e.Class), e.EnqueuedAt.Format(time.RFC3339Nano),
			e.NotBefore.Format(time.RFC3339Nano), kind, server, e.AfterSID,
			string(e.Mode), boolInt(e.HeldRequest), e.IneligibleReason,
			e.IneligibleSince.Format(time.RFC3339Nano))
		return err
	})
}

// QueueGet — запись по sid (ErrNotFound если её нет).
func (s *Store) QueueGet(sid string) (*model.QueueEntry, error) {
	row := s.db.QueryRow(`
SELECT sid, class, enqueued_at, not_before, constraint_kind, constraint_server,
	after_sid, mode, held_request, ineligible_reason, ineligible_since
FROM queue_entry WHERE sid = ?`, sid)
	var e model.QueueEntry
	var class int
	var enq, notBefore, inelig string
	var held int
	var kind, server, mode string
	if err := row.Scan(&e.SID, &class, &enq, &notBefore, &kind, &server,
		&e.AfterSID, &mode, &held, &e.IneligibleReason, &inelig); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("queue_get: %w", err)
	}
	e.Class = model.QueueClass(class)
	e.Constraint = constraintOf(kind, server)
	t, _ := time.Parse(time.RFC3339Nano, enq)
	e.EnqueuedAt = t
	t, _ = time.Parse(time.RFC3339Nano, notBefore)
	e.NotBefore = t
	t, _ = time.Parse(time.RFC3339Nano, inelig)
	e.IneligibleSince = t
	e.Mode = model.QueueMode(mode)
	e.HeldRequest = held != 0
	return &e, nil
}

// QueueDelete удаляет запись (например, неявный захват слота, раздел 6 ТЗ).
func (s *Store) QueueDelete(sid string) error {
	return s.DoWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(`DELETE FROM queue_entry WHERE sid = ?`, sid)
		return err
	})
}

// QueueSetHeldRequest — флаг held_request и/или режим записи.
// Режимы передаются в model.QueueMode; пустой режим не меняется.
func (s *Store) QueueSetHeldRequest(sid string, held bool, mode model.QueueMode) error {
	return s.DoWrite(func(tx *sql.Tx) error {
		var res sql.Result
		var err error
		if mode != "" {
			res, err = tx.Exec(`UPDATE queue_entry SET held_request = ?, mode = ? WHERE sid = ?`,
				boolInt(held), string(mode), sid)
		} else {
			res, err = tx.Exec(`UPDATE queue_entry SET held_request = ? WHERE sid = ?`,
				boolInt(held), sid)
		}
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// QueuePosition — позиция записи в очереди (1 = первая): число записей с
// более высоким классом либо тем же классом и не более поздним
// enqueued_at (однородная очередь раздела 5, Э3: без aging).
func (s *Store) QueuePosition(sid string) (int, error) {
	var pos int
	err := s.db.QueryRow(`
SELECT 1 + (
	SELECT COUNT(*) FROM queue_entry q
	WHERE q.sid <> ?
	  AND (q.class < m.class
	       OR (q.class = m.class AND q.enqueued_at < m.enqueued_at)
	       OR (q.class = m.class AND q.enqueued_at = m.enqueued_at AND q.sid < m.sid)))
FROM queue_entry m WHERE m.sid = ?`, sid, sid).Scan(&pos)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("queue_position: %w", err)
	}
	return pos, nil
}

// QueueList — все записи по порядку очереди (для /state и TUI, Э5).
func (s *Store) QueueList() ([]model.QueueEntry, error) {
	rows, err := s.db.Query(`
SELECT sid, class, enqueued_at, not_before, constraint_kind, constraint_server,
	after_sid, mode, held_request, ineligible_reason, ineligible_since
FROM queue_entry
ORDER BY class, enqueued_at, sid`)
	if err != nil {
		return nil, fmt.Errorf("queue_list: %w", err)
	}
	defer rows.Close()
	var out []model.QueueEntry
	for rows.Next() {
		var e model.QueueEntry
		var class int
		var enq, notBefore, inelig string
		var held int
		var kind, server, mode string
		if err := rows.Scan(&e.SID, &class, &enq, &notBefore, &kind, &server,
			&e.AfterSID, &mode, &held, &e.IneligibleReason, &inelig); err != nil {
			return nil, err
		}
		e.Class = model.QueueClass(class)
		e.Constraint = constraintOf(kind, server)
		t, _ := time.Parse(time.RFC3339Nano, enq)
		e.EnqueuedAt = t
		t, _ = time.Parse(time.RFC3339Nano, notBefore)
		e.NotBefore = t
		t, _ = time.Parse(time.RFC3339Nano, inelig)
		e.IneligibleSince = t
		e.Mode = model.QueueMode(mode)
		e.HeldRequest = held != 0
		out = append(out, e)
	}
	return out, rows.Err()
}

// constraintOf — constraint_kind/constraint_server → model.Constraint.
func constraintOf(kind, server string) model.Constraint {
	if kind == "" || kind == "none" {
		return model.NoConstraint
	}
	c, ok := model.ConstraintParse(kind + ":" + server)
	if !ok {
		return model.NoConstraint
	}
	return c
}
