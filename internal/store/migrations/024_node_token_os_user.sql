-- 024_node_token_os_user: токен узла ключится по идентичности узла
-- (host, os_user), а не по host. Два узла на одном host (разные os_user)
-- имеют независимые токены: re-enroll одного больше не отзывает токен
-- другого (дефект 2026-10-21, инцидент vzu5-big: claim admin отозвал токен
-- botagent → power=None у vzu5).
--
-- SQLite не умеет менять PRIMARY KEY — пересобираем таблицу.
-- Legacy-строки (os_user='') остаются валидными: совпадение по токену с
-- os_user='' делается только по host (совместимость со старыми узлами).
CREATE TABLE node_token_new (
	host       TEXT NOT NULL,
	os_user    TEXT NOT NULL DEFAULT '',
	token      TEXT NOT NULL,
	created_at TEXT NOT NULL,
	revoked_at TEXT,
	PRIMARY KEY (host, os_user)
);
INSERT INTO node_token_new (host, os_user, token, created_at, revoked_at)
	SELECT host, '', token, created_at, revoked_at FROM node_token;
DROP TABLE node_token;
ALTER TABLE node_token_new RENAME TO node_token;
CREATE INDEX idx_node_token_revoked ON node_token (revoked_at);
