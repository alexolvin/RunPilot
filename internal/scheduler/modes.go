package scheduler

// Режимы безопасности координатора (v2 разделы 6.2/6.4): аварийная
// остановка (EMERGENCY) и безопасный режим (SAFE_MODE). Выдача новых аренд
// блокируется (grantsBlocked); при EMERGENCY идущие ходы снимаются.

import (
	"context"
	"time"

	"runpilot/internal/model"
	"runpilot/internal/proto"
	"runpilot/internal/store"
)

// SetEmergency — аварийная остановка (6.2). Порядок:
//  1. meta.mode = EMERGENCY — первым шагом (переживает рестарт);
//  2. новые аренды не выдаются (grantsBlocked);
//  3. идущие ходы снимаются (аренда EMERGENCY, ход CANCELLED, сессия
//     HOLD(EMERGENCY)), панели получают cancel_keys;
//  4. событие MODE + [CRIT].
//
// Шлюз (503 runpilot_emergency + отмена потоков) и клиенты runpilot exec (cancel)
// реагируют на режим отдельно.
func (s *Scheduler) SetEmergency() error {
	if err := s.st.SetMode(string(model.ModeEmergency)); err != nil {
		return err
	}
	var cancelPanes []string
	s.mu.Lock()
	first := s.mode != string(model.ModeEmergency)
	if first {
		s.mode = string(model.ModeEmergency)
		s.emergencyStopLocked(s.clk.Now(), &cancelPanes)
	}
	s.mu.Unlock()
	if !first {
		return nil
	}
	// 6.2 шаг 3: отмена идущих потоков к апстримам (шлюз).
	if h := s.emergencyHook; h != nil {
		h()
	}
	s.cancelKeysPanes(cancelPanes)
	s.event(model.KindMode, "", "", map[string]any{"mode": string(model.ModeEmergency)})
	s.notifyf("runpilot: [CRIT] аварийная остановка: ходы прерваны, сессии в HOLD(EMERGENCY)")
	return nil
}

// ClearEmergency — выход из аварийного режима (6.2): → PAUSED (оператор
// вручную возвращает сессии и возобновляет очередь).
func (s *Scheduler) ClearEmergency() error {
	if err := s.st.SetMode(string(model.ModeNormal)); err != nil {
		return err
	}
	s.mu.Lock()
	first := s.mode == string(model.ModeEmergency)
	s.mode = ""
	if first {
		s.paused = true
	}
	s.mu.Unlock()
	if first {
		s.event(model.KindMode, "", "", map[string]any{"mode": string(model.ModePaused)})
		s.notifyf("runpilot: аварийный режим снят, координатор в паузе")
	}
	return nil
}

// AgentExit — 7.3 C1: кодер в управляемой панели завершился/убит (@runpilot_exit):
// аренда AGENT_EXITED, ход LOST, сессия → HOLD(AGENT_EXITED). code > 128 —
// сигнал (code−128); 9 — SIGKILL (вероятно, OOM-killer).
func (s *Scheduler) AgentExit(sid string, code int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.agentExitLocked(sid, code)
}

func (s *Scheduler) agentExitLocked(sid string, code int) {
	sess, err := s.st.GetSession(sid)
	if err != nil {
		return
	}
	if sess.State != model.SessionRunning && sess.State != model.SessionDispatching &&
		sess.State != model.SessionDetached {
		return // уже не в ходе — не трогаем
	}
	now := s.clk.Now()
	var server string
	if lease, err := s.st.LeaseGet(sid); err == nil {
		server = lease.Server
	}
	s.releaseLeaseLocked(sid, model.ReleaseAgentExited, now)
	if t, err := s.st.OpenTurn(sid); err == nil && t != nil {
		_ = s.st.TurnClose(t.ID, now, model.TurnLost, t.Requests, server)
		s.event(model.KindTurnEnd, sid, server, map[string]any{
			"turn_id": t.ID, "outcome": string(model.TurnLost), "requests": t.Requests,
		})
	}
	_ = s.st.SetSessionLastTurnEnd(sid, now)
	s.sessionEvent(sess, model.EvAgentExit, model.HoldAgentExited)
	s.wake()
	if code > signalCodeBase {
		sig := code - signalCodeBase
		if sig == sigKill {
			s.notifyf("runpilot: [WARN] %s: кодер завершился: сигнал KILL — вероятно, OOM-killer", sess.Name)
		} else {
			s.notifyf("runpilot: [WARN] %s: кодер завершился: сигнал %d", sess.Name, sig)
		}
	} else {
		s.notifyf("runpilot: [WARN] %s: кодер завершился: код %d", sess.Name, code)
	}
}

