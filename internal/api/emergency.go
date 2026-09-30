package api

// Аварийная остановка (v2 раздел 6.2) и снятие (W7).

import (
	"net/http"

	"runpilot/internal/model"
)

// handleEmergency — POST /api/v1/emergency: аварийная остановка (6.2).
// Порядок в Scheduler.SetEmergency: meta.mode=EMERGENCY первым шагом,
// отмена идущих потоков (шлюз), снятие аренд (EMERGENCY), сессии →
// HOLD(EMERGENCY), событие MODE + [CRIT].
func (s *Server) handleEmergency(w http.ResponseWriter, r *http.Request) {
	if s.sched == nil {
		httpError(w, http.StatusServiceUnavailable, "NO_SCHEDULER", "планировщик не запущен")
		return
	}
	if err := s.sched.SetEmergency(); err != nil {
		httpError(w, http.StatusInternalServerError, "EMERGENCY", err.Error())
		return
	}
	writeJSON(w, map[string]any{"mode": string(model.ModeEmergency)})
}

// handleEmergencyRelease — POST /api/v1/emergency/release: снять аварийный
// режим (6.2 выход) → PAUSED. Сессии HOLD(EMERGENCY) возвращает
// /sessions/hold-emergency/requeue.
func (s *Server) handleEmergencyRelease(w http.ResponseWriter, r *http.Request) {
	if s.sched == nil {
		httpError(w, http.StatusServiceUnavailable, "NO_SCHEDULER", "планировщик не запущен")
		return
	}
	if err := s.sched.ClearEmergency(); err != nil {
		httpError(w, http.StatusInternalServerError, "EMERGENCY_RELEASE", err.Error())
		return
	}
	writeJSON(w, map[string]any{"mode": string(model.ModePaused)})
}

// handleRequeueEmergency — POST /api/v1/sessions/hold-emergency/requeue:
// вернуть все сессии HOLD(EMERGENCY) в очередь (резюм-записи).
func (s *Server) handleRequeueEmergency(w http.ResponseWriter, r *http.Request) {
	if s.sched == nil {
		httpError(w, http.StatusServiceUnavailable, "NO_SCHEDULER", "планировщик не запущен")
		return
	}
	n := s.sched.RequeueHoldEmergency()
	writeJSON(w, map[string]any{"requeued": n})
}
