package scheduler

import (
	"fmt"
	"time"

	"runpilot/internal/model"
	"runpilot/internal/store"
)

// Enqueue — ручная постановка (раздел 9 ТЗ). target — sid или имя.
func (s *Scheduler) Enqueue(target string, e model.QueueEntry, front bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.enqueueLocked(target, e, front)
}

// enqueueLocked — ядро Enqueue без блокировки: вызывается и из публичного
// Enqueue, и из тика (tickAutoEnqueue), где s.mu уже захвачен — повторный
// Lock на не-реентрантном мьютексе заклинил бы планировщик.
// Проверки: сессия не GONE, панель IDLE или BUSY; при IDLE ввод не пуст.
// Флаг front — enqueued_at на 1 мс раньше первой записи того же класса.
func (s *Scheduler) enqueueLocked(target string, e model.QueueEntry, front bool) error {
	now := s.clk.Now()

	sess, err := s.resolveSession(target)
	if err != nil {
		return err
	}
	if sess.State == model.SessionGone {
		return fmt.Errorf("enqueue: сессия %s GONE", target)
	}

	// Панель: IDLE или BUSY; при IDLE — ввод не пуст.
	if pr := s.paneRec[sess.SID]; pr != nil && pr.paneID != "" {
		switch pr.state {
		case model.PaneIdle:
			if snap, ok := s.panes.PaneBySID(sess.SID); ok && snap.InputEmpty {
				return fmt.Errorf("enqueue: %s: ввод пуст", target)
			}
		case model.PaneBusy:
			// допустимо (предварительная постановка RUNNING-сессии)
		default:
			return fmt.Errorf("enqueue: %s: панель %s", target, pr.state)
		}
	}

	if e.SID == "" {
		e.SID = sess.SID
	}
	// Пустая запись (не RESUME: у resume всегда Mode=resume) — класс
	// сессии по умолчанию.
	if e.Class == 0 && e.Mode == "" {
		e.Class = sess.Class
	}
	if e.Constraint.Kind == "" {
		e.Constraint = model.Constraint{Kind: sess.ConstraintKind, Server: sess.ConstraintServer}
	}
	if e.Mode == "" {
		e.Mode = model.QueueModeSubmit
	}
	if e.NotBefore.IsZero() {
		e.NotBefore = now
	}
	e.EnqueuedAt = now
	if front {
		if t := s.frontTime(e.Class, now); !t.IsZero() {
			e.EnqueuedAt = t.Add(-time.Millisecond)
		}
	}
	if err := s.st.QueueUpsert(e); err != nil {
		return err
	}

	if sess.State == model.SessionIdle || sess.State == model.SessionHold {
		ev := model.EvEnqueue
		if sess.State == model.SessionHold {
			ev = model.EvRequeue
		}
		s.sessionEvent(sess, ev, "")
	}
	s.event(model.KindQueueEnqueue, sess.SID, "", map[string]any{
		"class": e.Class.String(), "mode": string(e.Mode), "front": front,
	})
	return nil
}

// frontTime — enqueued_at первой записи класса (для --front).
func (s *Scheduler) frontTime(c model.QueueClass, now time.Time) time.Time {
	entries, err := s.st.QueueList()
	if err != nil {
		return time.Time{}
	}
	var first time.Time
	for _, e := range entries {
		if e.Class != c {
			continue
		}
		if first.IsZero() || e.EnqueuedAt.Before(first) {
			first = e.EnqueuedAt
		}
	}
	return first
}

// Dequeue — снять запись; сессия QUEUED → IDLE.
func (s *Scheduler) Dequeue(target string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, err := s.resolveSession(target)
	if err != nil {
		return err
	}
	if err := s.st.QueueDelete(sess.SID); err != nil && err != store.ErrNotFound {
		return err
	}
	delete(s.why, sess.SID)
	s.event(model.KindQueueDequeue, sess.SID, "", map[string]any{})
	if sess.State == model.SessionQueued {
		s.sessionEvent(sess, model.EvDequeue, "")
	}
	return nil
}

// ClearQueueAll — снять все записи очереди (O5 «Очистить очередь»): сессии
// QUEUED → IDLE, порядок записей сохранён для отмены. Возвращает удалённые
// записи в порядке очереди (для RestoreQueue).
func (s *Scheduler) ClearQueueAll() ([]model.QueueEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := s.st.QueueList()
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if err := s.st.QueueDelete(e.SID); err != nil && err != store.ErrNotFound {
			return nil, err
		}
		delete(s.why, e.SID)
		s.event(model.KindQueueDequeue, e.SID, "", map[string]any{"bulk": true})
		if sess, err := s.st.GetSession(e.SID); err == nil && sess.State == model.SessionQueued {
			s.sessionEvent(sess, model.EvDequeue, "")
		}
	}
	return entries, nil
}

// RestoreQueue — вернуть записи в очередь, сохранив порядок и enqueued_at
// (O5 отмена «Очистить очередь» / «Вернуть все»).
func (s *Scheduler) RestoreQueue(entries []model.QueueEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range entries {
		sess, err := s.st.GetSession(e.SID)
		if err != nil || sess.State == model.SessionGone {
			continue
		}
		if err := s.st.QueueUpsert(e); err != nil {
			return err
		}
		if sess.State == model.SessionIdle || sess.State == model.SessionHold {
			ev := model.EvEnqueue
			if sess.State == model.SessionHold {
				ev = model.EvRequeue
			}
			s.sessionEvent(sess, ev, "")
		}
		s.event(model.KindQueueEnqueue, e.SID, "", map[string]any{"undo": true})
	}
	return nil
}

