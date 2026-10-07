-- 016: PID процесса runpilot exec (JOB-сессия, 8.1) в записи EXEC-сессии.
-- Авторитетная копия для координатора (отчёт/управление): узлу доступен.
-- Правило «кодер вне очереди» не читает это поле — EXEC-корни выводятся
-- локально (ps на узле, обход ВВЕРХ до runpilot exec + RUNPILOT_SID).
ALTER TABLE session ADD COLUMN exec_pid INTEGER NOT NULL DEFAULT 0;
