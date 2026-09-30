package scheduler

import (
	"context"
	"sort"
	"time"

	"runpilot/internal/model"
	"runpilot/internal/proto"
	"runpilot/internal/store"
)

// tickGrant — основной цикл тика (раздел 5 ТЗ, псевдокод):
//
//	free := freeSlots()
//	for e in queue.sortedByRank():
//	    cand := [s in free if eligible(e, s)]
//	    if len(cand) == 0 { e.ineligible_reason = primaryReason(e, free); continue }
//	    s := choose(e, cand)
//	    grant(e, s)
//	    free.take(s)
//	    if free.empty(): break
// grantable — запись рассматривается в тике выдачи: плановая — только
// QUEUED; held_request (неявная аренда) — IDLE/DETACHED/QUEUED.
func grantable(e model.QueueEntry, sess store.SessionRecord) bool {
	if e.HeldRequest {
		switch sess.State {
		case model.SessionIdle, model.SessionDetached, model.SessionQueued:
			return true
		}
		return false
	}
	return sess.State == model.SessionQueued
}

func (s *Scheduler) tickGrant(now time.Time) {
	entries, err := s.st.QueueList()
	if err != nil || len(entries) == 0 {
		return
	}
	// Раздел 8.1: число активных JOB-аренд (потолок jobs.slots).
	s.jobUsed = s.activeJobLeases()
	sessions := s.sessionsMap()
	sort.SliceStable(entries, func(i, j int) bool {
		return s.rankLess(entries[i], entries[j], now)
	})

	free := s.freeSlots(now)
	if !anyFreeSlot(free) {
		// Свободных слотов нет: пишем причины пропуска, выдача невозможна.
		for _, e := range entries {
			sess, ok := sessions[e.SID]
			if !ok || !grantable(e, sess) {
				continue
			}
			s.recordSkip(e, sess, free, s.paneRec[e.SID], now)
		}
		return
	}

	for _, e := range entries {
		sess, ok := sessions[e.SID]
		if !ok || !grantable(e, sess) {
			continue
		}
		// Кандидаты: свободные слоты, сервер допустим для записи.
		var cand []slotRec
		for _, f := range free {
			if f.taken {
				continue
			}
			v, ok := s.viewOf(f.Name)
			if !ok {
				continue
			}
			if s.eligible(e, sess, v, s.paneRec[e.SID], now) {
				cand = append(cand, f)
			}
		}
		if len(cand) == 0 {
			s.recordSkip(e, sess, free, s.paneRec[e.SID], now)
			continue
		}
		chosen := s.choose(e, sess, cand, now)
		s.grant(e, sess, chosen, now)
		// Помечаем занятый слот в самом списке (копия не сработает).
		for i := range free {
			if free[i].Name == chosen.Name && free[i].Slot == chosen.Slot {
				free[i].taken = true
				break
			}
		}
		// Свободных слотов нет — остальные записи дойдут до cand==0 и
		// получат ineligible_reason (каждый SKIP пишет причину).
	}
}

// anyFreeSlot — есть ли в списке свободный (не занятый в этом тике) слот.
func anyFreeSlot(free []slotRec) bool {
	for _, f := range free {
		if !f.taken {
			return true
		}
	}
	return false
}

