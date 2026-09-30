package scheduler

import (
	"time"

	"runpilot/internal/model"
	"runpilot/internal/store"
)

// eff — эффективный класс записи (раздел 5 ТЗ):
// eff = max(HIGH, class − floor(wait / aging_sec)); RESUME старением
// не достигается (не участвует в старении).
func (s *Scheduler) eff(e model.QueueEntry, now time.Time) model.QueueClass {
	if e.Class == model.ClassResume {
		return model.ClassResume
	}
	wait := now.Sub(e.EnqueuedAt)
	aging := time.Duration(s.cfg.Scheduler.AgingSec) * time.Second
	if aging <= 0 {
		return e.Class
	}
	steps := int(wait / aging)
	eff := int(e.Class) - steps
	if eff < int(model.ClassHigh) {
		eff = int(model.ClassHigh)
	}
	if eff > int(model.ClassLow) {
		eff = int(model.ClassLow)
	}
	return model.QueueClass(eff)
}

// rankLess — ранг = (eff, enqueued_at); SID — детерминированный
// тай-брейк (однородная очередь).
func (s *Scheduler) rankLess(a, b model.QueueEntry, now time.Time) bool {
	ea, eb := s.eff(a, now), s.eff(b, now)
	if ea != eb {
		return ea < eb
	}
	if !a.EnqueuedAt.Equal(b.EnqueuedAt) {
		return a.EnqueuedAt.Before(b.EnqueuedAt)
	}
	return a.SID < b.SID
}

// viewOf — сервер по имени (nil, если нет в конфигурации).
func (s *Scheduler) viewOf(name string) (ServerView, bool) {
	for _, v := range s.src.List() {
		if v.Name == name {
			return v, true
		}
	}
	return ServerView{}, false
}

// accepts — сервер принимает ли эффективный класс (servers[].accept).
func accepts(v ServerView, c model.QueueClass) bool {
	for _, name := range v.Accept {
		if cl, ok := model.QueueClassParse(name); ok && cl == c {
			return true
		}
	}
	return false
}

// eligible — допустимость записи e для сервера sv (раздел 5 ТЗ, все 7
// условий одновременно). pr — снимок панели (может быть nil —
// снимка нет). Для held_request пункты 3–4 не проверяются, пауза не
// действует.
func (s *Scheduler) eligible(e model.QueueEntry, sess store.SessionRecord,
	sv ServerView, pr *paneRec, now time.Time) bool {
	eff := s.eff(e, now)

	// 1. not_before.
	if now.Before(e.NotBefore) {
		return false
	}

	// 2. after_sid: ход с исходом OK, завершившийся после enqueued_at.
	if e.AfterSID != "" {
		ok, err := s.st.TurnOKSince(e.AfterSID, e.EnqueuedAt)
		if err != nil || !ok {
			return false
		}
	}

	// W7 (6.2/6.4): EMERGENCY — новых аренд нет (никаких, вкл. неявные);
	// SAFE_MODE — новых нет (идущие обслуживаются шлюзом). Вызывается с mu.
	if s.grantsBlocked() {
		return false
	}

	// W7 (8.1): JOB — потолок одновременных аренд (jobs.slots, CONTROL 6).
	if isJob(sess) && s.jobSlotsFull() {
		return false
	}

	// Пауза: submit/resume без held_request не выдаются.
	if s.paused && !e.HeldRequest {
		return false
	}

	// 3–4. Свежесть и стабильность панели (для held_request и JOB — нет:
	// JOB не имеет tmux-панели).
	if !e.HeldRequest && !isJob(sess) {
		if pr == nil || now.Sub(pr.receivedAt) > s.snapshotMaxAge() {
			return false
		}
		if pr.state == model.PaneWaitUI {
			return false
		}
		if pr.state != model.PaneIdle {
			return false
		}
		if pr.paneID == "" || now.Sub(pr.hashSince) < s.dispatchStable() {
			return false
		}
	}

	// 5. Ограничения pin/prefer.
	switch e.Constraint.Kind {
	case model.ConstraintPin:
		if sv.Name != e.Constraint.Server {
			return false
		}
		pv, ok := s.viewOf(e.Constraint.Server)
		if !ok || pv.State != model.ServerUp {
			return false
		}
	case model.ConstraintPrefer:
		if sv.Name != e.Constraint.Server {
			pv, ok := s.viewOf(e.Constraint.Server)
			// prefer: s = X, либо ожидание ≥ prefer_wait_sec, либо
			// X в DOWN/DRAINING.
			if ok && pv.State == model.ServerUp &&
				now.Sub(e.EnqueuedAt) < s.preferWait() {
				return false
			}
		}
	}

	// 6. accept.
	if !accepts(sv, eff) {
		return false
	}

	// 7. Узел сессии не в drain.
	if s.nodeDrain[sess.Host] {
		return false
	}
	return true
}

