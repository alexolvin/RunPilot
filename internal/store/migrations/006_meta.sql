-- 006_meta: метаданные координатора (v2 разделы 6.2/6.5).
-- meta.mode — режим (NORMAL/PAUSED/EMERGENCY/SAFE_MODE): при EMERGENCY
-- записывается ПЕРВЫМ шагом (6.2); читается при старте.
-- meta.last_alive_at — «сердцебиение» (6.5): координатор пишет его каждые
-- coordinator.heartbeat_sec; при старте интервал [last_alive_at, now] даёт
-- простой после аварийного завершения. Время — RFC3339 UTC.
CREATE TABLE IF NOT EXISTS meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
