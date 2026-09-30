package store

import (
	"database/sql"
	"time"

	"runpilot/internal/model"
)

// JournalPage — страница журнала с серверной пагинацией по id-cursor
// (v2 раздел 11.8: пагинация на сервере, размер web.page_size).
// beforeID = 0 → от самых новых; иначе id < beforeID (следующая страница).

// EventPage — события (вкладка «События» журнала), от новых к старым.
func (s *Store) EventPage(beforeID int64, limit int) ([]model.Event, error) {
	rows, err := s.db.Query(`
SELECT id, ts, kind, sid, server, payload FROM event
`+beforeIDClause(beforeID)+` ORDER BY id DESC LIMIT ?`, argsWithBefore(beforeID, limit)...)
	if err != nil {
		return nil, err
	}
	return scanEventRows(rows)
}

// auditKinds — виды событий, составляющих вкладку «Аудит» (действия оператора;
// R5: в payload только метаданные, без текстов).
const auditKinds = `'paste','term_open','term_close'`

// AuditPage — аудит (вкладка «Аудит» журнала): действия оператора,
// от новых к старым.
func (s *Store) AuditPage(beforeID int64, limit int) ([]model.Event, error) {
	q := `
SELECT id, ts, kind, sid, server, payload FROM event
WHERE kind IN (` + auditKinds + `)`
	args := []any{}
	if beforeID != 0 {
		q += ` AND id < ?`
		args = append(args, beforeID)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	return scanEventRows(rows)
}

// scanEventRows — чтение строк event (EventPage/AuditPage).
func scanEventRows(rows *sql.Rows) ([]model.Event, error) {
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

// TurnPage — ходы (вкладка «Ходы» журнала), от новых к старым.
func (s *Store) TurnPage(beforeID int64, limit int) ([]model.Turn, error) {
	rows, err := s.db.Query(`
SELECT id, sid, session_name, started_at, ended_at, servers, outcome, requests
FROM turn
`+beforeIDClause(beforeID)+` ORDER BY id DESC LIMIT ?`, argsWithBefore(beforeID, limit)...)
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

// beforeIDClause / argsWithBefore — параметризованная пагинация по id.
// beforeID=0 — без ограничения (от новых); иначе «WHERE id < ?».
func beforeIDClause(beforeID int64) string {
	if beforeID == 0 {
		return ""
	}
	return "WHERE id < " + "?"
}

func argsWithBefore(beforeID int64, limit int) []any {
	if beforeID == 0 {
		return []any{limit}
	}
	return []any{beforeID, limit}
}
