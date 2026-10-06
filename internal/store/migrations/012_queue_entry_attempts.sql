-- 012: attempts в queue_entry (П.5: повтор не сбрасывает счётчик
-- автоперепостановок). При CONFLICT — MAX(старое, новое), см. QueueUpsert.
ALTER TABLE queue_entry ADD COLUMN attempts INTEGER NOT NULL DEFAULT 0;
