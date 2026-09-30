// Раздел 8.1 ТЗ: runpilot exec — сессии JOB (cron) без tmux-панели.
//
//   POST /api/v1/jobs            — регистрация JOB-сессии + запись в очередь
//   GET  /api/v1/jobs/{sid}/wait — long-poll аренды, возвращает окружение шлюза
//   POST /api/v1/jobs/{sid}/heartbeat — пульс; ответ cancel → SIGTERM qwen
//   POST /api/v1/jobs/{sid}/finish  — завершение; аренда снимается TURN_DONE
//
// Аутентификация (раздел 15.3): токен оператора (стенд/локально) ИЛИ
// валидный токен узла с host_ip = IP узла.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"time"

	"runpilot/internal/model"
	"runpilot/internal/sid"
	"runpilot/internal/store"
)

// jobHostKey — ключ контекста: аутентифицированный host задания.
type ctxKey int

const jobHostKey ctxKey = iota

// jobsAuth — аутентификация каналов runpilot exec (раздел 15.3 ТЗ).
func (s *Server) jobsAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Пустой операторский токен — локальный режим (тесты, стенд).
		if s.token == "" {
			next.ServeHTTP(w, r)
			return
		}
		tok := bearerToken(r)
		// Операторский токен (стенд/тесты): host = "operator".
		if tok == s.token {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), jobHostKey, "operator")))
			return
		}
		// Токен узла: верификация + host_ip = IP узла (раздел 15.3).
		host, ok := s.store.VerifyNodeToken(tok)
		if !ok {
			httpError(w, http.StatusForbidden, "JOB_AUTH", "неверный или отозванный токен узла")
			return
		}
		if n, ok := s.hub.Node(host); ok && n.HostIP != "" && n.HostIP != remoteIP(r) {
			httpError(w, http.StatusForbidden, "JOB_IP", "IP запроса не совпадает с узлом")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), jobHostKey, host)))
	})
}

// remoteIP — IP источника (без порта).
func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// jobCreateRequest — тело POST /api/v1/jobs.
type jobCreateRequest struct {
	Name   string `json:"name"`
	Prio   string `json:"prio"`
	Pin    string `json:"pin"`
	Prefer string `json:"prefer"`
}

// jobCreateResponse — ответ POST /api/v1/jobs.
type jobCreateResponse struct {
	SID string `json:"sid"`
}

// handleJobCreate — регистрация JOB-сессии (раздел 8.1, п.2).
func (s *Server) handleJobCreate(w http.ResponseWriter, r *http.Request) {
	var req jobCreateRequest
	_ = json.NewDecoder(r.Body).Decode(&req)

	prio := req.Prio
	if prio == "" {
		prio = model.ClassNormal.String()
	}
	class, ok := model.QueueClassParse(prio)
	if !ok {
		httpError(w, http.StatusBadRequest, "BAD_PRIO", "неизвестный класс очереди: "+prio)
		return
	}
	c := model.NoConstraint
	if req.Pin != "" {
		c = model.Constraint{Kind: model.ConstraintPin, Server: req.Pin}
	} else if req.Prefer != "" {
		c = model.Constraint{Kind: model.ConstraintPrefer, Server: req.Prefer}
	}

	newSID, err := sid.New()
	if err != nil {
		httpError(w, http.StatusInternalServerError, "SID", err.Error())
		return
	}
	name := req.Name
	if name == "" {
		name = "job-" + newSID
	}
	if live, err := s.store.FindLiveByName(name); err == nil {
		httpError(w, http.StatusConflict, "NAME_IN_USE",
			"имя занято сессией "+live.SID)
		return
	}

	now := s.clk.Now()
	host, _ := r.Context().Value(jobHostKey).(string)
	rec := store.SessionRecord{
		SID: newSID, Name: name, Host: host, HostIP: remoteIP(r),
		Profile: "qwen", Kind: model.KindJob,
		State: model.SessionIdle, StateChangedAt: now,
		Class: class, ConstraintKind: c.Kind, ConstraintServer: c.Server,
		CreatedAt: now,
	}
	if err := s.store.CreateSession(rec); err != nil {
		s.log.Error("api: jobs: создание: " + err.Error())
		httpError(w, http.StatusInternalServerError, "STORE", err.Error())
		return
	}
	// Ставим в очередь (проверки панели для JOB не выполняются).
	if err := s.sched.Enqueue(newSID, model.QueueEntry{
		SID: newSID, Class: class, Constraint: c,
	}, false); err != nil {
		// Сессия создана; очередь не поставлена — 500, клиент увидит ошибку.
		s.log.Error("api: jobs: enqueue: " + err.Error())
		httpError(w, http.StatusInternalServerError, "ENQUEUE", err.Error())
		return
	}
	s.log.Info("api: jobs: задание зарегистрировано", "sid", newSID, "name", name, "host", host)
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(jobCreateResponse{SID: newSID})
}

// jobWaitResponse — ответ GET /jobs/{sid}/wait (аренда выдана).
type jobWaitResponse struct {
	SID    string            `json:"sid"`
	Server string            `json:"server"`
	Slot   int               `json:"slot"`
	Env    map[string]string `json:"env"`
}