// grant — выдача: аренда (PENDING плановая / ACTIVE held), сессия,
// события, dispatch узлу (раздел 8 ТЗ).
func (s *Scheduler) grant(e model.QueueEntry, sess store.SessionRecord, f slotRec, now time.Time) {
	if e.HeldRequest {
		// Удержанный запрос: неявная аренда, без dispatch (Enter уже
		// нажат оператором / ход продолжен).
		l, err := s.st.LeaseCreateIfAbsent(model.Lease{
			SID: sess.SID, Server: f.Name, Slot: f.Slot,
			State: model.LeaseActive, Origin: model.LeaseOriginImplicit,
			GrantedAt: now,
		}, true)
		if err != nil {
			s.log.Error("scheduler: held-аренда", "sid", sess.SID, "err", err)
			return
		}
		// Гонку выиграл другой участник (шлюз): аренду выдал не этот слот.
		if l.Server != f.Name {
			s.wake()
			return
		}
		s.slotLeased(f.Name, f.Slot)
		switch sess.State {
		case model.SessionIdle:
			s.sessionEvent(sess, model.EvEnterHasSlot, "")
		case model.SessionDetached:
			s.sessionEvent(sess, model.EvResumeRequest, "")
		}
		s.event(model.KindLeaseGrant, sess.SID, f.Name, map[string]any{
			"server": f.Name, "slot": l.Slot, "origin": "IMPLICIT", "held": true,
		})
		s.wake()
		delete(s.why, sess.SID)
		return
	}

	// Раздел 8.1: JOB (runpilot exec) — без dispatch узлу: аренда сразу ACTIVE,
	// сессия DISPATCHING → RUNNING (клиент сам ведёт запросы к шлюзу).
	if isJob(sess) {
		l, err := s.st.LeaseCreateIfAbsent(model.Lease{
			SID: sess.SID, Server: f.Name, Slot: f.Slot,
			State: model.LeaseActive, Origin: model.LeaseOriginDispatch,
			GrantedAt: now,
		}, false)
		if err != nil {
			s.log.Error("scheduler: JOB-аренда", "sid", sess.SID, "err", err)
			return
		}
		if l.State != model.LeaseActive {
			s.wake()
			return
		}
		s.slotLeased(f.Name, f.Slot)
		s.jobUsed++
		s.sessionEvent(sess, model.EvLeaseGranted, "") // QUEUED→DISPATCHING
		sess.State = model.SessionDispatching
		s.sessionEvent(sess, model.EvStartConfirmed, "") // DISPATCHING→RUNNING
		// Запись очереди: аренда выдана — она больше не в очереди.
		_ = s.st.QueueDelete(sess.SID)
		s.event(model.KindLeaseGrant, sess.SID, f.Name, map[string]any{
			"server": f.Name, "slot": f.Slot, "origin": "DISPATCH", "job": true,
		})
		s.wake()
		delete(s.why, sess.SID)
		return
	}

	// Плановая выдача: аренда PENDING, сессия DISPATCHING.
	l, err := s.st.LeaseCreateIfAbsent(model.Lease{
		SID: sess.SID, Server: f.Name, Slot: f.Slot,
		State: model.LeasePending, Origin: model.LeaseOriginDispatch,
		GrantedAt: now,
	}, false)
	if err != nil {
		s.log.Error("scheduler: аренда", "sid", sess.SID, "err", err)
		return
	}
	if l.State != model.LeasePending {
		// Аренду уже выдал шлюз (гонка) — не дублируем dispatch.
		return
	}
	s.slotLeased(f.Name, f.Slot)
	s.sessionEvent(sess, model.EvLeaseGranted, "")
	s.event(model.KindLeaseGrant, sess.SID, f.Name, map[string]any{
		"server": f.Name, "slot": f.Slot, "origin": "DISPATCH",
	})

	// Dispatch узлу: снимок панели → expect_hash; resume → resume_text.
	paneID := ""
	expectHash := uint64(0)
	if pr := s.paneRec[sess.SID]; pr != nil {
		paneID = pr.paneID
		expectHash = pr.hash
	}
	mode := string(e.Mode)
	rec := &dispatchRec{
		paneID: paneID, mode: mode, expectHash: expectHash,
		entry: e, grantedAt: now, leaseServer: f.Name,
	}
	s.dispatched[sess.SID] = rec
	s.event(model.KindDispatch, sess.SID, f.Name, map[string]any{
		"pane_id": paneID, "mode": mode, "expect_hash": expectHash,
	})
	delete(s.why, sess.SID)

	msg := proto.New(proto.KindDispatch)
	msg.SID, msg.PaneID, msg.Mode, msg.ExpectHash = sess.SID, paneID, mode, expectHash
	if e.Mode == model.QueueModeResume {
		msg.ResumeText = s.resumeText
	}
	if s.noAsyncDispatch {
		return // тест: reply инжектится через InjectReply
	}
	go s.runDispatch(sess.SID, paneID, msg, now)
}

