package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"runpilot/internal/model"
)

// ErrNotFound — сущность отсутствует.
var ErrNotFound = errors.New("store: not found")

// SessionRecord — строка session из БД (модель раздела 4 ТЗ, без токенов).
type SessionRecord struct {
	SID              string
	Name             string
	Host             string
	HostIP           string
	TmuxSession      string
	Window           int
	PaneID           string
	Profile          string
	AgentVersion     string
	Kind             string // PANE (дефолт) | JOB (раздел 8.1)
	State            model.SessionState
	StateChangedAt   time.Time
	HoldReason       model.HoldReason
	Class            model.QueueClass
	ConstraintKind   model.ConstraintKind
	ConstraintServer string
	AutoEnqueue      bool
	LastServer       string
	LastTurnEnd      time.Time
	LastMigratedFrom string
	Attempts         int
	CreatedAt        time.Time
}

const sessionCols = `sid, name, host, host_ip, tmux_session, window, pane_id,
profile, agent_version, kind, state, state_changed_at, hold_reason, class,
constraint_kind, constraint_server, auto_enqueue, last_server,
last_turn_end, last_migrated_from, attempts, created_at`

func scanSession(row interface{ Scan(...any) error }) (SessionRecord, error) {
	var r SessionRecord
	var state, hold, kind, ckind, changed, turnEnd, created string
	var auto int
	var class int
	err := row.Scan(&r.SID, &r.Name, &r.Host, &r.HostIP, &r.TmuxSession, &r.Window,
		&r.PaneID, &r.Profile, &r.AgentVersion, &kind, &state, &changed, &hold,
		&class, &ckind, &r.ConstraintServer, &auto, &r.LastServer,
		&turnEnd, &r.LastMigratedFrom, &r.Attempts, &created)
	if err == nil {
		r.Class = model.QueueClass(class)
	}
	if err != nil {
		return r, err
	}
	r.Kind = kind
	r.State = model.SessionState(state)
	r.HoldReason = model.HoldReason(hold)
	r.ConstraintKind = model.ConstraintKind(ckind)
	r.AutoEnqueue = auto == 1
	r.StateChangedAt, _ = time.Parse(time.RFC3339Nano, changed)
	r.LastTurnEnd, _ = time.Parse(time.RFC3339Nano, turnEnd)
	r.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	return r, nil
}

func ts(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// CreateSession — новая сессия (runpilot run). Уникальность имени среди
// не-GONE enforced индексом idx_session_name_live.
func (s *Store) CreateSession(r SessionRecord) error {
	r.StateChangedAt = r.StateChangedAt.UTC()
	if r.Kind == "" {
		r.Kind = model.KindPane
	}
	return s.DoWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO session (
			sid, name, host, host_ip, tmux_session, window, pane_id, profile,
			agent_version, kind, state, state_changed_at, hold_reason, class,
			constraint_kind, constraint_server, auto_enqueue, last_server,
			last_turn_end, last_migrated_from, attempts, created_at
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			r.SID, r.Name, r.Host, r.HostIP, r.TmuxSession, r.Window, r.PaneID,
			r.Profile, r.AgentVersion, r.Kind, r.State, ts(r.StateChangedAt),
			r.HoldReason, int(r.Class), r.ConstraintKind, r.ConstraintServer,
			boolInt(r.AutoEnqueue), r.LastServer, ts(r.LastTurnEnd),
			r.LastMigratedFrom, r.Attempts, ts(r.CreatedAt))
		return err
	})
}

// GetSession — по sid.
func (s *Store) GetSession(sid string) (SessionRecord, error) {
	return s.queryOne("SELECT "+sessionCols+` FROM session WHERE sid = ?`, sid)
}

// SessionNames — sid → имя сессии (журнал: человекочитаемое название в строках
// ходов). Неизвестные sid в результат не попадают.
func (s *Store) SessionNames(sids []string) (map[string]string, error) {
	out := map[string]string{}
	if len(sids) == 0 {
		return out, nil
	}
	placeholders := make([]string, len(sids))
	args := make([]any, len(sids))
	for i, sid := range sids {
		placeholders[i] = "?"
		args[i] = sid
	}
	rows, err := s.db.Query(`SELECT sid, name FROM session WHERE sid IN (`+
		strings.Join(placeholders, ",")+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var sid, name string
		if err := rows.Scan(&sid, &name); err != nil {
			return nil, err
		}
		out[sid] = name
	}
	return out, rows.Err()
}

// FindSessionByName — по имени, любое состояние.
func (s *Store) FindSessionByName(name string) (SessionRecord, error) {
	return s.queryOne("SELECT "+sessionCols+` FROM session WHERE name = ?`, name)
}

// FindLiveByName — по имени среди не-GONE.
func (s *Store) FindLiveByName(name string) (SessionRecord, error) {
	return s.queryOne("SELECT "+sessionCols+` FROM session WHERE name = ? AND state <> 'GONE'`, name)
}

