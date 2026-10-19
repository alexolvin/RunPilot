package scheduler

import (
	"net/http"
	"time"

	"runpilot/internal/model"
	"runpilot/internal/store"
)

// tickComplete — завершение хода и правила снятия аренды посреди хода
// (раздел 8 ТЗ).
func (s *Scheduler) tickComplete(now time.Time) {
	sessions := s.sessionsMap()
	for _, sess := range sessions {
		// JOB (runpilot exec) завершается явно через /finish, а не по
		// детекции IDLE-панели — хода без панели нет (раздел 8.1).
		if isJob(sess) {
			continue
		}
		// Завершение хода — к любой ACTIVE-аренде (RUNNING, DETACHED, IDLE с
		// неявной арендой). DISPATCHING (аренда PENDING) ход НЕ завершает —
		// только путями подтверждения старта (см. tickConfirm).
		lease, err := s.st.LeaseGet(sess.SID)
		if err != nil || lease.State != model.LeaseActive {
			continue
		}
		s.checkTurn(sess, now)
	}
}

// checkTurn — завершение хода (RUNNING и DETACHED) либо снятие аренды
// посреди хода (только RUNNING). Правило завершения — раздел 8 ТЗ.
func (s *Scheduler) checkTurn(sess store.SessionRecord, now time.Time) {
	pr := s.paneRec[sess.SID]
	inflight := s.inflight[sess.SID]
	lastEnd, haveEnd := s.lastReqEnd[sess.SID]

	// Ход без строки (страховка) — создать.
	turn, err := s.st.OpenTurn(sess.SID)
	if err != nil {
		if _, err2 := s.st.TurnStart(sess.SID, now); err2 != nil {
			s.log.Error("scheduler: ход (страховка)", "sid", sess.SID, "err", err2)
		}
		turn, _ = s.st.OpenTurn(sess.SID)
	}
	if turn == nil {
		return
	}

	// --- Снятие аренды посреди хода (к любой ACTIVE-аренде; раздел 8, таблица) ---
	{
		// Панель BUSY, запросов нет tool_hold_max_sec → TOOL_TIMEOUT.
		if pr != nil && pr.state == model.PaneBusy && inflight == 0 {
			since := time.Time{}
			if haveEnd && lastEnd.After(since) {
				since = lastEnd
			}
			if pr.stateSince.After(since) {
				since = pr.stateSince
			}
			if !since.IsZero() && now.Sub(since) >= s.toolHoldMax() {
				s.releaseLeaseLocked(sess.SID, model.ReleaseToolTimeout, now)
				s.sessionEvent(sess, model.EvLeaseRevoked, "")
				return
			}
		}

		// Панель PROMPT approval_hold_max_sec → APPROVAL_TIMEOUT
		// (уведомление — сразу при входе в PROMPT, в PaneUpdate).
		if pr != nil && pr.state == model.PanePrompt {
			if now.Sub(pr.stateSince) >= s.approvalHoldMax() {
				s.releaseLeaseLocked(sess.SID, model.ReleaseApprovalTimeout, now)
				s.sessionEvent(sess, model.EvLeaseRevoked, "")
				return
			}
		}

		// Панель UNKNOWN unknown_max_sec, inflight = 0 → PANE_UNKNOWN.
		// Ход НЕ закрывается: строка turn остаётся открытой.
		if pr != nil && pr.state == model.PaneUnknown && inflight == 0 {
			if now.Sub(pr.stateSince) >= s.unknownMax() {
				s.releaseLeaseLocked(sess.SID, model.ReleasePaneUnknown, now)
				s.sessionEvent(sess, model.EvErrorFatal, model.HoldPaneUnknown)
				return
			}
		}
	}

	// --- Завершение хода (RUNNING и DETACHED; раздел 8 ТЗ) ---
	// Все условия одновременно:
	//  1. панель IDLE;
	//  2. панель стабильна с момента СТАРТА хода (stable_since ≥ started_at,
	//     а не «стабильна до хода» — это гасит фантомное закрытие после долгого
	//     ожидания в очереди) и ≥ done_stable_sec;
	//  3. тишина: last_request_end только из запросов текущего хода; ход без
	//     запросов — тишина от turn.started_at (≥ done_quiet_sec).
	if !s.turnDone(sess, pr, inflight, lastEnd, haveEnd, turn, now) {
		return
	}
	s.closeTurn(sess, turn, now)
}