// emergencyStopLocked — снять идущие ходы при аварийной остановке (вызывать
// с mu): аренда EMERGENCY, ход CANCELLED, сессия → HOLD(EMERGENCY).
func (s *Scheduler) emergencyStopLocked(now time.Time, cancelPanes *[]string) {
	for _, sess := range s.sessionsMap() {
		switch sess.State {
		case model.SessionDispatching, model.SessionRunning:
			s.emergencyReleaseTurn(sess, now)
			if pr := s.paneRec[sess.SID]; pr != nil && pr.paneID != "" {
				*cancelPanes = append(*cancelPanes, pr.paneID)
			}
			s.sessionEvent(sess, model.EvEmergency, model.HoldEmergency)
		case model.SessionDetached:
			if pr := s.paneRec[sess.SID]; pr != nil && pr.paneID != "" {
				*cancelPanes = append(*cancelPanes, pr.paneID)
			}
			s.sessionEvent(sess, model.EvEmergency, model.HoldEmergency)
		}
	}
}

// emergencyReleaseTurn — закрыть ход CANCELLED + снять аренду (вызывать с mu).
func (s *Scheduler) emergencyReleaseTurn(sess store.SessionRecord, now time.Time) {
	if turn, err := s.st.OpenTurn(sess.SID); err == nil && turn != nil {
		reqCount, _ := s.st.CountRequestsSince(sess.SID, turn.StartedAt)
		serversCSV, _ := s.st.ServersSince(sess.SID, turn.StartedAt)
		_ = s.st.TurnClose(turn.ID, now, model.TurnCancelled, reqCount, serversCSV)
		s.event(model.KindTurnEnd, sess.SID, "", map[string]any{
			"turn_id": turn.ID, "outcome": string(model.TurnCancelled),
			"requests": reqCount, "servers": serversCSV,
		})
	}
	s.releaseLeaseLocked(sess.SID, model.ReleaseEmergency, now)
}

// cancelKeysPanes — отправить cancel-клавиши панелям (6.2 шаг 4: кодеры
// перестают генерировать). Ошибки не фатальны — ходы уже сняты.
func (s *Scheduler) cancelKeysPanes(panes []string) {
	if len(s.cancelKeys) == 0 {
		return
	}
	for _, paneID := range panes {
		ctx, cancel := context.WithTimeout(context.Background(), s.nodeReplyTimeout())
		m := proto.New(proto.KindSendKeys)
		m.PaneID, m.Keys = paneID, s.cancelKeys
		_, _ = s.dispatch(ctx, paneID, m)
		cancel()
	}
}

// SetSafeMode — безопасный режим (6.4): сработал при ошибке записи SQLite
// (SQLITE_FULL/IOERR). Новые аренды не выдаются; идущие обслуживаются.
func (s *Scheduler) SetSafeMode(reason string) error {
	if err := s.st.SetMode(string(model.ModeSafeMode)); err != nil {
		return err
	}
	s.mu.Lock()
	first := s.mode != string(model.ModeSafeMode)
	if first {
		s.mode = string(model.ModeSafeMode)
		s.safeSince = s.clk.Now()
	}
	s.mu.Unlock()
	if first {
		s.event(model.KindMode, "", "", map[string]any{"mode": string(model.ModeSafeMode), "reason": reason})
		s.notifyf("runpilot: [CRIT] безопасный режим: записей в БД нет (%s)", reason)
	}
	return nil
}

