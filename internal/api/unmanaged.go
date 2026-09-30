package api

// «Вне runpilot» (8.3 X1) и жизненный цикл GONE-сессий (5.4): приём уже
// открытых tmux-панелей с кодером в управление, удаление GONE-сессий
// из БД и периодическая очистка хранилища по retention.* (раздел 14).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"time"

	"runpilot/internal/model"
	"runpilot/internal/proto"
	"runpilot/internal/sid"
	"runpilot/internal/store"
)

// handleAdopt — POST /api/v1/unmanaged/{host}/{pane_id}/adopt:
// «Перезапустить под runpilot» (8.3 X1). Узел: quit_text + submit (операторский
// ввод), ожидание выхода кодера, respawn-pane -k с окружением профиля и
// @runpilot_sid. Контекст разговора кодера теряется (вебо предупреждает).
func (s *Server) handleAdopt(w http.ResponseWriter, r *http.Request) {
	host := r.PathValue("host")
	paneID := r.PathValue("pane_id")
	if _, ok := s.hub.Node(host); !ok {
		httpError(w, http.StatusServiceUnavailable, "NODE", "узел не подключён")
		return
	}
	var pane UnmanagedView
	found := false
	for _, u := range s.hub.Unmanaged() {
		if u.Host == host && u.PaneID == paneID {
			pane, found = u, true
			break
		}
	}
	if !found {
		httpError(w, http.StatusNotFound, "NOT_FOUND", "панель не в списке «вне runpilot»")
		return
	}
	name := pane.Session
	if name == "" {
		name = filepath.Base(pane.Dir)
	}
	if name == "" || name == "." {
		name = "adopted"
	}
	if live, err := s.store.FindLiveByName(name); err == nil {
		httpError(w, http.StatusConflict, "NAME_IN_USE",
			fmt.Sprintf("имя занято сессией %s (%s)", live.SID, live.State))
		return
	}
	newSID, err := sid.New()
	if err != nil {
		httpError(w, http.StatusInternalServerError, "SID", err.Error())
		return
	}
	env, err := s.spawnEnv(newSID)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "ENV", err.Error())
		return
	}
	m := proto.New(proto.KindAdopt)
	m.PaneID, m.SID, m.Env = paneID, newSID, env
	rep, err := s.dispatchToNodeTimeout(r.Context(), host, m, adoptOpTimeout)
	if err != nil {
		httpError(w, http.StatusServiceUnavailable, "NODE", err.Error())
		return
	}
	switch rep.Result {
	case proto.ResAdopted:
		// успех — создаём сессию.
	case proto.ResNotIdle:
		httpError(w, http.StatusConflict, "NOT_IDLE", "кодер в панели не в простое: "+rep.Detail)
		return
	case proto.ResManaged:
		httpError(w, http.StatusConflict, "MANAGED", "панель уже под управлением runpilot: "+rep.Detail)
		return
	case proto.ResPaneGone:
		httpError(w, http.StatusGone, "PANE_GONE", "панель отсутствует")
		return
	default:
		httpError(w, http.StatusBadGateway, "ADOPT_FAILED", rep.Detail)
		return
	}
	now := s.clk.Now()
	rec := store.SessionRecord{
		SID: newSID, Name: name, Host: host, HostIP: s.hostIP(host),
		Profile: "qwen", State: model.SessionIdle, StateChangedAt: now,
		Class: model.ClassNormal, ConstraintKind: model.ConstraintNone,
		CreatedAt: now,
	}
	if err := s.store.CreateSession(rec); err != nil {
		httpError(w, http.StatusInternalServerError, "STORE", err.Error())
		return
	}
	// Панель уже управляема: снимок регистрируем сразу, как после spawn
	// (узел пришлёт актуальный в следующем цикле).
	s.hub.SetPane(host, proto.Pane{PaneID: paneID, SID: newSID, State: "IDLE"})
	if s.sched != nil {
		s.sched.PaneUpdate(paneID, newSID, model.PaneIdle, 0, now)
	}
	s.recordSessionCreated(newSID, now)
	s.log.Info("api: adopt: сессия создана", "sid", newSID, "name", name,
		"node", host, "pane", paneID)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"sid": newSID, "name": name, "node": host, "pane": paneID,
	})
}

// handleSessionDelete — DELETE /api/v1/sessions/{sid}: удаление GONE-сессии
// из БД (панель давно исчезла, остаётся только запись). Не-GONE → 409:
// сначала «Закрыть».
func (s *Server) handleSessionDelete(w http.ResponseWriter, r *http.Request) {
	sidv := r.PathValue("sid")
	rec, err := s.store.GetSession(sidv)
	if errors.Is(err, store.ErrNotFound) {
		httpError(w, http.StatusNotFound, "NOT_FOUND", "сессия не найдена")
		return
	}
	if err != nil {
		httpError(w, http.StatusInternalServerError, "STORE", err.Error())
		return
	}
	if rec.State != model.SessionGone {
		httpError(w, http.StatusConflict, "NOT_GONE",
			"удаляется только GONE-сессия; сначала «Закрыть»")
		return
	}
	if err := s.store.DeleteSession(sidv); err != nil {
		httpError(w, http.StatusInternalServerError, "STORE", err.Error())
		return
	}
	s.log.Info("api: сессия удалена", "sid", sidv, "name", rec.Name)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"deleted": sidv})
}

// RetentionLoop — периодическая очистка хранилища по retention.* (раздел 14
// ТЗ): запросы, события и GONE-сессии старше порогов. Первый прогон — сразу.
func (s *Server) RetentionLoop(ctx context.Context) {
	run := func() {
		out, err := s.store.Purge(s.clk.Now(), s.cfg.Retention)
		if err != nil {
			s.log.Warn("api: retention: purge", "err", err)
			return
		}
		if out.Requests > 0 || out.Events > 0 || out.Sessions > 0 {
			s.log.Info("api: retention: purge",
				"requests", out.Requests, "events", out.Events, "sessions", out.Sessions)
		}
	}
	run()
	t := time.NewTicker(retentionLoopTick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			run()
		}
	}
}
