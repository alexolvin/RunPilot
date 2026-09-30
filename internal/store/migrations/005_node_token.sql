-- 005_node_token: токены узлов (раздел 14.2 v2).
-- Загрузка дистрибутива узел делает по Bearer-токену; отзыв при удалении
-- узла делает повторную загрузку 401 (CONTROL 4). Время — RFC3339 UTC.
CREATE TABLE IF NOT EXISTS node_token (
	host       TEXT PRIMARY KEY,
	token      TEXT NOT NULL,
	created_at TEXT NOT NULL,
	revoked_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_node_token_revoked ON node_token (revoked_at);
