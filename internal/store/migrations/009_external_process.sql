-- 009_external_process: внешние кодеры вне tmux/runpilot (раздел 8.2 ТЗ).
-- external_process — живой внешний кодер: host+pid+start_time — устойчивый
-- ключ процесса. source — цепочка родителей (cron/systemd/sshd/tmux/other);
-- target — вывод координатора (gateway:<sid> / server:<name> / unknown) по
-- OPENAI_BASE_URL. Значения аргументов не хранятся (R5): только exe и имена
-- флагов (flags, пробельный список).
CREATE TABLE IF NOT EXISTS external_process (
    host       TEXT NOT NULL,
    pid        INTEGER NOT NULL,
    uid        INTEGER NOT NULL,
    start_time INTEGER NOT NULL,
    source     TEXT NOT NULL,
    exe        TEXT NOT NULL,
    flags      TEXT NOT NULL DEFAULT '',
    target     TEXT NOT NULL,
    status     TEXT NOT NULL DEFAULT 'ACTIVE',
    first_seen TEXT NOT NULL,
    last_seen  TEXT NOT NULL,
    PRIMARY KEY (host, pid, start_time)
);

-- ignore_rule — «Игнорировать»: при следующем скане кандидат под правило не
-- отчитывается (status IGNORED, без уведомления). Пустое поле — любое
-- значение; flags — список флагов, которые должны входить в кандидата.
CREATE TABLE IF NOT EXISTS ignore_rule (
    id      INTEGER PRIMARY KEY AUTOINCREMENT,
    host    TEXT NOT NULL DEFAULT '',
    exe     TEXT NOT NULL,
    flags   TEXT NOT NULL DEFAULT '',
    created TEXT NOT NULL
);