// runDispatch — команда узлу + ожидание reply (таймаут держим сами:
// dispatch.node_reply_timeout_sec).
func (s *Scheduler) runDispatch(sid, paneID string, msg proto.Msg, grantedAt time.Time) {
	ctx, cancel := context.WithDeadline(context.Background(),
		s.clk.Now().Add(s.nodeReplyTimeout()))
	defer cancel()
	var reply proto.Msg
	var err error
	if paneID != "" && s.dispatch != nil {
		reply, err = s.dispatch(ctx, paneID, msg)
	} else {
		err = errNoPane
	}
	s.onDispatchReply(sid, reply, err, grantedAt)
}

var errNoPane = errString("нет снимка панели")

type errString string

func (e errString) Error() string { return string(e) }

// InjectReply — ответ узла на dispatch (тесты: вместо async-вызова).
func (s *Scheduler) InjectReply(sid string, reply proto.Msg, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.dispatched[sid]
	if !ok {
		return
	}
	s.dispatchReplyLocked(sid, reply, err, rec.grantedAt)
}

// onDispatchReply — обработка ответа узла из async-пути (таблица раздела 8 ТЗ).
func (s *Scheduler) onDispatchReply(sid string, reply proto.Msg, err error, grantedAt time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dispatchReplyLocked(sid, reply, err, grantedAt)
}

// dispatchReplyLocked — тело обработки ответа (блокировка уже взята).
func (s *Scheduler) dispatchReplyLocked(sid string, reply proto.Msg, err error, grantedAt time.Time) {
	now := s.clk.Now()
	rec, ok := s.dispatched[sid]
	if !ok {
		return // аренда уже снята (панель ушла, drain)
	}
	delete(s.dispatched, sid)
	sess, errGet := s.st.GetSession(sid)
	if errGet != nil || sess.State != model.SessionDispatching {
		return
	}

	switch {
	case err != nil:
		// Нет ответа узла за node_reply_timeout_sec: аренда снята,
		// запись возвращается с прежним рангом.
		s.releaseLeaseLocked(sid, model.ReleaseDispatchNoReply, now)
		s.requeueEntry(rec.entry, now)
		s.sessionEvent(sess, model.EvPaneChanged, "")
		s.log.Warn("scheduler: нет ответа узла на dispatch", "sid", sid, "err", err)

	case reply.Result == proto.ResSent:
		// SENT: ждём подтверждения в start_confirm_sec.
		s.confirm[sid] = &confirmRec{
			deadline:  now.Add(s.startConfirm()),
			entry:     rec.entry,
			grantedAt: grantedAt,
		}
		s.event(model.KindDispatch, sid, "", map[string]any{
			"result": proto.ResSent, "dispatch_ms": now.Sub(grantedAt).Milliseconds(),
		})

	case reply.Result == proto.ResAlreadyBusy:
		// ALREADY_BUSY: старт подтверждён (клавиши не отправлялись).
		s.recordLatency(grantedAt, now)
		s.confirmNowLocked(sid, now, rec.entry, grantedAt)

	case reply.Result == proto.ResPaneChanged, reply.Result == proto.ResPaneWaitUI:
		// Аренда снята, запись в очередь с прежним рангом, attempts
		// без изменения.
		reason := model.ReleasePaneChanged
		if reply.Result == proto.ResPaneWaitUI {
			reason = model.ReleasePaneWaitUI
		}
		s.releaseLeaseLocked(sid, reason, now)
		s.requeueEntry(rec.entry, now)
		s.sessionEvent(sess, model.EvPaneChanged, "")

	case reply.Result == proto.ResEmptyInput:
		// Аренда снята, HOLD(EMPTY_INPUT).
		s.releaseLeaseLocked(sid, model.ReleaseEmptyInput, now)
		s.sessionEvent(sess, model.EvEmptyInput, model.HoldEmptyInput)

	case reply.Result == proto.ResPaneGone:
		// Аренда снята, GONE.
		s.releaseLeaseLocked(sid, model.ReleasePaneGone, now)
		s.goneLocked(sess, now)

	default:
		s.releaseLeaseLocked(sid, model.ReleaseDispatchNoReply, now)
		s.requeueEntry(rec.entry, now)
		s.sessionEvent(sess, model.EvPaneChanged, "")
	}
}

