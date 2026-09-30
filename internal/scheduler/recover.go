package scheduler

// Восстановление после аварийного завершения координатора (v2 раздел 6.5).
//
// Координатор пишет meta.last_alive_at каждые coordinator.heartbeat_sec
// (heartbeatLoop). При старте, если простой [last_alive_at, now] длиннее
// heartbeat_sec — координатор пережил аварийное завершение (kill -9, OOM,
// паника), и идущие ходы рассудаются по правилу 6.5.3:
//   - последний запрос хода 2xx и завершился ДО last_alive_at −
//     turn.done_quiet_sec → OK (ход завершился, координатор не успел
//     записать закрытие): аренда снята TURN_DONE, ход OK, сессия IDLE;
//   - иначе → LOST = ERROR_TRANSIENT: attempts += 1, аренда снята CRASH,
//     ход LOST, сессия → очередь (resume).

import (
	"net/http"
	"time"

	"runpilot/internal/model"
)

// RecoverFromCrash — рассудок идущих ходов после аварийного простоя.
// Вызывается на старте (до Start). Возвращает число LOST (→RESUME).
func (s *Scheduler) RecoverFromCrash() (int, error) {
	if s.st == nil {
		return 0, nil
	}
	last, err := s.st.LastAliveAt()
	if err != nil || last.IsZero() {
		return 0, nil // первый запуск / метрика пуста
	}
	hb := time.Duration(s.cfg.Coordinator.HeartbeatSec) * time.Second
	if hb <= 0 {
		return 0, nil // интервал пульса не задан — габ не определяем
	}
	now := s.clk.Now()
	if now.Sub(last) <= hb {
		return 0, nil // нет габ — чистое завершение
	}

	quiet := time.Duration(s.cfg.Turn.DoneQuietSec) * time.Second
	cutoff := last.Add(-quiet)
	leases, err := s.st.LeaseListActive()
	if err != nil {
		return 0, err
	}
	lost, ok := 0, 0
	for _, l := range leases {
		sess, err := s.st.GetSession(l.SID)
		if err != nil {
			continue
		}
		if s.turnFinishedOK(l.SID, cutoff) {
			if t, err := s.st.OpenTurn(l.SID); err == nil && t != nil {
				_ = s.st.TurnClose(t.ID, now, model.TurnOK, t.Requests, l.Server)
			}
			_ = s.st.LeaseRelease(l.SID, model.ReleaseTurnDone, now)
			_ = s.st.SetSessionLastTurnEnd(l.SID, now)
			_ = s.st.SetSessionState(l.SID, model.SessionIdle, "", now)
			ok++
			continue
		}
		// LOST → ERROR_TRANSIENT (6.5.4).
		_ = s.st.SetSessionAttempts(l.SID, sess.Attempts+1)
		if t, err := s.st.OpenTurn(l.SID); err == nil && t != nil {
			_ = s.st.TurnClose(t.ID, now, model.TurnLost, t.Requests, l.Server)
		}
		_ = s.st.LeaseRelease(l.SID, model.ReleaseCrash, now)
		_ = s.st.SetSessionLastTurnEnd(l.SID, now)
		_ = s.st.SetSessionState(l.SID, model.SessionQueued, "", now)
		_ = s.st.QueueUpsert(model.QueueEntry{
			SID:        l.SID,
			Class:      sess.Class,
			EnqueuedAt: now,
			Constraint: model.Constraint{Kind: sess.ConstraintKind, Server: sess.ConstraintServer},
			Mode:       model.QueueModeResume,
		})
		_ = s.st.EventRecord(model.Event{TS: now, Kind: model.KindQueueEnqueue, SID: l.SID, Server: l.Server})
		lost++
	}
	if lost > 0 || ok > 0 {
		s.notifyf("runpilot: [WARN] восстановление после простоя %s: LOST %d (→RESUME), OK %d",
			now.Sub(last).Round(time.Second), lost, ok)
	}
	return lost, nil
}

// turnFinishedOK — 6.5.3: последний запрос хода 2xx и завершился раньше
// cutoff (last_alive_at − done_quiet_sec). Для kill -9 посреди хода запрос
// не завершён → false → LOST.
func (s *Scheduler) turnFinishedOK(sid string, cutoff time.Time) bool {
	reqs, err := s.st.RequestList(sid, 1)
	if err != nil || len(reqs) == 0 {
		return false
	}
	r := reqs[0]
	return r.Status >= http.StatusOK && r.Status < http.StatusMultipleChoices &&
		!r.TEnd.IsZero() && r.TEnd.Before(cutoff)
}