// turnDone — условие завершения хода (раздел 8 ТЗ). Возвращает false, пока
// ход не завершён. Панель — IDLE. Общее условие (ко ВСЕМ ходам):
//   - анти-фантом: панель стабильна именно ПОСЛЕ старта хода
//     (stable_since ≥ turn.started_at — иначе это «стабильность до хода»,
//     т.е. фантом после долгого ожидания в очереди / кодер не отработал) и
//     стабильность ≥ done_stable_sec.
// Плюс тишина (зависит от запросов хода):
//   - ход С запросами: тишина от last_request_end (запроса текущего хода) ≥
//     done_quiet_sec;
//   - ход БЕЗ запросов: тишина от turn.started_at ≥ done_quiet_sec.
func (s *Scheduler) turnDone(sess store.SessionRecord, pr *paneRec, inflight int,
	lastEnd time.Time, haveEnd bool, turn *model.Turn, now time.Time) bool {
	if pr == nil || pr.state != model.PaneIdle {
		return false
	}
	if inflight != 0 || pr.paneID == "" {
		return false
	}
	// Анти-фантом (ко всем ходам): хеш панели должен измениться ПОСЛЕ старта
	// хода (stable_since ≥ turn.started_at), а не быть стабильным до него.
	if pr.hashSince.Before(turn.StartedAt) {
		return false
	}
	if now.Sub(pr.hashSince) < s.doneStable() {
		return false
	}
	if s.turnRequests(turn.StartedAt, sess.SID) > 0 {
		// Ход с запросами: тишина от last_request_end (запроса хода).
		if !haveEnd || now.Sub(lastEnd) < s.doneQuiet() {
			return false
		}
		return true
	}
	// Ход без запросов: тишина от started_at.
	return now.Sub(turn.StartedAt) >= s.doneQuiet()
}

