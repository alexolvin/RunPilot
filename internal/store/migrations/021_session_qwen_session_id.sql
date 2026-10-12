-- R0 (ТЗ Relay §3): qwen_session_id — UUIDv4, сгенерированный координатором
-- при создании сессии. Используется для --session-id / --resume / --chat-recording.
ALTER TABLE session ADD COLUMN qwen_session_id TEXT NOT NULL DEFAULT '';
