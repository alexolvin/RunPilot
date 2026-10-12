package store

import (
	"database/sql"
	"time"

	"runpilot/internal/model"
)

// RelayLink — строка relay_link: автомат цикла связи (ТЗ Relay §4, §5).
// Тексты в БД не пишутся — только SHA-256, размеры и время.
type RelayLink struct {
	SID           string
	Site          string
	ChatKey       string
	State         model.RelayState
	HoldReason    string
	Kind          string // report | question
	Source        string // recording | terminal
	CycleSeq      int
	AutoCycles    int
	SentHash      string
	SentAt        time.Time
	AnswerHash    string
	AnswerAt      time.Time
	DeliveredHash string
	DeliveredAt   time.Time
	UpdatedAt     time.Time
}

const relayLinkCols = `sid, site, chat_key, state, hold_reason, kind, source,
cycle_seq, auto_cycles, sent_hash, sent_at, answer_hash, answer_at,
delivered_hash, delivered_at, updated_at`

func scanRelayLink(row interface{ Scan(...any) error }) (RelayLink, error) {
	var r RelayLink
	var state, sentAt, answerAt, deliveredAt, updated string
	err := row.Scan(&r.SID, &r.Site, &r.ChatKey, &state, &r.HoldReason, &r.Kind,
		&r.Source, &r.CycleSeq, &r.AutoCycles, &r.SentHash, &sentAt,
		&r.AnswerHash, &answerAt, &r.DeliveredHash, &deliveredAt, &updated)
	if err != nil {
		return r, err
	}
	r.State = model.RelayState(state)
	r.SentAt, _ = time.Parse(time.RFC3339Nano, sentAt)
	r.AnswerAt, _ = time.Parse(time.RFC3339Nano, answerAt)
	r.DeliveredAt, _ = time.Parse(time.RFC3339Nano, deliveredAt)
	r.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return r, nil
}

// GetRelayLink — по sid.
func (s *Store) GetRelayLink(sid string) (RelayLink, error) {
	row := s.DB().QueryRow("SELECT "+relayLinkCols+` FROM relay_link WHERE sid = ?`, sid)
	r, err := scanRelayLink(row)
	if err == sql.ErrNoRows {
		return RelayLink{}, ErrNotFound
	}
	return r, err
}

// UpsertRelayLink — вставить или обновить связь (сначала БД, потом побочное —
// ТЗ Relay §строгое 4).
func (s *Store) UpsertRelayLink(r RelayLink) error {
	return s.DoWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO relay_link (
			sid, site, chat_key, state, hold_reason, kind, source,
			cycle_seq, auto_cycles, sent_hash, sent_at, answer_hash, answer_at,
			delivered_hash, delivered_at, updated_at
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(sid) DO UPDATE SET
			site=excluded.site, chat_key=excluded.chat_key, state=excluded.state,
			hold_reason=excluded.hold_reason, kind=excluded.kind,
			source=excluded.source, cycle_seq=excluded.cycle_seq,
			auto_cycles=excluded.auto_cycles, sent_hash=excluded.sent_hash,
			sent_at=excluded.sent_at, answer_hash=excluded.answer_hash,
			answer_at=excluded.answer_at, delivered_hash=excluded.delivered_hash,
			delivered_at=excluded.delivered_at, updated_at=excluded.updated_at`,
			r.SID, r.Site, r.ChatKey, r.State, r.HoldReason, r.Kind, r.Source,
			r.CycleSeq, r.AutoCycles, r.SentHash, ts(r.SentAt),
			r.AnswerHash, ts(r.AnswerAt), r.DeliveredHash, ts(r.DeliveredAt),
			ts(r.UpdatedAt))
		return err
	})
}

// SetRelayLinkState — переход состояния автомата (сначала БД, потом побочное).
func (s *Store) SetRelayLinkState(sid string, st model.RelayState, holdReason string, now time.Time) error {
	return s.DoWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE relay_link
			SET state = ?, hold_reason = ?, updated_at = ? WHERE sid = ?`,
			st, holdReason, ts(now), sid)
		if err != nil {
			return err
		}
		return requireRows(res)
	})
}

// SetRelayLinkCycle — счётчики цикла (cycle_seq, auto_cycles).
func (s *Store) SetRelayLinkCycle(sid string, cycleSeq, autoCycles int) error {
	return s.DoWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE relay_link
			SET cycle_seq = ?, auto_cycles = ?, updated_at = ? WHERE sid = ?`,
			cycleSeq, autoCycles, ts(time.Now()), sid)
		if err != nil {
			return err
		}
		return requireRows(res)
	})
}

// SetRelayLinkSent — хеш и время последней отправки.
func (s *Store) SetRelayLinkSent(sid, hash string, at time.Time) error {
	return s.DoWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE relay_link
			SET sent_hash = ?, sent_at = ?, updated_at = ? WHERE sid = ?`,
			hash, ts(at), ts(at), sid)
		if err != nil {
			return err
		}
		return requireRows(res)
	})
}