// UpdateSessionPane — панель после tmux new-session (pane_id, tmux_session,
// agent_version).
func (s *Store) UpdateSessionPane(sid, tmuxSession, paneID, agentVersion string, window int) error {
	return s.DoWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE session
			SET tmux_session = ?, window = ?, pane_id = ?,
			    agent_version = CASE WHEN ? = '' THEN agent_version ELSE ? END
			WHERE sid = ?`, tmuxSession, window, paneID, agentVersion, agentVersion, sid)
		if err != nil {
			return err
		}
		return requireRows(res)
	})
}

// UpdateSessionAgentVersion — версия кодера (C6): сменяется при перезапуске
// кодера (узел сообщает version_cmd). Обновляет только agent_version.
func (s *Store) UpdateSessionAgentVersion(sid, version string) error {
	return s.DoWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE session SET agent_version = ? WHERE sid = ?`, version, sid)
		if err != nil {
			return err
		}
		return requireRows(res)
	})
}

// SetSessionMigratedFrom — последняя миграция (раздел 6 ТЗ).
func (s *Store) SetSessionMigratedFrom(sid, server string) error {
	return s.DoWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE session SET last_migrated_from = ? WHERE sid = ?`,
			server, sid)
		if err != nil {
			return err
		}
		return requireRows(res)
	})
}

// SetSessionClass — класс очереди сессии (runpilot prio).
func (s *Store) SetSessionClass(sid string, c model.QueueClass) error {
	return s.DoWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE session SET class = ? WHERE sid = ?`,
			int(c), sid)
		if err != nil {
			return err
		}
		return requireRows(res)
	})
}

// SetSessionConstraint — ограничение pin/prefer (runpilot pin/prefer/unpin).
func (s *Store) SetSessionConstraint(sid string, kind model.ConstraintKind, server string) error {
	return s.DoWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE session
			SET constraint_kind = ?, constraint_server = ? WHERE sid = ?`,
			kind, server, sid)
		if err != nil {
			return err
		}
		return requireRows(res)
	})
}

// SetSessionAttempts — счётчик транзиентных ошибок (раздел 9 ТЗ).
func (s *Store) SetSessionAttempts(sid string, attempts int) error {
	return s.DoWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE session SET attempts = ? WHERE sid = ?`,
			attempts, sid)
		if err != nil {
			return err
		}
		return requireRows(res)
	})
}

// SetSessionAutoEnqueue — автопостановка on/off (runpilot set auto-enqueue).
func (s *Store) SetSessionAutoEnqueue(sid string, on bool) error {
	b := 0
	if on {
		b = 1
	}
	return s.DoWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE session SET auto_enqueue = ? WHERE sid = ?`,
			b, sid)
		if err != nil {
			return err
		}
		return requireRows(res)
	})
}

// SetSessionLastTurnEnd — конец последнего хода (раздел 8 ТЗ).
func (s *Store) SetSessionLastTurnEnd(sid string, at time.Time) error {
	return s.DoWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE session SET last_turn_end = ? WHERE sid = ?`,
			ts(at), sid)
		if err != nil {
			return err
		}
		return requireRows(res)
	})
}

// SetSessionLastMigratedFromEmpty — сброс после завершения хода
// (раздел 5 ТЗ: «s ≠ last_migrated_from текущего хода»).
func (s *Store) SetSessionLastMigratedFromEmpty(sid string) error {
	return s.DoWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE session SET last_migrated_from = '' WHERE sid = ?`, sid)
		if err != nil {
			return err
		}
		return requireRows(res)
	})
}

// SetSessionLastServer — сервер последнего запроса (раздел 6 ТЗ).
func (s *Store) SetSessionLastServer(sid, server string) error {
	return s.DoWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE session SET last_server = ? WHERE sid = ?`,
			server, sid)
		if err != nil {
			return err
		}
		return requireRows(res)
	})
}

// SetSessionState — переход состояния (сначала БД, потом побочный эффект —
// раздел 14 ТЗ).
func (s *Store) SetSessionState(sid string, st model.SessionState, reason model.HoldReason, now time.Time) error {
	return s.DoWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE session
			SET state = ?, state_changed_at = ?,
			    hold_reason = CASE WHEN ? = '' THEN '' ELSE ? END
			WHERE sid = ?`, st, ts(now), reason, reason, sid)
		if err != nil {
			return err
		}
		return requireRows(res)
	})
}

// DeleteSession — удаление GONE-сессии и её строк (очередь, аренды, ходы,
// запросы). Вызывается только для GONE (проверяет вызывающий).
func (s *Store) DeleteSession(sidv string) error {
	return s.DoWrite(func(tx *sql.Tx) error {
		for _, q := range []string{
			`DELETE FROM request WHERE sid = ?`,
			`DELETE FROM turn WHERE sid = ?`,
			`DELETE FROM lease WHERE sid = ?`,
			`DELETE FROM queue_entry WHERE sid = ?`,
			`DELETE FROM session WHERE sid = ?`,
		} {
			if _, err := tx.Exec(q, sidv); err != nil {
				return err
			}
		}
		return nil
	})
}

// ListSessions — все сессии (includeGone — добавить GONE).
func (s *Store) ListSessions(includeGone bool) ([]SessionRecord, error) {
	q := "SELECT " + sessionCols + ` FROM session`
	if !includeGone {
		q += ` WHERE state <> 'GONE'`
	}
	q += ` ORDER BY created_at`
	rows, err := s.DB().Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SessionRecord
	for rows.Next() {
		r, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) queryOne(q string, arg string) (SessionRecord, error) {
	row := s.DB().QueryRow(q, arg)
	r, err := scanSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return SessionRecord{}, ErrNotFound
	}
	if err != nil {
		return SessionRecord{}, err
	}
	return r, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func requireRows(res sql.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%w: 0 строк", ErrNotFound)
	}
	return nil
}