// ArmWriteErrorHook — v2 (K6): ошибка записи в БД автоматически переводит
// координатор в SAFE_MODE (6.4). Вызывается на старте (после LoadMode).
func (s *Scheduler) ArmWriteErrorHook() {
	if s.st == nil {
		return
	}
	s.st.SetWriteErrorHook(s.onStoreWriteError)
}

// onStoreWriteError — хук store (вызывается асинхронно из write-loop-горутины):
// атомарно переводит в SAFE_MODE (идемпотентно), блокирует новые аренды до
// успешной пробной записи (tickSafeProbe).
func (s *Scheduler) onStoreWriteError(err error) {
	if err == nil {
		return
	}
	s.mu.Lock()
	already := s.mode == string(model.ModeSafeMode)
	if !already {
		s.mode = string(model.ModeSafeMode)
		s.safeSince = s.clk.Now()
	}
	s.mu.Unlock()
	if already {
		return
	}
	// Persist best-effort (запись может снова упасть — s.mode уже SAFE_MODE
	// в памяти и блокирует выдачи).
	_ = s.st.SetMode(string(model.ModeSafeMode))
	s.event(model.KindMode, "", "", map[string]any{"mode": string(model.ModeSafeMode), "reason": "write_error"})
	s.notifyf("runpilot: [CRIT] безопасный режим: запись в БД невозможна (%s)", err)
}

// RequeueHoldEmergency — «Вернуть в очередь все сессии HOLD(EMERGENCY)»
// (6.2 выход): RESUME-записи + сессии → QUEUED. Возвращает число сессий.
func (s *Scheduler) RequeueHoldEmergency() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clk.Now()
	n := 0
	for _, sess := range s.sessionsMap() {
		if sess.State != model.SessionHold || sess.HoldReason != model.HoldEmergency {
			continue
		}
		entry := model.QueueEntry{
			SID:         sess.SID,
			Class:       model.ClassResume,
			EnqueuedAt:  now,
			NotBefore:   now,
			Constraint:  model.Constraint{Kind: sess.ConstraintKind, Server: sess.ConstraintServer},
			Mode:        model.QueueModeResume,
		}
		if err := s.st.QueueUpsert(entry); err != nil {
			s.log.Error("scheduler: requeue emergency", "sid", sess.SID, "err", err)
			continue
		}
		s.event(model.KindQueueEnqueue, sess.SID, "", map[string]any{"emergency": true})
		s.sessionEvent(sess, model.EvRequeue, "")
		n++
	}
	return n
}

// ClearSafeMode — выход из безопасного режиме (6.4): после успешной пробной
// записи.
func (s *Scheduler) ClearSafeMode() error {
	if err := s.st.SetMode(string(model.ModeNormal)); err != nil {
		return err
	}
	s.mu.Lock()
	first := s.mode == string(model.ModeSafeMode)
	s.mode = ""
	s.mu.Unlock()
	if first {
		s.event(model.KindMode, "", "", map[string]any{"mode": string(model.ModeNormal)})
		s.notifyf("runpilot: безопасный режим снят, записи восстановлены")
	}
	return nil
}

// tickSafeProbe — пробная запись (6.4): раз в monitor.safe_probe_sec. Успех →
// ClearSafeMode. Вызывается из Tick при SAFE_MODE (s.mu держится).
func (s *Scheduler) tickSafeProbe(now time.Time) bool {
	if s.mode != string(model.ModeSafeMode) {
		return false
	}
	interval := time.Duration(s.cfg.Monitor.SafeProbeSec) * time.Second
	if now.Sub(s.safeSince) < interval && !s.safeSince.IsZero() {
		return false
	}
	s.safeSince = now
	if err := s.st.ProbeWrite(); err != nil {
		s.log.Warn("scheduler: safe_probe: запись не удалась", "err", err)
		return false
	}
	// ClearSafeMode запишет meta.mode = NORMAL и снимет режим.
	_ = s.st.SetMode(string(model.ModeNormal))
	s.mode = ""
	s.event(model.KindMode, "", "", map[string]any{"mode": string(model.ModeNormal)})
	s.notifyf("runpilot: безопасный режим снят, записи восстановлены")
	return true
}
