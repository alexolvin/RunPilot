-- 004_notification: центр уведомлений (v2 раздел 3.4, 17).
-- Уведомление дедуплицируется по key (kind + сущность): одно активное на ключ,
-- повтор увеличивает count; авто-закрытие при исчезновении условия (resolved_at).

CREATE TABLE IF NOT EXISTS notification (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	key         TEXT NOT NULL,
	severity    TEXT NOT NULL,
	kind        TEXT NOT NULL,
	sid         TEXT NOT NULL DEFAULT '',
	server      TEXT NOT NULL DEFAULT '',
	host        TEXT NOT NULL DEFAULT '',
	title       TEXT NOT NULL DEFAULT '',
	count       INTEGER NOT NULL DEFAULT 1,
	first_ts    TEXT NOT NULL,
	last_ts     TEXT NOT NULL,
	read_at     TEXT NOT NULL DEFAULT '',
	resolved_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_notification_key ON notification (key);
CREATE INDEX IF NOT EXISTS idx_notification_resolved ON notification (resolved_at);