// SetRelayLinkAnswer — хеш и время последнего блока для агента.
func (s *Store) SetRelayLinkAnswer(sid, hash string, at time.Time) error {
	return s.DoWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE relay_link
			SET answer_hash = ?, answer_at = ?, updated_at = ? WHERE sid = ?`,
			hash, ts(at), ts(at), sid)
		if err != nil {
			return err
		}
		return requireRows(res)
	})
}

// SetRelayLinkDelivered — хеш и время последней доставки агенту.
func (s *Store) SetRelayLinkDelivered(sid, hash string, at time.Time) error {
	return s.DoWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE relay_link
			SET delivered_hash = ?, delivered_at = ?, updated_at = ? WHERE sid = ?`,
			hash, ts(at), ts(at), sid)
		if err != nil {
			return err
		}
		return requireRows(res)
	})
}

// DeleteRelayLink — снять связь (session GONE или оператор выключил).
func (s *Store) DeleteRelayLink(sid string) error {
	return s.DoWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(`DELETE FROM relay_link WHERE sid = ?`, sid)
		return err
	})
}

// ListRelayLinks — все активные связи (state != OFF).
func (s *Store) ListRelayLinks() ([]RelayLink, error) {
	rows, err := s.DB().Query("SELECT " + relayLinkCols + ` FROM relay_link WHERE state <> 'OFF' ORDER BY sid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RelayLink
	for rows.Next() {
		r, err := scanRelayLink(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RelayCycle — строка relay_cycle: история цикла (ТЗ Relay §4).
type RelayCycle struct {
	ID          int64
	SID         string
	Kind        string
	Source      string
	StartedAt   time.Time
	SentAt      time.Time
	AnswerAt    time.Time
	DeliveredAt time.Time
	Outcome     string
	HoldReason  string
	ReportHash  string
	ReportBytes int
	AnswerHash  string
	AnswerBytes int
	Attachments string
	LLMUsed     bool
	Proof       string // recording | terminal
}

// InsertRelayCycle — новая строка истории цикла.
func (s *Store) InsertRelayCycle(c RelayCycle) (int64, error) {
	var id int64
	err := s.DoWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`INSERT INTO relay_cycle (
			sid, kind, source, started_at, sent_at, answer_at, delivered_at,
			outcome, hold_reason, report_hash, report_bytes, answer_hash,
			answer_bytes, attachments, llm_used, proof
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			c.SID, c.Kind, c.Source, ts(c.StartedAt), ts(c.SentAt),
			ts(c.AnswerAt), ts(c.DeliveredAt), c.Outcome, c.HoldReason,
			c.ReportHash, c.ReportBytes, c.AnswerHash, c.AnswerBytes,
			c.Attachments, boolInt(c.LLMUsed), c.Proof)
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		return err
	})
	return id, err
}

// SetRelayCycleOutcome — исход цикла (в конце).
func (s *Store) SetRelayCycleOutcome(id int64, outcome, holdReason string, deliveredAt time.Time) error {
	return s.DoWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE relay_cycle
			SET outcome = ?, hold_reason = ?, delivered_at = ? WHERE id = ?`,
			outcome, holdReason, ts(deliveredAt), id)
		if err != nil {
			return err
		}
		return requireRows(res)
	})
}

// ListRelayCycles — история циклов сессии (по убыванию id).
func (s *Store) ListRelayCycles(sid string, limit int) ([]RelayCycle, error) {
	rows, err := s.DB().Query(`SELECT id, sid, kind, source, started_at, sent_at,
		answer_at, delivered_at, outcome, hold_reason, report_hash, report_bytes,
		answer_hash, answer_bytes, attachments, llm_used, proof
		FROM relay_cycle WHERE sid = ? ORDER BY id DESC LIMIT ?`, sid, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RelayCycle
	for rows.Next() {
		var c RelayCycle
		var started, sentAt, answerAt, deliveredAt string
		var llm int
		if err := rows.Scan(&c.ID, &c.SID, &c.Kind, &c.Source, &started, &sentAt,
			&answerAt, &deliveredAt, &c.Outcome, &c.HoldReason, &c.ReportHash,
			&c.ReportBytes, &c.AnswerHash, &c.AnswerBytes, &c.Attachments,
			&llm, &c.Proof); err != nil {
			return nil, err
		}
		c.StartedAt, _ = time.Parse(time.RFC3339Nano, started)
		c.SentAt, _ = time.Parse(time.RFC3339Nano, sentAt)
		c.AnswerAt, _ = time.Parse(time.RFC3339Nano, answerAt)
		c.DeliveredAt, _ = time.Parse(time.RFC3339Nano, deliveredAt)
		c.LLMUsed = llm == 1
		out = append(out, c)
	}
	return out, rows.Err()
}
