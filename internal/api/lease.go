package api

// Административное снятие аренды (v2): POST /api/v1/leases/release.
// Причина OPERATOR, событие LEASE_RELEASE.

import (
	"encoding/json"
	"net/http"

	"runpilot/internal/model"
)

// handleLeaseRelease — POST /api/v1/leases/release: снять аренду по sid.
func (s *Server) handleLeaseRelease(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SID string `json:"sid"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	if req.SID == "" {
		httpError(w, http.StatusBadRequest, "BAD_REQUEST", "sid обязателен")
		return
	}
	// Аренда должна быть ACTIVE или PENDING.
	l, err := s.store.LeaseGet(req.SID)
	if err != nil {
		httpError(w, http.StatusNotFound, "NOT_FOUND", "аренда не найдена")
		return
	}
	if l.State != model.LeaseActive && l.State != model.LeasePending {
		httpError(w, http.StatusConflict, "BAD_STATE",
			"аренда в состоянии "+string(l.State)+", только ACTIVE/PENDING")
		return
	}
	if err := s.store.LeaseRelease(req.SID, model.ReleaseOperator, s.clk.Now()); err != nil {
		httpError(w, http.StatusInternalServerError, "STORE", err.Error())
		return
	}
	// Событие LEASE_RELEASE.
	payload, _ := json.Marshal(map[string]any{
		"server": l.Server, "reason": model.ReleaseOperator,
	})
	_ = s.store.EventRecord(model.Event{
		TS: s.clk.Now(), Kind: model.KindLeaseRelease, SID: req.SID, Payload: payload,
	})
	s.log.Info("api: аренда снята оператором", "sid", req.SID, "server", l.Server)
	writeJSON(w, map[string]any{"released": true, "sid": req.SID, "server": l.Server})
}
