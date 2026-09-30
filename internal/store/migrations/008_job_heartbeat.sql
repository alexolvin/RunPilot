-- 008_job_heartbeat: пульс заданий cron (runpilot exec, раздел 8.1 ТЗ, строка X5).
-- last_beat — время последнего пульса (RFC3339 UTC). Пульсы шлются клиентом
-- runpilot exec каждые jobs.heartbeat_sec после запуска qwen; просрочка
-- jobs.heartbeat_timeout_sec без пульса → аренда снимается (LOST), без
-- перепостановки (повтор — дело cron).
-- cancel — флаг «Прервать» из веба (SIGTERM процессу qwen, затем SIGKILL).
CREATE TABLE IF NOT EXISTS job_heartbeat (
    sid       TEXT PRIMARY KEY,
    last_beat TEXT NOT NULL,
    cancel    INTEGER NOT NULL DEFAULT 0
);
