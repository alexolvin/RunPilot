-- 003_setting: рабочие настройки, ревизии и серверы (v2 раздел 3.4, 4).
-- Все времена — RFC3339 UTC. Значения секретов в таблицу не попадают
-- (хранятся только имена переменных окружения, строгое правило 5).

-- Текущие рабочие настройки: перекрывающий слой поверх defaults.go.
CREATE TABLE IF NOT EXISTS setting (
	path       TEXT PRIMARY KEY,
	value      TEXT NOT NULL,
	version    INTEGER NOT NULL DEFAULT 1,
	updated_at TEXT NOT NULL,
	updated_by TEXT NOT NULL DEFAULT ''
);

-- Ревизии настроек: автор, источник, разница «было → стало» и полный
-- снимок рабочего состояния на момент ревизии (для точного отката).
CREATE TABLE IF NOT EXISTS setting_revision (
	id       INTEGER PRIMARY KEY AUTOINCREMENT,
	ts       TEXT NOT NULL,
	author   TEXT NOT NULL DEFAULT '',
	source   TEXT NOT NULL,
	diff     TEXT NOT NULL DEFAULT '',
	snapshot TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_setting_revision_ts ON setting_revision (ts);

-- Серверы (v2 раздел 3.4): переезжают из YAML. config.Server — в JSON,
-- имя — PK (неизменяемое, [a-z0-9-]{1,32}).
CREATE TABLE IF NOT EXISTS server (
	name       TEXT PRIMARY KEY,
	data       TEXT NOT NULL,
	version    INTEGER NOT NULL DEFAULT 1,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
