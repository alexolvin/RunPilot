-- 013: held_since в queue_entry (п.6 — видимость удержания).
-- Время начала удержания (по internal/clock); пусто/0001 = удержание не
-- началось (held_request=false или запись не удерживает запрос).
ALTER TABLE queue_entry ADD COLUMN held_since TEXT NOT NULL DEFAULT '0001-01-01T00:00:00Z';