// closeTurn — завершение хода (раздел 8 ТЗ): исход по последнему
// генерирующему запросу, аренда RELEASED с TURN_DONE, слот → COOLDOWN,
// строка turn закрывается, сессии пишутся last_server/last_turn_end.
func (s *Scheduler) closeTurn(sess store.SessionRecord, turn *model.Turn, now time.Time) {
	// Аренда до освобождения (сервер/слот для COOLDOWN).
	lease, _ := s.st.LeaseGet(sess.SID)

	last, err := s.st.LastGenerative(sess.SID, turn.StartedAt)
	result := model.ResultOK
	if err == nil && last != nil {
		switch {
		case last.Status >= http.StatusOK && last.Status < http.StatusMultipleChoices:
			result = model.ResultOK
		case last.Status >= http.StatusBadRequest && last.Status < http.StatusInternalServerError:
			result = model.ResultErrorClient
		case last.Status >= http.StatusInternalServerError || last.Status == 0:
			// 5xx, runpilot-отсутствие слота, ошибка соединения,
			// оборванный поток (status 0).
			result = model.ResultErrorTransient
		default:
			result = model.ResultErrorTransient
		}
	}
	reqCount, _ := s.st.CountRequestsSince(sess.SID, turn.StartedAt)
	serversCSV, _ := s.st.ServersSince(sess.SID, turn.StartedAt)

	var outcome model.TurnOutcome
	switch result {
	case model.ResultErrorTransient:
		outcome = model.TurnError
	case model.ResultErrorClient:
		outcome = model.TurnError
	default:
		outcome = model.TurnOK
	}
	_ = s.st.TurnClose(turn.ID, now, outcome, reqCount, serversCSV)

	s.releaseLeaseLocked(sess.SID, model.ReleaseTurnDone, now)
	if lease != nil {
		s.setCooldown(lease.Server, lease.Slot, now)
	}
	_ = s.st.SetSessionLastTurnEnd(sess.SID, now)
	_ = s.st.SetSessionLastMigratedFromEmpty(sess.SID)

	// S11: предварительная постановка RUNNING — enqueued_at переписывается
	// на время конца хода (общий QueueUpsert: перезапись).
	if e, err := s.st.QueueGet(sess.SID); err == nil {
		if !e.EnqueuedAt.IsZero() && !e.EnqueuedAt.Before(turn.StartedAt) {
			e.EnqueuedAt = now
			_ = s.st.QueueUpsert(*e)
		}
	}

	s.event(model.KindTurnEnd, sess.SID, "", map[string]any{
		"turn_id": turn.ID, "outcome": string(outcome),
		"requests": reqCount, "servers": serversCSV,
	})
	s.wake()

	switch result {
	case model.ResultOK:
		_ = s.st.SetSessionAttempts(sess.SID, 0)
		// RUNNING → IDLE (turn_done); DETACHED → IDLE (idle_no_error): из
		// DETACHED нет перехода по EvTurnDone (автомат, раздел 4 ТЗ).
		// IDLE (неявная аренда, ход закрыт) — сессия уже IDLE, переход не нужен.
		switch sess.State {
		case model.SessionDetached:
			s.sessionEvent(sess, model.EvIdleNoError, "")
		case model.SessionRunning:
			s.sessionEvent(sess, model.EvTurnDone, "")
		}
	case model.ResultErrorTransient:
		s.requeueOrHold(sess, now)
	case model.ResultErrorClient:
		// HOLD(UPSTREAM_4XX) + уведомление, без перепостановки.
		s.sessionEvent(sess, model.EvErrorFatal, model.HoldUpstream4XX)
		s.notifyf("runpilot: %s: апстрим вернул 4xx, ход остановлен (UPSTREAM_4XX)", sess.Name)
	}
}

// requeueOrHold — автоперепостановка после ERROR_TRANSIENT
// (раздел 9 ТЗ): attempts += 1; ≤ auto_requeue_max → RESUME-запись с
// not_before = now + resume_backoff_sec × attempts; иначе HOLD(REQUEUE_LIMIT).
func (s *Scheduler) requeueOrHold(sess store.SessionRecord, now time.Time) {
	attempts := sess.Attempts + 1
	_ = s.st.SetSessionAttempts(sess.SID, attempts)
	if attempts <= s.cfg.Scheduler.AutoRequeueMax {
		entry := model.QueueEntry{
			SID:         sess.SID,
			Class:       model.ClassResume,
			EnqueuedAt:  now,
			NotBefore:   now.Add(time.Duration(attempts) * s.resumeBackoff()),
			Constraint:  model.Constraint{Kind: sess.ConstraintKind, Server: sess.ConstraintServer},
			Mode:        model.QueueModeResume,
		}
		_ = s.st.QueueUpsert(entry)
		s.event(model.KindQueueEnqueue, sess.SID, "", map[string]any{
			"auto": true, "attempts": attempts,
		})
		s.sessionEvent(sess, model.EvErrorRetryable, "")
		return
	}
	s.sessionEvent(sess, model.EvErrorFatal, model.HoldRequeueLimit)
	s.notifyf("runpilot: %s: лимит автоперепостановок исчерпан (REQUEUE_LIMIT)", sess.Name)
}

