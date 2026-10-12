-- R2 (ТЗ Relay §4): модель данных связи. Тексты в БД не пишутся — только
-- SHA-256, размеры и время.

-- session: связь с проектом (URL чата оркестратора, включена ли связь).
ALTER TABLE session ADD COLUMN orch_url TEXT NOT NULL DEFAULT '';
ALTER TABLE session ADD COLUMN relay_enabled INTEGER NOT NULL DEFAULT 0;

-- relay_link: автомат цикла связи (раздел 5). sid — PK (не больше одной связи
-- на сессию).
CREATE TABLE relay_link (
    sid            TEXT PRIMARY KEY,
    site           TEXT NOT NULL,
    chat_key       TEXT NOT NULL,
    state          TEXT NOT NULL,
    hold_reason    TEXT NOT NULL DEFAULT '',
    kind           TEXT NOT NULL DEFAULT '',
    source         TEXT NOT NULL DEFAULT '',
    cycle_seq      INTEGER NOT NULL DEFAULT 0,
    auto_cycles    INTEGER NOT NULL DEFAULT 0,
    sent_hash      TEXT NOT NULL DEFAULT '',
    sent_at        TEXT NOT NULL DEFAULT '',
    answer_hash    TEXT NOT NULL DEFAULT '',
    answer_at      TEXT NOT NULL DEFAULT '',
    delivered_hash TEXT NOT NULL DEFAULT '',
    delivered_at   TEXT NOT NULL DEFAULT '',
    updated_at     TEXT NOT NULL
);
-- один чат принадлежит не больше чем одной связи.
CREATE UNIQUE INDEX idx_relay_link_chat ON relay_link(site, chat_key);

-- relay_cycle: история циклов (раздел 4).
CREATE TABLE relay_cycle (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    sid           TEXT NOT NULL,
    kind          TEXT NOT NULL,
    source        TEXT NOT NULL,
    started_at    TEXT NOT NULL,
    sent_at       TEXT NOT NULL DEFAULT '',
    answer_at     TEXT NOT NULL DEFAULT '',
    delivered_at  TEXT NOT NULL DEFAULT '',
    outcome       TEXT NOT NULL DEFAULT '',
    hold_reason   TEXT NOT NULL DEFAULT '',
    report_hash   TEXT NOT NULL DEFAULT '',
    report_bytes  INTEGER NOT NULL DEFAULT 0,
    answer_hash   TEXT NOT NULL DEFAULT '',
    answer_bytes  INTEGER NOT NULL DEFAULT 0,
    attachments   TEXT NOT NULL DEFAULT '',
    llm_used      INTEGER NOT NULL DEFAULT 0,
    proof         TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_relay_cycle_sid ON relay_cycle(sid);
