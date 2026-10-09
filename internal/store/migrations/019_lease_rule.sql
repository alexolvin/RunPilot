-- 019: правило выбора слота (W13 этап 2) в аренде.
-- pin|prefer|warm_cache|priority|lru — какое правило ТЗ §5 выдало аренду.
-- Пусто = аренда до введения правила (старые строки).
ALTER TABLE lease ADD COLUMN rule TEXT NOT NULL DEFAULT '';
