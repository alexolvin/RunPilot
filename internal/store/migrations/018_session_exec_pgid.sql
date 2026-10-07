-- 018: PGID кодера (JOB-сессия, 8.1) в записи EXEC-сессии.
-- PID кодера = PGID (Setpgid=true): при смерти корня узел убивает группу
-- kill(-PGID, SIGKILL) — осиротевший qwen (внучатый процесс) не держит
-- соединение со шлюзом.
ALTER TABLE session ADD COLUMN exec_pgid INTEGER NOT NULL DEFAULT 0;