// releaseLeaseLocked — снять аренду + событие + LRU-пометка слота.
func (s *Scheduler) releaseLeaseLocked(sid, reason string, now time.Time) {
	lease, err := s.st.LeaseGet(sid)
	if err == nil {
		_ = s.st.LeaseRelease(sid, reason, now)
		s.slotFree(lease.Server, lease.Slot, now)
		s.eventAt(now, model.KindLeaseRelease, sid, lease.Server, map[string]any{
			"reason": reason, "server": lease.Server, "slot": lease.Slot,
		})
		s.wake()
	}
}

// setCooldown — слот → COOLDOWN на scheduler.cooldown_sec (раздел 8 ТЗ).
//
// Граница тика (фикс): окончание COOLDOWN — событие планировщика. При
// освобождении (at) ставится таймер internal/clock на at+cooldown_sec; его
// срабатывание запускает Tick, как любое другое событие. Тогда тик
// (tickCooldown→slotFree, tickGrant→выдача) происходит ровно в
// at+cooldown_sec, а не на следующем периодическом тике (раньше задержка
// выдачи равнялась cooldown + до tick_ms). Периодический тик — сеть
// безопасности (идемпотентен: LeaseCreateIfAbsent + запись уже снята).
// Один now на тик сохраняется (выдача берёт now этого тика).
func (s *Scheduler) setCooldown(server string, slot int, at time.Time) {
	if s.cooldown[server] == nil {
		s.cooldown[server] = map[int]time.Time{}
	}
	s.cooldown[server][slot] = at.Add(s.cooldownDur())
	s.clk.AfterFunc(s.cooldownDur(), func() {
		if s.closed.Load() {
			return
		}
		s.Tick()
	})
}

// tickCooldown — истёкшие COOLDOWN-слоты (для LRU-пометки).
func (s *Scheduler) tickCooldown(now time.Time) {
	for server, slots := range s.cooldown {
		for slot, until := range slots {
			if !now.Before(until) {
				delete(s.cooldown[server], slot)
				s.slotFree(server, slot, until)
			}
		}
		if len(slots) == 0 {
			delete(s.cooldown, server)
		}
	}
}

// serverDownLocked — сервер DOWN: аренды RUNNING-сессий без трафика
// снимаются (SERVER_DOWN), сессии → DETACHED (раздел 8 ТЗ).
func (s *Scheduler) serverDownLocked(server string, now time.Time) {
	sessions := s.sessionsMap()
	for _, sess := range sessions {
		if sess.State != model.SessionRunning {
			continue
		}
		lease, err := s.st.LeaseGet(sess.SID)
		if err != nil || lease.Server != server {
			continue
		}
		if s.inflight[sess.SID] != 0 {
			continue // запросы в полёте: исход по последнему запросу
		}
		s.releaseLeaseLocked(sess.SID, model.ReleaseServerDown, now)
		s.sessionEvent(sess, model.EvLeaseRevoked, "")
	}
}

// tickNodeLost — узел отключился без трафика turn.node_lost_release_sec:
// аренды его RUNNING-сессий снимаются (NODE_LOST), сессии → HOLD (раздел 8 ТЗ).
// NodeSeen сбрасывает таймер (узел снова отвечает) — см. NodeLost/NodeSeen.
func (s *Scheduler) tickNodeLost(now time.Time) {
	var lost []string
	for host, down := range s.nodeDown {
		if now.Sub(down) >= s.nodeLostRelease() {
			lost = append(lost, host)
		}
	}
	if len(lost) == 0 {
		return
	}
	lostSet := make(map[string]bool, len(lost))
	for _, host := range lost {
		lostSet[host] = true
	}
	sessions := s.sessionsMap()
	for _, sess := range sessions {
		if sess.State != model.SessionRunning || !lostSet[sess.NodeID()] {
			continue
		}
		if s.inflight[sess.SID] != 0 {
			continue // запросы в полёте: узел ещё отвечает
		}
		s.releaseLeaseLocked(sess.SID, model.ReleaseNodeLost, now)
		s.sessionEvent(sess, model.EvErrorFatal, model.HoldNodeLost)
		s.notifyf("runpilot: %s: узел %s не отвечает (NODE_LOST)", sess.Name, sess.NodeID())
	}
}

