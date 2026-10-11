-- W15 этап 1: размер тела запроса в строке request (только размер, байт).
ALTER TABLE request ADD COLUMN body_bytes INTEGER NOT NULL DEFAULT 0;