// --- primaryReason: наиболее конкретная причина (приложение А) ---

// freeNames — имена серверов со свободными (не LEASED) слотами.
func freeNames(slots []slotRec) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, f := range slots {
		if !f.taken && !seen[f.Name] {
			seen[f.Name] = true
			out = append(out, f.Name)
		}
	}
	return out
}

// primaryReason — причина пропуска записи e при текущем наборе
// свободных слотов (наиболее конкретная из приложения А).
func (s *Scheduler) primaryReason(e model.QueueEntry, sess store.SessionRecord,
	slots []slotRec, pr *paneRec, now time.Time) (string, map[string]any) {
	eff := s.eff(e, now)
	views := s.src.List()
	anyUp := false
	for _, v := range views {
		if v.State == model.ServerUp {
			anyUp = true
			break
		}
	}
	free := freeNames(slots)

	// pin: сервер DOWN/DRAINING — pin_down; UP, но слотов нет —
	// pin_mismatch.
	if e.Constraint.Kind == model.ConstraintPin {
		pv, ok := s.viewOf(e.Constraint.Server)
		if ok && pv.State != model.ServerUp {
			remain := time.Duration(0)
			if since, ok := s.downSince[e.Constraint.Server]; ok {
				if rem := s.pinUnavailable() - now.Sub(since); rem > 0 {
					remain = rem
				}
			}
			return model.ReasonPinDown, map[string]any{
				"pin": e.Constraint.Server, "state": string(pv.State),
				"pin_unavailable_in_sec": int(remain.Seconds()),
			}
		}
		if !ok || !contains(free, e.Constraint.Server) {
			return model.ReasonPinMismatch, map[string]any{
				"pin": e.Constraint.Server, "free_servers": free,
			}
		}
	}

	// prefer: ждём prefer-сервер (UP, занят, срок не вышел).
	if e.Constraint.Kind == model.ConstraintPrefer {
		if !contains(free, e.Constraint.Server) {
			pv, ok := s.viewOf(e.Constraint.Server)
			if ok && pv.State == model.ServerUp &&
				now.Sub(e.EnqueuedAt) < s.preferWait() {
				remain := s.preferWait() - now.Sub(e.EnqueuedAt)
				return model.ReasonPreferWait, map[string]any{
					"prefer": e.Constraint.Server,
					"wait_remaining_sec": int(remain.Seconds()),
				}
			}
		}
	}

	// node_drain.
	if s.nodeDrain[sess.Host] {
		return model.ReasonNodeDrain, map[string]any{"node": sess.Host}
	}

	// not_before.
	if now.Before(e.NotBefore) {
		return model.ReasonNotBefore, map[string]any{
			"not_before": e.NotBefore.UTC().Format(time.RFC3339),
			"in_sec":     int(e.NotBefore.Sub(now).Seconds()),
		}
	}

	// after_wait.
	if e.AfterSID != "" {
		ok, _ := s.st.TurnOKSince(e.AfterSID, e.EnqueuedAt)
		if !ok {
			return model.ReasonAfterWait, map[string]any{"after": e.AfterSID}
		}
	}

	// pause.
	if s.paused && !e.HeldRequest {
		return model.ReasonPause, map[string]any{}
	}

	// JOB: потолок одновременных аренд (раздел 8.1, jobs.slots).
	if isJob(sess) && s.jobSlotsFull() {
		return model.ReasonJobSlots, map[string]any{"jobs_slots": s.cfg.Jobs.Slots}
	}

	// Панель (для held_request и JOB — пропуск).
	if !e.HeldRequest && !isJob(sess) {
		if pr == nil {
			return model.ReasonStaleSnapshot, map[string]any{"reason": "нет снимка панели"}
		}
		age := now.Sub(pr.receivedAt)
		if age > s.snapshotMaxAge() {
			return model.ReasonStaleSnapshot, map[string]any{
				"received_at": pr.receivedAt.UTC().Format(time.RFC3339),
				"age_sec":     int(age.Seconds()),
			}
		}
		if pr.state == model.PaneWaitUI {
			return model.ReasonWaitUI, map[string]any{"pane_state": "WAIT_UI"}
		}
		if pr.state != model.PaneIdle {
			return model.ReasonNotIdle, map[string]any{"pane_state": string(pr.state)}
		}
		if pr.paneID == "" || now.Sub(pr.hashSince) < s.dispatchStable() {
			return model.ReasonOperatorTyping, map[string]any{
				"stable_for_sec": int(now.Sub(pr.hashSince).Seconds()),
				"need_sec":       s.cfg.Scheduler.DispatchStableSec,
			}
		}
	}

	// accept: eff не входит в accept свободных серверов.
	acceptAny := false
	for _, f := range slots {
		if f.taken {
			continue
		}
		if v, ok := s.viewOf(f.Name); ok && accepts(v, eff) {
			acceptAny = true
			break
		}
	}
	if !acceptAny && len(free) > 0 {
		return model.ReasonAccept, map[string]any{"eff": eff.String()}
	}

	// cooldown / external: подходящие серверы UP, но слоты в
	// COOLDOWN или EXTERNAL.
	cooldownAny, externalAny := false, false
	for _, v := range views {
		if v.State != model.ServerUp {
			continue
		}
		if !accepts(v, eff) {
			continue
		}
		if e.Constraint.Kind == model.ConstraintPin && v.Name != e.Constraint.Server {
			continue
		}
		leased := s.leasedSlots(v.Name)
		for slot := 1; slot <= v.Slots; slot++ {
			if leased[slot] {
				continue
			}
			if until, ok := s.cooldown[v.Name][slot]; ok && now.Before(until) {
				cooldownAny = true
			}
			if slot <= s.externalSlots(v.Name) {
				externalAny = true
			}
		}
	}
	if cooldownAny {
		return model.ReasonCooldown, map[string]any{}
	}
	if externalAny {
		return model.ReasonExternal, map[string]any{"external": s.externalSlotsAny()}
	}

	// Нет ни одного свободного слота (все LEASED) либо нет UP-сервера.
	if !anyUp {
		return model.ReasonNoUpServer, serverStates(views)
	}
	// Интерпретация (зафиксирована в CONTROL Э4): все слоты заняты
	// LEASED (серверы UP) — ближайший код приложения А no_up_server
	// («сервера, способного отдать слот, нет»); более специфичные
	// коды (pin_mismatch, accept, cooldown, external) выше.
	return model.ReasonNoUpServer, serverStates(views)
}