// tickJobStale — X5 (раздел 8.1): клиент runpilot exec пропал — нет пульса
// jobs.heartbeat_timeout_sec. Аренда снимается (CLIENT_LOST), исход LOST,
// без перепостановки (повтор — дело cron). Вызывается из Tick (s.mu держится).
func (s *Scheduler) tickJobStale(now time.Time) {
	if s.cfg.Jobs.HeartbeatTimeoutSec <= 0 {
		return
	}
	cutoff := now.Add(-time.Duration(s.cfg.Jobs.HeartbeatTimeoutSec) * time.Second)
	sids, err := s.st.JobStale(cutoff)
	if err != nil || len(sids) == 0 {
		return
	}
	for _, sid := range sids {
		sess, err := s.st.GetSession(sid)
		if err != nil || !isJob(sess) || sess.State != model.SessionRunning {
			continue
		}
		if _, err := s.st.LeaseGet(sid); err != nil {
			continue // аренды нет — уже снята
		}
		s.releaseLeaseLocked(sid, model.ReleaseClientLost, now)
		_ = s.st.JobDelete(sid)
		delete(s.inflight, sid)
		s.event(model.KindJobState, sid, "", map[string]any{"outcome": "LOST"})
		s.sessionEvent(sess, model.EvPaneGone, "") // RUNNING → GONE
		s.notifyf("runpilot: задание %s прервано: клиент пропал (CLIENT_LOST)", sess.Name)
	}
}

// releaseAndGoneLocked — панель исчезла: аренда, запись очереди,
// сессия → GONE (раздел 8 ТЗ: слот свободен).
func (s *Scheduler) releaseAndGoneLocked(sid string, now time.Time) {
	if sid == "" {
		return
	}
	sess, err := s.st.GetSession(sid)
	if err != nil {
		delete(s.paneRec, sid)
		return
	}
	if sess.State == model.SessionGone {
		return
	}
	s.releaseLeaseLocked(sid, model.ReleasePaneGone, now)
	_ = s.st.QueueDelete(sid)
	s.goneLocked(sess, now)
}

// goneLocked — сессия → GONE + очистка.
func (s *Scheduler) goneLocked(sess store.SessionRecord, now time.Time) {
	s.sessionEvent(sess, model.EvPaneGone, "")
	delete(s.paneRec, sess.SID)
	delete(s.dispatched, sess.SID)
	delete(s.confirm, sess.SID)
	delete(s.why, sess.SID)
	delete(s.inflight, sess.SID)
	delete(s.autoSince, sess.SID)
}

// tickDependencyAndPin — переходы из очереди, не зависящие от выдачи:
//   - --after: зависимость в HOLD/GONE → HOLD(DEPENDENCY_FAILED);
//   - pin: сервер DOWN/DRAINING дольше pin_unavailable_sec →
//     HOLD(PIN_UNAVAILABLE) + уведомление (не вечный пропуск).
func (s *Scheduler) tickDependencyAndPin(now time.Time) {
	entries, err := s.st.QueueList()
	if err != nil {
		return
	}
	sessions := s.sessionsMap()
	for _, e := range entries {
		sess, ok := sessions[e.SID]
		if !ok || sess.State != model.SessionQueued {
			continue
		}
		if e.AfterSID != "" {
			if dep, err := s.st.GetSession(e.AfterSID); err == nil &&
				(dep.State == model.SessionHold || dep.State == model.SessionGone) {
				_ = s.st.QueueDelete(e.SID)
				s.sessionEvent(sess, model.EvDependencyFailed, model.HoldDependencyFailed)
				s.notifyf("runpilot: %s: зависимость %s недоступна (DEPENDENCY_FAILED)", sess.Name, e.AfterSID)
				continue
			}
		}
		if e.Constraint.Kind == model.ConstraintPin {
			v, ok := s.viewOf(e.Constraint.Server)
			if ok && v.State != model.ServerUp {
				if since, ok := s.downSince[e.Constraint.Server]; ok &&
					now.Sub(since) >= s.pinUnavailable() {
					_ = s.st.QueueDelete(e.SID)
					s.sessionEvent(sess, model.EvPinUnavailable, model.HoldPinUnavailable)
					s.notifyf("runpilot: %s: сервер %s недоступен дольше %d с (PIN_UNAVAILABLE)",
						sess.Name, e.Constraint.Server, s.cfg.Scheduler.PinUnavailableSec)
				}
			}
		}
	}
}