// confirmNowLocked — подтверждение старта: аренда ACTIVE, сессия
// RUNNING, ход создан.
func (s *Scheduler) confirmNowLocked(sid string, now time.Time, entry model.QueueEntry, grantedAt time.Time) {
	lease, err := s.st.LeaseGet(sid)
	if err == nil && lease.State == model.LeasePending {
		_ = s.st.LeaseSetActive(sid)
	}
	sess, err := s.st.GetSession(sid)
	if err != nil {
		return
	}
	if sess.State == model.SessionDispatching {
		s.sessionEvent(sess, model.EvStartConfirmed, "")
	}
	// Запись очереди: подтверждение — она не нужна.
	_ = s.st.QueueDelete(sid)
	s.ensureTurn(sid, now, grantedAt)
	s.wake()
	_ = entry
}

// ensureTurn — ход создан (идемпотентно): при подтверждении старта и
// как страховка для неявных аренд.
func (s *Scheduler) ensureTurn(sid string, now, startedHint time.Time) {
	if _, err := s.st.OpenTurn(sid); err == nil {
		return
	}
	id, err := s.st.TurnStart(sid, startedHint)
	if err != nil {
		s.log.Error("scheduler: ход", "sid", sid, "err", err)
		return
	}
	s.event(model.KindDispatch, sid, "", map[string]any{"turn_started": id})
	_ = now
}

// tickConfirm — подтверждение старта по первому генерирующему запросу
// (аренда перестала быть PENDING — шлюз подтвердил) или по BUSY-панели;
// нет подтверждения за start_confirm_sec → HOLD(START_NOT_CONFIRMED).
func (s *Scheduler) tickConfirm(now time.Time) {
	for sid, rec := range s.confirm {
		lease, err := s.st.LeaseGet(sid)
		if err == nil && lease.State != model.LeasePending {
			// Первый запрос в шлюз: старт подтверждён.
			s.recordLatency(rec.grantedAt, now)
			s.confirmNowLocked(sid, now, rec.entry, rec.grantedAt)
			delete(s.confirm, sid)
			continue
		}
		// Панель BUSY — тоже подтверждение (раздел 8 ТЗ).
		if pr := s.paneRec[sid]; pr != nil && pr.state == model.PaneBusy {
			s.recordLatency(rec.grantedAt, now)
			s.confirmNowLocked(sid, now, rec.entry, rec.grantedAt)
			delete(s.confirm, sid)
			continue
		}
		if now.Before(rec.deadline) {
			continue
		}
		// Нет подтверждения за start_confirm_sec.
		delete(s.confirm, sid)
		s.releaseLeaseLocked(sid, model.ReleaseStartNotConf, now)
		sess, err := s.st.GetSession(sid)
		if err == nil && sess.State == model.SessionDispatching {
			s.sessionEvent(sess, model.EvStartNotConfirmed, model.HoldStartNotConfirmed)
		}
	}
}

// recordLatency — задержка диспетчеризации (grant → SENT/подтверждение).
func (s *Scheduler) recordLatency(grantedAt, now time.Time) {
	s.latencies = append(s.latencies, now.Sub(grantedAt).Milliseconds())
}

// DispatchLatency — выборка задержек диспетчеризации, мс (приёмка p95).
func (s *Scheduler) DispatchLatency() []int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]int64, len(s.latencies))
	copy(out, s.latencies)
	return out
}

// requeueEntry — возврат записи в очередь с прежним рангом.
func (s *Scheduler) requeueEntry(e model.QueueEntry, now time.Time) {
	e.IneligibleReason = ""
	e.IneligibleSince = time.Time{}
	_ = s.st.QueueUpsert(e)
	s.event(model.KindQueueEnqueue, e.SID, "", map[string]any{"requeue": true})
	_ = now
}

// sessionsMap — живые сессии (кроме GONE).
func (s *Scheduler) sessionsMap() map[string]store.SessionRecord {
	out := map[string]store.SessionRecord{}
	sessions, err := s.st.ListSessions(false)
	if err != nil {
		return out
	}
	for _, sess := range sessions {
		out[sess.SID] = sess
	}
	return out
}
