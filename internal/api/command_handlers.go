// Эндпоинты командной строки (раздел 12 v2): POST /api/v1/command (parse + Run)
// и GET /api/v1/command/complete (подсказки). Общий разбор веб/Telegram.
package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"runpilot/internal/command"
)

// handleCommand — POST /api/v1/command {line}. Ответ — Outcome {code,text,details};
// ошибка разбора — {"error":{code,message}}.
func (s *Server) handleCommand(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Line string `json:"line"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	ctx := WithOperator(r.Context(), "web")
	p, err := command.Parse(body.Line)
	if err != nil {
		var uk *command.UnknownCommand
		if errors.As(err, &uk) {
			httpError(w, http.StatusNotFound, "UNKNOWN_COMMAND", err.Error())
			return
		}
		httpError(w, http.StatusBadRequest, "BAD_COMMAND", err.Error())
		return
	}
	out, err := command.Run(ctx, p, s.newExecutor())
	if err != nil {
		httpError(w, http.StatusInternalServerError, "COMMAND", err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// handleCommandComplete — GET /api/v1/command/complete?line=… (бюджет
// ui.complete_budget_ms держит фронт; здесь — только подсказки).
func (s *Server) handleCommandComplete(w http.ResponseWriter, r *http.Request) {
	line := r.URL.Query().Get("line")
	ins := command.Complete(line, s.dataProviders())
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(ins)
}

// dataProviders — живые данные для подсказок (из /state: сессии, серверы, узлы).
func (s *Server) dataProviders() *stateDataProviders { return &stateDataProviders{s: s} }

type stateDataProviders struct{ s *Server }

func (d *stateDataProviders) Targets() []command.TargetValue {
	sessions, err := d.s.store.ListSessions(true)
	if err != nil {
		return nil
	}
	out := make([]command.TargetValue, 0, len(sessions))
	for _, rec := range sessions {
		out = append(out, command.TargetValue{
			SID: rec.SID, Name: rec.Name, State: string(rec.State),
		})
	}
	return out
}

func (d *stateDataProviders) Servers() []string {
	if d.s.sched == nil {
		return nil
	}
	rows := d.s.sched.ServersView()
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Name)
	}
	return out
}

func (d *stateDataProviders) Nodes() []string {
	nodes := d.s.hub.Nodes()
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n.Host)
	}
	return out
}

// Catalogs — каталоги узлов для /new (пока пусто: каталог — свободный ввод).
func (d *stateDataProviders) Catalogs() []string { return nil }