// Requeue — вернуть из HOLD в очередь (раздел 9 ТЗ). back — в конец
// класса; иначе исходный enqueued_at сохраняется.
func (s *Scheduler) Requeue(target string, back bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clk.Now()
	sess, err := s.resolveSession(target)
	if err != nil {
		return err
	}
	if sess.State != model.SessionHold {
		return fmt.Errorf("requeue: %s не в HOLD (%s)", target, sess.State)
	}
	e, err := s.st.QueueGet(sess.SID)
	if err != nil {
		e = &model.QueueEntry{
			SID:        sess.SID,
			Class:      sess.Class,
			Constraint: model.Constraint{Kind: sess.ConstraintKind, Server: sess.ConstraintServer},
			Mode:       model.QueueModeSubmit,
		}
	}
	if back || e.EnqueuedAt.IsZero() {
		e.EnqueuedAt = now
	}
	e.NotBefore = now
	_ = s.st.QueueUpsert(*e)
	s.sessionEvent(sess, model.EvRequeue, "")
	s.event(model.KindQueueEnqueue, sess.SID, "", map[string]any{"requeue": true})
	return nil
}

// Prio — сменить класс (runpilot prio).
func (s *Scheduler) Prio(target string, c model.QueueClass) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, err := s.resolveSession(target)
	if err != nil {
		return err
	}
	_ = s.st.SetSessionClass(sess.SID, c)
	if e, err := s.st.QueueGet(sess.SID); err == nil {
		e.Class = c
		_ = s.st.QueueUpsert(*e)
	}
	return nil
}

// Pin / Prefer / Unpin — ограничения (runpilot pin/prefer/unpin).
func (s *Scheduler) Pin(target, server string) error    { return s.constraint(target, model.ConstraintPin, server) }
func (s *Scheduler) Prefer(target, server string) error { return s.constraint(target, model.ConstraintPrefer, server) }
func (s *Scheduler) Unpin(target string) error          { return s.constraint(target, model.ConstraintNone, "") }

func (s *Scheduler) constraint(target string, kind model.ConstraintKind, server string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, err := s.resolveSession(target)
	if err != nil {
		return err
	}
	_ = s.st.SetSessionConstraint(sess.SID, kind, server)
	if e, err := s.st.QueueGet(sess.SID); err == nil {
		e.Constraint = model.Constraint{Kind: kind, Server: server}
		_ = s.st.QueueUpsert(*e)
	}
	return nil
}

// Hold — ручной hold (runpilot hold).
func (s *Scheduler) Hold(target string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, err := s.resolveSession(target)
	if err != nil {
		return err
	}
	if sess.State != model.SessionIdle && sess.State != model.SessionQueued {
		return fmt.Errorf("hold: %s в состоянии %s", target, sess.State)
	}
	s.sessionEvent(sess, model.EvHold, model.HoldOperator)
	return nil
}

// Unhold — снять ручной hold (runpilot unhold).
func (s *Scheduler) Unhold(target string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, err := s.resolveSession(target)
	if err != nil {
		return err
	}
	if sess.State != model.SessionHold {
		return fmt.Errorf("unhold: %s не в HOLD", target)
	}
	s.sessionEvent(sess, model.EvUnhold, "")
	return nil
}

// resolveSession — sid или имя → запись.
func (s *Scheduler) resolveSession(target string) (store.SessionRecord, error) {
	if r, err := s.st.GetSession(target); err == nil {
		return r, nil
	}
	return s.st.FindLiveByName(target)
}

// --- why (GET /api/v1/sessions/{t}/why) ---

// Why — причина, по которой запись не получает слот: результат последней
// оценки (или пересчёт сейчас) + набор кандидатов + предикаты.
func (s *Scheduler) Why(target string) (Why, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clk.Now()
	sess, err := s.resolveSession(target)
	if err != nil {
		return Why{}, err
	}
	out := Why{SID: sess.SID, State: sess.State, Candidates: []string{}}
	e, err := s.st.QueueGet(sess.SID)
	if err != nil {
		return out, nil
	}
	if rec, ok := s.why[sess.SID]; ok && now.Sub(rec.ts) < s.whyFresh() {
		out.Reason = rec.reason
		out.Candidates = rec.candidates
		out.Predicates = rec.predicates
		return out, nil
	}
	// Пересчёт: как в тике.
	slots := s.freeSlots(now)
	cand := []string{}
	for _, f := range slots {
		if !f.taken {
			if v, ok := s.viewOf(f.Name); ok && s.eligible(*e, sess, v, s.paneRec[sess.SID], now) {
				cand = append(cand, f.Name)
			}
		}
	}
	if len(cand) > 0 {
		out.Reason = ""
		out.Candidates = cand
		return out, nil
	}
	reason, predicates := s.primaryReason(*e, sess, slots, s.paneRec[sess.SID], now)
	out.Reason = reason
	out.Predicates = predicates
	out.Candidates = freeNames(slots)
	s.why[sess.SID] = &whyRec{reason: reason, candidates: out.Candidates, predicates: predicates, ts: now}
	return out, nil
}

// Why — ответ why API (приложение А ТЗ).
type Why struct {
	SID        string            `json:"sid"`
	State      model.SessionState `json:"state"`
	Reason     string            `json:"reason,omitempty"`
	Predicates map[string]any    `json:"predicates,omitempty"`
	Candidates []string          `json:"candidates"`
}

// whyFresh — свежесть кэша why: один тик + запас.
func (s *Scheduler) whyFresh() time.Duration {
	return time.Duration(s.cfg.Scheduler.TickMS)*time.Millisecond + time.Second
}
