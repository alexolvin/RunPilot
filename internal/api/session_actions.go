// Операторские действия сессии (v2 раздел 15.3): REST-пути, вызывающие те же
// Cmd*, что и командная строка. Композер карточки использует /paste (многострочный
// текст сохраняется побайто, в отличие от разбора команды).
package api

import (
	"encoding/json"
	"net/http"
)

// handlePaste — POST /sessions/{t}/paste {text, enqueue} (13.1).
func (s *Server) handlePaste(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text    string `json:"text"`
		Enqueue bool   `json:"enqueue"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	sid := s.resolveSID(r.PathValue("t"))
	if sid == "" {
		httpError(w, http.StatusNotFound, "NOT_FOUND", "нет такой сессии")
		return
	}
	out, _ := s.CmdPaste(WithOperator(r.Context(), "web"), sid, body.Text, body.Enqueue)
	// O9: слишком длинный текст задания → 413 с фактическим размером.
	if out.Code == "PASTE_TOO_LONG" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":   map[string]any{"code": "PASTE_TOO_LONG", "message": out.Text},
			"details": out.Details,
		})
		return
	}
	writeJSON(w, out)
}

// handleApprove — POST /sessions/{t}/approve {option} (13.3).
func (s *Server) handleApprove(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Option string `json:"option"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	sid := s.resolveSID(r.PathValue("t"))
	if sid == "" {
		httpError(w, http.StatusNotFound, "NOT_FOUND", "нет такой сессии")
		return
	}
	out, _ := s.CmdApprove(WithOperator(r.Context(), "web"), sid, body.Option)
	writeJSON(w, out)
}

// handleCompress — POST /sessions/{t}/compress (13.1: сжатие контекста).
func (s *Server) handleCompress(w http.ResponseWriter, r *http.Request) {
	sid := s.resolveSID(r.PathValue("t"))
	if sid == "" {
		httpError(w, http.StatusNotFound, "NOT_FOUND", "нет такой сессии")
		return
	}
	out, _ := s.CmdCompress(WithOperator(r.Context(), "web"), sid)
	writeJSON(w, out)
}

// handleLaunchCommand — POST /sessions/{t}/launch-command {command, enqueue}
// (раздел постановки в очередь): буфер «команды для запуска» + опциональная
// постановка в очередь. Заряд воспроизводится узлом только при dispatch.
func (s *Server) handleLaunchCommand(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Command string `json:"command"`
		Enqueue bool   `json:"enqueue"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	sid := s.resolveSID(r.PathValue("t"))
	if sid == "" {
		httpError(w, http.StatusNotFound, "NOT_FOUND", "нет такой сессии")
		return
	}
	out, _ := s.CmdLaunchCommand(WithOperator(r.Context(), "web"), sid, body.Command, body.Enqueue)
	if out.Code == "LAUNCH_TOO_LONG" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":   map[string]any{"code": "LAUNCH_TOO_LONG", "message": out.Text},
			"details": out.Details,
		})
		return
	}
	writeJSON(w, out)
}

// handleStop — POST /sessions/{t}/stop (раздел постановки в очередь): отправка
// Esc (cancel-клавиш) работающей сессии. Реальную остановку клиент сверяет по
// активности панели, а не по факту отправки.
func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	if !s.schedOr503(w) {
		return
	}
	sid := s.resolveSID(r.PathValue("t"))
	if sid == "" {
		httpError(w, http.StatusNotFound, "NOT_FOUND", "нет такой сессии")
		return
	}
	if err := s.sched.Cancel(sid); err != nil {
		httpError(w, http.StatusBadGateway, "STOP", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
