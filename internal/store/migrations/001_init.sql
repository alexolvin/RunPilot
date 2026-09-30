-- 001_init: базовая схема runpilot (раздел 4 ТЗ).
-- Поля токенов отсутствуют (строгое правило 3). Все времена — RFC3339 UTC.

CREATE TABLE IF NOT EXISTS session (
	sid                TEXT PRIMARY KEY,
	name               TEXT NOT NULL,
	host               TEXT NOT NULL,
	host_ip            TEXT NOT NULL,
	tmux_session       TEXT NOT NULL,
	window             INTEGER NOT NULL,
	pane_id            TEXT NOT NULL,
	profile            TEXT NOT NULL,
	agent_version      TEXT NOT NULL DEFAULT '',
	state              TEXT NOT NULL,
	state_changed_at   TEXT NOT NULL,
	hold_reason        TEXT NOT NULL DEFAULT '',
	class              INTEGER NOT NULL DEFAULT 2,
	constraint_kind    TEXT NOT NULL DEFAULT 'none',
	constraint_server  TEXT NOT NULL DEFAULT '',
	auto_enqueue       INTEGER NOT NULL DEFAULT 0,
	last_server        TEXT NOT NULL DEFAULT '',
	last_turn_end      TEXT NOT NULL DEFAULT '',
	last_migrated_from TEXT NOT NULL DEFAULT '',
	attempts           INTEGER NOT NULL DEFAULT 0,
	created_at         TEXT NOT NULL
);

-- Имя уникально среди не-GONE (GONE регулируется retention и проверкой при runpilot run).
CREATE UNIQUE INDEX IF NOT EXISTS idx_session_name_live
	ON session (name) WHERE state <> 'GONE';

CREATE TABLE IF NOT EXISTS queue_entry (
	sid               TEXT PRIMARY KEY REFERENCES session (sid) ON DELETE CASCADE,
	class             INTEGER NOT NULL,
	enqueued_at       TEXT NOT NULL,
	not_before        TEXT NOT NULL,
	constraint_kind   TEXT NOT NULL DEFAULT 'none',
	constraint_server TEXT NOT NULL DEFAULT '',
	after_sid         TEXT NOT NULL DEFAULT '',
	mode              TEXT NOT NULL DEFAULT 'submit',
	held_request      INTEGER NOT NULL DEFAULT 0,
	ineligible_reason TEXT NOT NULL DEFAULT '',
	ineligible_since  TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS lease (
	id             INTEGER PRIMARY KEY AUTOINCREMENT,
	sid            TEXT NOT NULL,
	server         TEXT NOT NULL,
	slot           INTEGER NOT NULL,
	state          TEXT NOT NULL,
	origin         TEXT NOT NULL,
	granted_at     TEXT NOT NULL,
	released_at    TEXT NOT NULL DEFAULT '',
	release_reason TEXT NOT NULL DEFAULT '',
	requests       INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_lease_sid ON lease (sid, granted_at);
CREATE INDEX IF NOT EXISTS idx_lease_server_state ON lease (server, state);

CREATE TABLE IF NOT EXISTS turn (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	sid        TEXT NOT NULL,
	started_at TEXT NOT NULL,
	ended_at   TEXT NOT NULL DEFAULT '',
	servers    TEXT NOT NULL DEFAULT '',
	outcome    TEXT NOT NULL DEFAULT '',
	requests   INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_turn_sid ON turn (sid, started_at);

CREATE TABLE IF NOT EXISTS request (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	sid          TEXT NOT NULL,
	turn_id      INTEGER NOT NULL,
	server       TEXT NOT NULL,
	path         TEXT NOT NULL,
	status       INTEGER NOT NULL,
	t_start      TEXT NOT NULL,
	t_first_byte TEXT NOT NULL DEFAULT '',
	t_end        TEXT NOT NULL DEFAULT '',
	held_ms      INTEGER NOT NULL DEFAULT 0,
	error        TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_request_sid ON request (sid, t_start);

CREATE TABLE IF NOT EXISTS event (
	id      INTEGER PRIMARY KEY AUTOINCREMENT,
	ts      TEXT NOT NULL,
	kind    TEXT NOT NULL,
	sid     TEXT NOT NULL DEFAULT '',
	server  TEXT NOT NULL DEFAULT '',
	payload TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_event_ts ON event (ts);
