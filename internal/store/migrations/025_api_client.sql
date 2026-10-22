-- 025_api_client: безсессионные API-клиенты шлюза (OpenAI-запросы в общую
-- очередь). api_client — клиент + ключ (SHA-256; сам ключ показывается один
-- раз при создании и нигде не хранится). api_request — учёт запросов.
--
-- Псевдо-сессия клиента (sid=api:<name>) живёт в обычной таблице session
-- (Kind='API') — она видна в очереди и в списке сессий.
CREATE TABLE api_client (
    name             TEXT PRIMARY KEY,
    key_hash         TEXT NOT NULL UNIQUE,
    class            INTEGER NOT NULL DEFAULT 4,
    priority         INTEGER NOT NULL DEFAULT 0,
    enabled          INTEGER NOT NULL DEFAULT 1,
    allowed_servers  TEXT NOT NULL DEFAULT '',
    max_hold_sec     INTEGER NOT NULL DEFAULT 100,
    sticky_idle_sec  INTEGER NOT NULL DEFAULT 20,
    aging_sec        INTEGER NOT NULL DEFAULT 600,
    created_at       TEXT NOT NULL,
    updated_at       TEXT NOT NULL
);

CREATE TABLE api_request (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    ts                 TEXT NOT NULL,
    client             TEXT NOT NULL,
    server             TEXT NOT NULL DEFAULT '',
    model              TEXT NOT NULL DEFAULT '',
    prompt_tokens      INTEGER NOT NULL DEFAULT 0,
    completion_tokens  INTEGER NOT NULL DEFAULT 0,
    wait_ms            INTEGER NOT NULL DEFAULT 0,
    latency_ms         INTEGER NOT NULL DEFAULT 0,
    result             TEXT NOT NULL
);
CREATE INDEX idx_api_request_client_ts ON api_request (client, ts);
CREATE INDEX idx_api_request_ts ON api_request (ts);
