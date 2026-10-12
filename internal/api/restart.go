package api

// Перезапуск кодера (W15 этап 3): только IDLE-сессия, respawn-pane с тем же
// sid/name/панелью + --continue (кодер возобновляет диалог). Нужен после смены
// пула (модальности/контекст) — сессия, стартовавшая до смены, работает со
// старым system-settings.json.

import (
	"context"
	"errors"
	"net/http"

	"runpilot/internal/command"
	"runpilot/internal/model"
	"runpilot/internal/proto"
	"runpilot/internal/store"
)

// restartAgentTimeout — ожидание respawn-pane при перезапуске кодера.
const restartAgentTimeout = adoptOpTimeout

// handleRestart — POST /api/v1/sessions/{sid}/restart: respawn с --continue.
// Только IDLE; иначе 409. Возвращает {sid, pane}.
func (s *Server) handleRestart(w http.ResponseWriter, r *http.Request) {
	out, err := s.CmdRestart(r.Context(), r.PathValue("sid"))
	if err != nil {
		httpError(w, http.StatusInternalServerError, "RESTART", err.Error())
		return
	}
	switch out.Code {
	case "OK":
		writeJSON(w, map[string]string{"sid": r.PathValue("sid"), "pane": out.Text})
	case "NOT_FOUND":
		httpError(w, http.StatusNotFound, out.Code, out.Text)
	case "NOT_IDLE", "NO_PANE":
		httpError(w, http.StatusConflict, out.Code, out.Text)
	default:
		httpError(w, http.StatusBadGateway, out.Code, out.Text)
	}
}

// CmdRestart — перезапуск кодера (CLI /restart + веб-кнопка): только IDLE,
// respawn-pane с тем же sid/панелью + --continue. Возвращает Outcome
// (Text = pane ID при OK).
func (s *Server) CmdRestart(ctx context.Context, sidv string) (command.Outcome, error) {
	sess, err := s.store.GetSession(sidv)
	if errors.Is(err, store.ErrNotFound) {
		return command.Outcome{Code: "NOT_FOUND", Text: "нет сессии"}, nil
	}
	if err != nil {
		return command.Outcome{Code: "STORE", Text: err.Error()}, nil
	}
	if sess.State != model.SessionIdle {
		return command.Outcome{Code: "NOT_IDLE", Text: "перезапуск только для IDLE (сейчас " + string(sess.State) + ")"}, nil
	}
	if sess.Host == "" || sess.PaneID == "" {
		return command.Outcome{Code: "NO_PANE", Text: "нет панели (сессия не на узле?)"}, nil
	}
	env, err := s.spawnEnv(sidv)
	if err != nil {
		return command.Outcome{Code: "ENV", Text: err.Error()}, nil
	}
	m := proto.New(proto.KindRespawn)
	m.SID = sidv
	m.PaneID = sess.PaneID
	m.Env = env
	m.ContextWindow = s.MinContextWindow()
	m.InputModalities = s.IntersectionModalities()
	// R0 ТЗ Relay §3: --resume <uuid> вместо --continue (кодер возобновляет
	// запись). Сессии до R0 без qwen_session_id — как раньше (--continue).
	if sess.QwenSessionID != "" {
		m.QwenSessionID = sess.QwenSessionID
	} else {
		m.Args = []string{"--continue"}
	}
	rep, err := s.dispatchToNodeTimeout(ctx, sess.Host, m, restartAgentTimeout)
	if err != nil {
		return command.Outcome{Code: "NODE", Text: err.Error()}, nil
	}
	if rep.Result != proto.ResRespawned {
		return command.Outcome{Code: rep.Result, Text: rep.Detail}, nil
	}
	s.log.Info("api: restart: кодер перезапущен (--continue)", "sid", sidv, "pane", rep.Detail)
	return command.Outcome{Code: "OK", Text: rep.Detail}, nil
}
