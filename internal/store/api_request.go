package store

// Учёт запросов API-клиентов (api_request, раздел «Учёт и интерфейс»).

import (
	"database/sql"
	"time"
)

// ApiRequest — строка api_request.
type ApiRequest struct {
	ID               int64
	TS               time.Time
	Client           string
	Server           string
	Model            string
	PromptTokens     int
	CompletionTokens int
	WaitMS           int64
	LatencyMS        int64
	Result           string // OK, 401, 400, 502, 503
}

// ApiRequestRecord — записать запрос.
func (s *Store) ApiRequestRecord(r ApiRequest) error {
	r.TS = r.TS.UTC()
	return s.DoWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(`
INSERT INTO api_request
	(ts, client, server, model, prompt_tokens, completion_tokens,
	 wait_ms, latency_ms, result)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			ts(r.TS), r.Client, r.Server, r.Model, r.PromptTokens,
			r.CompletionTokens, r.WaitMS, r.LatencyMS, r.Result)
		return err
	})
}

// ApiRequestDaily — агрегация по клиенту за сутки: число запросов + токены.
type ApiRequestDaily struct {
	Client           string
	Requests         int
	PromptTokens     int
	CompletionTokens int
}

// ApiRequestsDaily — агрегация по клиентам за последние сутки.
func (s *Store) ApiRequestsDaily(now time.Time) (map[string]ApiRequestDaily, error) {
	since := now.Add(-hoursPerDay * time.Hour).UTC()
	rows, err := s.db.Query(`
SELECT client, COUNT(*), COALESCE(SUM(prompt_tokens),0), COALESCE(SUM(completion_tokens),0)
FROM api_request WHERE ts >= ? GROUP BY client`, ts(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]ApiRequestDaily{}
	for rows.Next() {
		var d ApiRequestDaily
		if err := rows.Scan(&d.Client, &d.Requests, &d.PromptTokens, &d.CompletionTokens); err != nil {
			return nil, err
		}
		out[d.Client] = d
	}
	return out, rows.Err()
}
