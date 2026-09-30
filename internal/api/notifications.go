package api

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"runpilot/internal/model"
	"runpilot/internal/scheduler"
	"runpilot/internal/store"
	"runpilot/internal/texts"
)

// Центр уведомлений (v2 разделы 11.11, 17).
//
// W4-область: уведомления выводятся из ТЕКУЩЕГО состояния (состояния серверов,
// сессии в HOLD, панели в PROMPT) с дедупликацией по ключу и авто-закрытием при
// исчезновении условия. Полный каталог событий раздела 7 (S/N/C/X/K/O/U) и
// Telegram — W5/W7.

// serverNotifKind — состояние сервера → (kind, важность).
func serverNotifKind(state string) (kind, severity string) {
	switch state {
	case "DOWN":
		return "server_down", "CRIT"
	case "QUARANTINED":
		return "server_quarantine", "WARN"
	case "MODEL_PROBLEM":
		return "server_model", "WARN"
	case "KEY_MISSING":
		// v2 (S11): «Переменная … не задана» — CRIT.
		return "server_key", "CRIT"
	case "DRAINING":
		return "server_drain", "INFO"
	case "DISABLED":
		return "server_disabled", "INFO"
	default: // REMOVING
		return "server_removing", "INFO"
	}
}

// deriveNotifications — активные уведомления из текущего состояния.
func (s *Server) deriveNotifications(now time.Time) []store.Notification {
	var items []store.Notification

	var servers []scheduler.ServerRow
	if s.sched != nil {
		servers = s.sched.ServersView()
	}
	for _, sr := range servers {
		if sr.State == "UP" {
			continue
		}
		kind, sev := serverNotifKind(sr.State)
		items = append(items, store.Notification{
			Key: kind + ":" + sr.Name, Severity: sev, Kind: kind,
			Server: sr.Name,
			Title:  fmt.Sprintf("Сервер «%s»: %s", sr.Name, texts.ServerLabel(sr.State)),
			FirstTS: now, LastTS: now,
		})
	}

	if sessions, err := s.store.ListSessions(true); err == nil {
		for _, rec := range sessions {
			if rec.State != model.SessionHold {
				continue
			}
			title := "Сессия требует внимания"
			if rec.HoldReason != "" {
				title += ": " + texts.HoldLabel(rec.HoldReason)
			}
			items = append(items, store.Notification{
				Key: "session_hold:" + rec.SID, Severity: "WARN", Kind: "session_hold",
				SID: rec.SID, Host: rec.Host, Title: title,
				FirstTS: now, LastTS: now,
			})
		}
	}

	for _, p := range s.hub.State(nil).Panes {
		if p.Pane.State != string(model.PanePrompt) || p.Pane.SID == "" {
			continue
		}
		items = append(items, store.Notification{
			Key: "prompt:" + p.Pane.SID, Severity: "WARN", Kind: "prompt",
			SID: p.Pane.SID, Host: p.Host, Title: "Сессия ждёт разрешения",
			FirstTS: now, LastTS: now,
		})
	}
	return items
}

// handleNotifications — GET /api/v1/notifications?all= (раздел 11.11).
// Синхронизирует центр с текущим состоянием (upsert + авто-закрытие) и отдаёт
// активные (all=false) или все (all=true).
func (s *Server) handleNotifications(w http.ResponseWriter, r *http.Request) {
	now := s.clk.Now()
	items := s.deriveNotifications(now)
	keys := make([]string, 0, len(items))
	for _, it := range items {
		keys = append(keys, it.Key)
		if err := s.store.UpsertNotification(it); err != nil {
			httpError(w, http.StatusInternalServerError, "STORE", err.Error())
			return
		}
	}
	if err := s.store.ResolveInactiveNotifications(keys, now); err != nil {
		httpError(w, http.StatusInternalServerError, "STORE", err.Error())
		return
	}
	all := r.URL.Query().Get("all") == "true"
	list, err := s.store.ListNotifications(all)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "STORE", err.Error())
		return
	}
	writeJSON(w, list)
}

// handleNotificationRead — POST /api/v1/notifications/{id}/read.
func (s *Server) handleNotificationRead(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), decimalBase, int64Bits)
	if err != nil {
		httpError(w, http.StatusBadRequest, "BAD_ID", "id должен быть числом")
		return
	}
	if err := s.store.MarkNotificationRead(id, s.clk.Now()); err != nil {
		httpError(w, http.StatusNotFound, "NOT_FOUND", "нет уведомления")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleNotificationsReadAll — POST /api/v1/notifications/read-all.
func (s *Server) handleNotificationsReadAll(w http.ResponseWriter, r *http.Request) {
	if err := s.store.MarkAllNotificationsRead(s.clk.Now()); err != nil {
		httpError(w, http.StatusInternalServerError, "STORE", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