// tickQueueWait — C14: запись в очереди дольше notify.alerts.queue_wait_min →
// уведомление «Ждёт N мин: why.text» (раз по постановке). Вызывается из Tick
// (s.mu держится).
func (s *Scheduler) tickQueueWait(now time.Time) {
	lim := s.cfg.Notify.Alerts.QueueWaitMin
	if lim <= 0 {
		return
	}
	threshold := time.Duration(lim) * time.Minute
	entries, err := s.st.QueueList()
	if err != nil {
		return
	}
	sessions := s.sessionsMap()
	for _, e := range entries {
		sess, ok := sessions[e.SID]
		if !ok || sess.State != model.SessionQueued {
			delete(s.queueWaitNotified, e.SID)
			continue
		}
		waited := now.Sub(e.EnqueuedAt)
		if waited < threshold {
			delete(s.queueWaitNotified, e.SID)
			continue
		}
		// Уже уведомляли за эту постановку (enqueued_at совпадает).
		if at, done := s.queueWaitNotified[e.SID]; done && at.Equal(e.EnqueuedAt) {
			continue
		}
		why := e.IneligibleReason
		if why == "" {
			why = "нет причины"
		}
		s.notifyf("runpilot: %s: ждёт в очереди %d мин: %s", sess.Name, int(waited/(secPerMinute*time.Second)), why)
		s.queueWaitNotified[e.SID] = e.EnqueuedAt
	}
}

// tickAutoEnqueue — автопостановка нового задания (раздел 9 ТЗ):
// сессия IDLE, панель IDLE, ввод не пуст, хеш не меняется
// turn.auto_enqueue_stable_sec.
func (s *Scheduler) tickAutoEnqueue(now time.Time) {
	sessions := s.sessionsMap()
	for _, sess := range sessions {
		if !sess.AutoEnqueue || sess.State != model.SessionIdle {
			continue
		}
		pr := s.paneRec[sess.SID]
		if pr == nil || pr.state != model.PaneIdle || pr.paneID == "" {
			delete(s.autoSince, sess.SID)
			continue
		}
		snap, snapOK := s.panes.PaneBySID(sess.SID)
		if !snapOK || snap.InputEmpty {
			delete(s.autoSince, sess.SID)
			continue
		}
		if since, ok := s.autoSince[sess.SID]; !ok {
			s.autoSince[sess.SID] = now
			continue
		} else if now.Sub(since) < s.autoEnqueueStable() {
			continue
		}
		delete(s.autoSince, sess.SID)
		if err := s.enqueueLocked(sess.SID, model.QueueEntry{
			Class:      sess.Class,
			Constraint: model.Constraint{Kind: sess.ConstraintKind, Server: sess.ConstraintServer},
		}, false); err != nil {
			s.log.Warn("autoenqueue: Enqueue ошибка", "sid", sess.SID, "err", err.Error())
		}
	}
}

// turnRequests — число запросов хода с момента его старта.
func (s *Scheduler) turnRequests(since time.Time, sid string) int {
	n, err := s.st.CountRequestsSince(sid, since)
	if err != nil {
		return 0
	}
	return n
}
