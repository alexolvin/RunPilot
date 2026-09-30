-- 010: имя сессии в строке хода. «Ходы» журнала показывают читаемое имя,
-- а не служебный sid; имя снимается при старте хода — после удаления
-- сессии из turn/session его уже не восстановить.
ALTER TABLE turn ADD COLUMN session_name TEXT NOT NULL DEFAULT '';
