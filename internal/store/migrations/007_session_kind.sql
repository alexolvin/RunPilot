-- 007_session_kind: вид сессии (v2 разделы 2.4/2.5).
-- PANE — процесс сессии вида PANE (кодер в tmux-панели, дефолт).
-- JOB  — задание cron (runpilot exec, раздел 8.1): без tmux-панели, без
-- проверок панели; число одновременных аренд ограничивает jobs.slots.
ALTER TABLE session ADD COLUMN kind TEXT NOT NULL DEFAULT 'PANE';
