-- Операторские состояния (п.1): per-name (сервер/узел) drain/pause.
-- name + kind — первичный ключ; строка = «состояние стоит».
CREATE TABLE IF NOT EXISTS operator_state (
  name TEXT NOT NULL,
  kind TEXT NOT NULL,
  PRIMARY KEY (name, kind)
);
