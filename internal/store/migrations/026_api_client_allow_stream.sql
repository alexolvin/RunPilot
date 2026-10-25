-- 026_api_client_allow_stream: разрешение стриминга (stream:true) для
-- API-клиентов. Учёт токенов — из usage-чанка (шлюз добавляет
-- stream_options.include_usage=true к апстриму); usage-чанк с пустым
-- choices не передаётся клиенту, не запросившему include_usage.
ALTER TABLE api_client ADD COLUMN allow_stream INTEGER NOT NULL DEFAULT 0;