// handleJobWait — long-poll аренды (раздел 8.1, п.3).
// Возвращает окружение шлюза при выдаче аренды; 204 — ещё ждём;
// 410 — сессия GONE/HOLD(EMERGENCY) (клиент прекращает ожидание).
func (s *Server) handleJobWait(w http.ResponseWriter, r *http.Request) {
	si := r.PathValue("sid")
	if _, err := s.store.GetSession(si); errors.Is(err, store.ErrNotFound) {
		httpError(w, http.StatusNotFound, "NOT_FOUND", "нет сессии")
		return
	}

	waitSec := jobWaitDefaultSec
	if v := r.URL.Query().Get("wait_sec"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			waitSec = n
			if waitSec > jobWaitMaxSec {
				waitSec = jobWaitMaxSec
			}
		}
	}
	deadline := time.Now().Add(time.Duration(waitSec) * time.Second)

	for {
		if l, err := s.store.LeaseGet(si); err == nil && l.State != model.LeaseReleased {
			s.writeJobEnv(w, si, l)
			return
		}
		// Сессия уже не в очереди (GONE / HOLD(EMERGENCY) / GONE) — стоп.
		if sess, err := s.store.GetSession(si); err == nil {
			if sess.State == model.SessionGone || sess.State == model.SessionHold {
				w.WriteHeader(http.StatusGone)
				return
			}
		}
		if time.Now().After(deadline) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(jobWaitPollMS * time.Millisecond):
		}
	}
}

// writeJobEnv — окружение шлюза для runpilot exec (раздел 8.1, п.4).
func (s *Server) writeJobEnv(w http.ResponseWriter, si string, l *model.Lease) {
	gw := s.cfg.Coordinator.GatewayURL
	env := map[string]string{
		"OPENAI_BASE_URL": gw + "/s/" + si + "/v1",
		"OPENAI_API_KEY":  "runpilot",
		"OPENAI_MODEL":    s.cfg.Profiles.Qwen.ModelAlias,
		"RUNPILOT_SID":         si,
	}
	_ = json.NewEncoder(w).Encode(jobWaitResponse{
		SID: si, Server: l.Server, Slot: l.Slot, Env: env,
	})
}

// jobHeartbeatResponse — ответ POST /jobs/{sid}/heartbeat.
type jobHeartbeatResponse struct {
	Cancel bool `json:"cancel"`
}

// handleJobHeartbeat — пульс задания (раздел 8.1, п.5; X5).
// cancel — аварийная остановка (EMERGENCY) или «Прервать» из веба.
func (s *Server) handleJobHeartbeat(w http.ResponseWriter, r *http.Request) {
	si := r.PathValue("sid")
	if _, err := s.store.GetSession(si); errors.Is(err, store.ErrNotFound) {
		httpError(w, http.StatusNotFound, "NOT_FOUND", "нет сессии")
		return
	}
	_ = s.store.JobBeat(si, s.clk.Now())
	cancel := s.store.JobCancel(si)
	if !cancel {
		if m, err := s.store.Mode(); err == nil && m == string(model.ModeEmergency) {
			cancel = true
		}
	}
	_ = json.NewEncoder(w).Encode(jobHeartbeatResponse{Cancel: cancel})
}

// jobFinishRequest — тело POST /jobs/{sid}/finish.
type jobFinishRequest struct {
	ExitCode int    `json:"exit_code"`
	Signal   string `json:"signal"`
}

// handleJobFinish — завершение задания (раздел 8.1, п.7): аренда TURN_DONE,
// исход OK (код 0) / ERROR (не 0), сессия GONE. Без авто-перепостановки.
func (s *Server) handleJobFinish(w http.ResponseWriter, r *http.Request) {
	si := r.PathValue("sid")
	sess, err := s.store.GetSession(si)
	if errors.Is(err, store.ErrNotFound) {
		httpError(w, http.StatusNotFound, "NOT_FOUND", "нет сессии")
		return
	}
	var req jobFinishRequest
	_ = json.NewDecoder(r.Body).Decode(&req)

	now := s.clk.Now()
	outcome := "ERROR"
	if req.ExitCode == 0 && req.Signal == "" {
		outcome = "OK"
	}
	// Аренда снимается TURN_DONE (идемпотентно, если уже снята).
	_ = s.store.LeaseRelease(si, model.ReleaseTurnDone, now)
	_ = s.store.QueueDelete(si)
	_ = s.store.JobDelete(si)
	_ = s.store.EventRecord(model.Event{
		TS: now, Kind: model.KindJobState, SID: si,
		Payload: mustJSON(map[string]any{
			"outcome":   outcome,
			"exit_code": req.ExitCode,
			"signal":    req.Signal,
		}),
	})
	_ = s.store.SetSessionState(si, model.SessionGone, "", now)
	_ = sess.Name // имя для лога
	s.log.Info("api: jobs: задание завершено", "sid", si, "outcome", outcome,
		"exit_code", req.ExitCode, "signal", req.Signal)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "outcome": outcome})
}

// mustJSON — JSON-мarshal без ошибки (payload события).
func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