// recordSkip — пропуск: ineligible_reason в БД и QUEUE_SKIP при смене.
func (s *Scheduler) recordSkip(e model.QueueEntry, sess store.SessionRecord,
	slots []slotRec, pr *paneRec, now time.Time) {
	reason, predicates := s.primaryReason(e, sess, slots, pr, now)
	candidates := freeNames(slots)
	s.why[e.SID] = &whyRec{
		reason:     reason,
		candidates: candidates,
		predicates: predicates,
		ts:         now,
	}
	if e.IneligibleReason == reason {
		return
	}
	updated := e
	updated.IneligibleReason = reason
	updated.IneligibleSince = now
	_ = s.st.QueueUpsert(updated)
	s.event(model.KindQueueSkip, e.SID, "", map[string]any{
		"reason": reason, "prev": e.IneligibleReason,
	})
}

// serverStates — состояния всех серверов (предикат why).
func serverStates(views []ServerView) map[string]any {
	m := map[string]any{}
	for _, v := range views {
		m[v.Name] = string(v.State)
	}
	return m
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// --- задания JOB (раздел 8.1, CONTROL 6) ---

// isJob — сессия вида JOB (runpilot exec): без tmux-панели и проверок панели.
func isJob(sess store.SessionRecord) bool { return sess.Kind == model.KindJob }

// jobSlotsFull — достигнут потолок одновременных JOB-аренд (jobs.slots).
func (s *Scheduler) jobSlotsFull() bool {
	return s.jobUsed >= s.cfg.Jobs.Slots
}

// activeJobLeases — число активных JOB-аренд (из БД; вызывается в
// начале tickGrant, один раз за тик).
func (s *Scheduler) activeJobLeases() int {
	leases, err := s.st.LeaseListActive()
	if err != nil {
		return 0
	}
	n := 0
	for _, l := range leases {
		if sess, err := s.st.GetSession(l.SID); err == nil && sess.Kind == model.KindJob {
			n++
		}
	}
	return n
}

// --- конфиг-доступы (литералы времени — только через config) ---

func (s *Scheduler) snapshotMaxAge() time.Duration {
	return time.Duration(s.cfg.Scheduler.SnapshotMaxAgeSec) * time.Second
}

func (s *Scheduler) dispatchStable() time.Duration {
	return time.Duration(s.cfg.Scheduler.DispatchStableSec) * time.Second
}

func (s *Scheduler) preferWait() time.Duration {
	return time.Duration(s.cfg.Scheduler.PreferWaitSec) * time.Second
}

func (s *Scheduler) pinUnavailable() time.Duration {
	return time.Duration(s.cfg.Scheduler.PinUnavailableSec) * time.Second
}

func (s *Scheduler) affinityTTL() time.Duration {
	return time.Duration(s.cfg.Scheduler.AffinityTTLSec) * time.Second
}

func (s *Scheduler) resumeBackoff() time.Duration {
	return time.Duration(s.cfg.Scheduler.ResumeBackoffSec) * time.Second
}

func (s *Scheduler) externalConfirm() time.Duration {
	return time.Duration(s.cfg.Scheduler.ExternalConfirmSec) * time.Second
}

func (s *Scheduler) cooldownDur() time.Duration {
	return time.Duration(s.cfg.Scheduler.CooldownSec) * time.Second
}

func (s *Scheduler) doneQuiet() time.Duration {
	return time.Duration(s.cfg.Turn.DoneQuietSec) * time.Second
}

func (s *Scheduler) doneStable() time.Duration {
	return time.Duration(s.cfg.Turn.DoneStableSec) * time.Second
}

func (s *Scheduler) toolHoldMax() time.Duration {
	return time.Duration(s.cfg.Turn.ToolHoldMaxSec) * time.Second
}

func (s *Scheduler) approvalHoldMax() time.Duration {
	return time.Duration(s.cfg.Turn.ApprovalHoldMaxSec) * time.Second
}

func (s *Scheduler) unknownMax() time.Duration {
	return time.Duration(s.cfg.Turn.UnknownMaxSec) * time.Second
}

func (s *Scheduler) nodeLostRelease() time.Duration {
	return time.Duration(s.cfg.Turn.NodeLostReleaseSec) * time.Second
}

func (s *Scheduler) autoEnqueueStable() time.Duration {
	return time.Duration(s.cfg.Turn.AutoEnqueueStableSec) * time.Second
}

func (s *Scheduler) nodeReplyTimeout() time.Duration {
	return time.Duration(s.cfg.Dispatch.NodeReplyTimeoutSec) * time.Second
}

func (s *Scheduler) startConfirm() time.Duration {
	return time.Duration(s.cfg.Dispatch.StartConfirmSec) * time.Second
}
