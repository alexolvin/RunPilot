package api

import (
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"runpilot/internal/model"
)

// Журнал (v2 раздел 11.8): вкладки «События»/«Ходы»/«Аудит», серверная
// пагинация (web.page_size, id-cursor), экспорт CSV. Данные — только из
// хранилища (R3). «Аудит» — действия оператора (paste/term_open/term_close;
// R5: в payload только метаданные, без текстов).

// journalRow — строка журнала (событие или ход).
type journalRow struct {
	ID       int64           `json:"id"`
	TS       string          `json:"ts"`
	Kind     string          `json:"kind,omitempty"`   // событие
	SID      string          `json:"sid,omitempty"`
	Session  string          `json:"session,omitempty"` // ход: имя сессии
	Server   string          `json:"server,omitempty"` // событие (один сервер)
	Servers  []string        `json:"servers,omitempty"` // ход (>1 при миграции)
	Outcome  string          `json:"outcome,omitempty"`
	DurSec   int64           `json:"dur_sec,omitempty"`
	Requests int             `json:"requests,omitempty"`
	Payload  json.RawMessage `json:"payload,omitempty"` // событие: сырой JSON
}

// journalPage — ответ GET /api/v1/journal.
type journalPage struct {
	Kind      string       `json:"kind"`
	Rows      []journalRow `json:"rows"`
	Next      int64        `json:"next_cursor"` // 0 — нет следующей страницы
	PageSize  int          `json:"page_size"`
}

// handleJournal — GET /api/v1/journal?kind=&cursor=&limit=&format=.
func (s *Server) handleJournal(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	kind := q.Get("kind")
	if kind == "" {
		kind = "events"
	}
	cursor, _ := strconv.ParseInt(q.Get("cursor"), decimalBase, int64Bits)
	limit := s.cfg.Web.PageSize
	if limit <= 0 {
		limit = journalDefaultPageSize
	}
	if v := q.Get("limit"); v != "" {
		if x, err := strconv.Atoi(v); err == nil && x > 0 {
			limit = x
		}
	}

	switch kind {
	case "events":
		s.journalEvents(w, q, cursor, limit)
	case "turns":
		s.journalTurns(w, q, cursor, limit)
	case "audit":
		s.journalAudit(w, q, cursor, limit)
	default:
		httpError(w, http.StatusBadRequest, "BAD_KIND", "kind: events|turns|audit")
	}
}

func (s *Server) journalEvents(w http.ResponseWriter, q url.Values, cursor int64, limit int) {
	events, err := s.store.EventPage(cursor, limit)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "STORE", err.Error())
		return
	}
	if q.Get("format") == "csv" {
		writeJournalCSV(w, "events", events)
		return
	}
	s.journalWrite(w, "events", eventRows(events), limit)
}

// journalAudit — вкладка «Аудит»: действия оператора (paste/term_open/
// term_close) с тем же форматом строк, что и «События».
func (s *Server) journalAudit(w http.ResponseWriter, q url.Values, cursor int64, limit int) {
	events, err := s.store.AuditPage(cursor, limit)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "STORE", err.Error())
		return
	}
	if q.Get("format") == "csv" {
		writeJournalCSV(w, "audit", events)
		return
	}
	s.journalWrite(w, "audit", eventRows(events), limit)
}

// eventRows — события → строки журнала (payload — сырой JSON).
func eventRows(events []model.Event) []journalRow {
	rows := make([]journalRow, 0, len(events))
	for _, e := range events {
		row := journalRow{
			ID: e.ID, TS: e.TS.UTC().Format(time.RFC3339),
			Kind: e.Kind, SID: e.SID, Server: e.Server,
		}
		if len(e.Payload) > 0 {
			row.Payload = e.Payload
		}
		rows = append(rows, row)
	}
	return rows
}

func (s *Server) journalTurns(w http.ResponseWriter, q url.Values, cursor int64, limit int) {
	turns, err := s.store.TurnPage(cursor, limit)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "STORE", err.Error())
		return
	}
	if q.Get("format") == "csv" {
		writeTurnsCSV(w, turns)
		return
	}
	// Имя сессии (журнал: человекочитаемый объект, а не служебный код):
	// из строки хода (снимок при старте); для старых ходов — из session,
	// пока сессия на месте.
	needNames := map[string]bool{}
	for _, t := range turns {
		if t.SessionName == "" {
			needNames[t.SID] = true
		}
	}
	var names map[string]string
	if len(needNames) > 0 {
		sids := make([]string, 0, len(needNames))
		for sid := range needNames {
			sids = append(sids, sid)
		}
		var err error
		names, err = s.store.SessionNames(sids)
		if err != nil {
			httpError(w, http.StatusInternalServerError, "STORE", err.Error())
			return
		}
	}

	rows := make([]journalRow, 0, len(turns))
	for _, t := range turns {
		session := t.SessionName
		if session == "" {
			session = names[t.SID]
		}
		row := journalRow{
			ID: t.ID, TS: t.StartedAt.UTC().Format(time.RFC3339),
			SID: t.SID, Session: session,
			Servers: t.Servers, Outcome: string(t.Outcome),
			Requests: t.Requests,
		}
		if !t.StartedAt.IsZero() && !t.EndedAt.IsZero() {
			row.DurSec = int64(t.EndedAt.Sub(t.StartedAt).Seconds())
		}
		rows = append(rows, row)
	}
	s.journalWrite(w, "turns", rows, limit)
}

// journalWrite — страница: строки + next_cursor (id последней строки).
func (s *Server) journalWrite(w http.ResponseWriter, kind string, rows []journalRow, limit int) {
	next := int64(0)
	if len(rows) == limit && len(rows) > 0 {
		next = rows[len(rows)-1].ID
	}
	writeJSON(w, journalPage{Kind: kind, Rows: rows, Next: next, PageSize: limit})
}

// writeJournalCSV — события текущей страницы в CSV (kind — имя в файле).
func writeJournalCSV(w http.ResponseWriter, kind string, events []model.Event) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=journal-"+kind+".csv")
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"id", "ts", "kind", "sid", "server", "payload"})
	for _, e := range events {
		_ = cw.Write([]string{
			strconv.FormatInt(e.ID, decimalBase),
			e.TS.UTC().Format(time.RFC3339), e.Kind, e.SID, e.Server, string(e.Payload),
		})
	}
	cw.Flush()
}

// writeTurnsCSV — ходы текущей страницы в CSV.
func writeTurnsCSV(w http.ResponseWriter, turns []model.Turn) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=journal-turns.csv")
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"id", "started_at", "ended_at", "sid", "servers", "outcome", "requests"})
	for _, t := range turns {
		ended := ""
		if !t.EndedAt.IsZero() {
			ended = t.EndedAt.UTC().Format(time.RFC3339)
		}
		_ = cw.Write([]string{
			strconv.FormatInt(t.ID, decimalBase),
			t.StartedAt.UTC().Format(time.RFC3339), ended, t.SID,
			joinStrings(t.Servers), string(t.Outcome), strconv.Itoa(t.Requests),
		})
	}
	cw.Flush()
}

// joinStrings — CSV-строка для CSV-экспорта (разделитель ; чтобы не конфликтовать
// с запятой CSV-разделителя).
func joinStrings(list []string) string { return strings.Join(list, ";") }
