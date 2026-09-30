-- 002_web_session: сессии браузера (v2 раздел 3.4, 16).
-- В БД — SHA-256 идентификатора (256 бит), не сам токен; все времена RFC3339 UTC.
CREATE TABLE IF NOT EXISTS web_session (
	id_hash    TEXT PRIMARY KEY,
	csrf_token TEXT NOT NULL,
	created_at TEXT NOT NULL,
	last_seen  TEXT NOT NULL,
	user_agent TEXT NOT NULL DEFAULT '',
	ts_login   TEXT NOT NULL,
	revoked_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_web_session_last_seen ON web_session (last_seen);
