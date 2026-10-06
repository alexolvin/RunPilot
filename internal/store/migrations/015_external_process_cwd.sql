-- 015: cwd в external_process (п.3 — «кодер вне очереди» с командой перезапуска).
-- cwd — readlink /proc/<pid>/cwd, который узел читает при скане внешних кодеров.
-- Веб-интерфейс по нему строит команду перезапуска под runpilot
-- (runpilot run --dir <cwd>); пусто — каталог неизвестен (процесс исчез/нет прав).
ALTER TABLE external_process ADD COLUMN cwd TEXT NOT NULL DEFAULT '';
