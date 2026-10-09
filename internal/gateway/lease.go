package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"runpilot/internal/model"
	"runpilot/internal/store"
)

// leaseDecision — результат разрешения аренды для запроса.
type leaseDecision struct {
	server   *Server
	lease    *model.Lease
	heldMS   int64 // сколько ждали слот (удержание), мс
	migrated bool  // аренда получена миграцией
}

// classAndConstraint — класс и ограничение для неявного захвата
// (раздел 6 ТЗ: RESUME для DETACHED, HIGH для остальных).
func classAndConstraint(sess store.SessionRecord) (model.QueueClass, model.Constraint) {
	class := model.ClassHigh
	if sess.State == model.SessionDetached {
		class = model.ClassResume
	}
	return class, model.Constraint{
		Kind:   sess.ConstraintKind,
		Server: sess.ConstraintServer,
	}
}

// resolvePass — разрешение аренды (раздел 6 ТЗ, первое сработавшее
// правило):
//
//  1. аренда ACTIVE → проксировать на её сервер;
//  2. аренда PENDING → старт подтверждён: аренда ACTIVE, сессия RUNNING;
//  3. аренды нет → неявный захват (pin/prefer/accept; пауза и проверки
//     панели не действуют); есть слот → аренда IMPLICIT, запись очереди
//     удаляется;
//  4. слота нет → удержание до gateway.hold_max_sec; таймаут →
//     errHoldTimeout (запись остаётся: held_request=false, mode=resume).
func (g *Gateway) resolvePass(ctx context.Context, sess store.SessionRecord) (*leaseDecision, error) {
	// Правила 1–2: действующая аренда.
	if lease, err := g.st.LeaseGet(sess.SID); err == nil {
		srv := g.servers.ByName(lease.Server)
		if srv != nil {
			if lease.State == model.LeasePending {
				// Старт подтверждён первым запросом в шлюз (раздел 6 ТЗ).
				_ = g.st.LeaseSetActive(sess.SID)
				if sess.State == model.SessionDispatching {
					g.applySessionEvent(sess, model.EvStartConfirmed)
				}
				return &leaseDecision{server: srv, lease: lease}, nil
			}
			// ACTIVE (и RECOVERING — домен Э7): проксировать как есть.
			return &leaseDecision{server: srv, lease: lease}, nil
		}
		// Сервера больше нет в конфигурации: аренда не пригодна,
		// переходим к захвату нового слота.
		_ = g.st.LeaseRelease(sess.SID, releaseReasonServerDown, g.clk.Now())
		g.wake()
	}

	// Правило 3: неявный захват.
	class, constraint := classAndConstraint(sess)
	if lease, srv := g.grant(class, constraint, nil, sess.SID, model.LeaseOriginImplicit); srv != nil {
		g.sessionGrantedEvents(sess)
		return &leaseDecision{server: srv, lease: lease}, nil
	}

	// Правило 4: удержание.
	return g.holdPass(ctx, sess, class, constraint, nil)
}

// migratePass — отказ апстрима до первого байта (раздел 6 ТЗ):
// внеочередная проверка /health; аренда освобождается с SERVER_DOWN;
// буферизованный запрос один раз проходит разрешение заново,
// исключая этот сервер. Есть слот → аренда MIGRATED,
// last_migrated_from = старый сервер; нет → удержание (с исключением).
func (g *Gateway) migratePass(ctx context.Context, sess store.SessionRecord,
	decision *leaseDecision) (*leaseDecision, error) {
	failed := decision.server
	if failed.HealthOK(ctx) {
		// Сервер жив: отказ транзиторный, состояние UP не меняем
		// (автомат UP/DOWN по счётчикам — монитор, Э6).
		g.log.Info("gateway: транзиторный отказ апстрима",
			"server", failed.Cfg.Name, "sid", sess.SID)
	} else {
		g.servers.SetState(failed.Cfg.Name, model.ServerDown)
	}
	if decision.lease != nil {
		_ = g.st.LeaseRelease(sess.SID, releaseReasonServerDown, g.clk.Now())
		g.wake()
	}

	class, constraint := classAndConstraint(sess)
	exclude := []string{failed.Cfg.Name}
	if lease, srv := g.grant(class, constraint, exclude, sess.SID, model.LeaseOriginMigrated); srv != nil {
		g.sessionGrantedEvents(sess)
		_ = g.st.SetSessionMigratedFrom(sess.SID, failed.Cfg.Name)
		return &leaseDecision{server: srv, lease: lease, migrated: true}, nil
	}
	d, err := g.holdPass(ctx, sess, class, constraint, exclude)
	if err != nil {
		return nil, err
	}
	d.migrated = true
	return d, nil
}

// pickServers — кандидаты в порядке приоритета с учётом pin/prefer.
func (g *Gateway) pickServers(constraint model.Constraint, exclude []string) []*Server {
	excluded := map[string]bool{}
	for _, name := range exclude {
		excluded[name] = true
	}
	if constraint.Kind == model.ConstraintPin {
		srv := g.servers.ByName(constraint.Server)
		if srv != nil && !excluded[srv.Cfg.Name] {
			return []*Server{srv}
		}
		return nil
	}
	order := g.servers.All()
	if constraint.Kind == model.ConstraintPrefer {
		if pref := g.servers.ByName(constraint.Server); pref != nil && !excluded[pref.Cfg.Name] {
			order = append([]*Server{pref}, order...)
		}
	}
	var out []*Server
	seen := map[string]bool{}
	for _, srv := range order {
		if seen[srv.Cfg.Name] || excluded[srv.Cfg.Name] {
			continue
		}
		seen[srv.Cfg.Name] = true
		out = append(out, srv)
	}
	return out
}

// holdPass — удержание (раздел 6 ТЗ, правило 4): запись очереди с
// held_request=true и тем же class; ожидание до gateway.hold_max_sec;
// слот выдан ПЛАНИРОВЩИКОМ (LEASE_GRANT, «held»:true) → проксировать;
// Esc/отмена → запись удалена + QUEUE_DEQUEUE (client_gone) + request.error;
// таймаут → запись остаётся с held_request=false и mode=resume.
//
// П.10: аренду выдаёт ТОЛЬКО планировщик. Self-grant шлюза в удержании
// отсутствует — шлюз не берёт слот сам, ждёт LEASE_GRANT своей sid.
// (acquireNow при поступлении запроса — правило 3 — не меняется.)
//
// П.5: повторный вход в удержание (ретраи клиента) сохраняет исходные
// enqueued_at / attempts / class — QueueUpsertHeld при CONFLICT берёт
// MIN(enqueued_at), MAX(attempts), MIN(class).
func (g *Gateway) holdPass(ctx context.Context, sess store.SessionRecord,
	class model.QueueClass, constraint model.Constraint, exclude []string) (*leaseDecision, error) {
	now := g.clk.Now()
	// П.5: enqueued_at/attempts/class — QueueUpsertHeld сохраняет исходные
	// при CONFLICT (повтор не сбрасывает). Первый вход — now / 0 / class.
	// п.6: HeldSince — время начала удержания (при ретрае MIN, не сбрасывается).
	_ = g.st.QueueUpsertHeld(model.QueueEntry{
		SID:         sess.SID,
		Class:       class,
		EnqueuedAt:  now,
		NotBefore:   now,
		Constraint:  constraint,
		Mode:        model.QueueModeSubmit,
		HeldRequest: true,
		HeldSince:   now,
	})
	// П.2: инкремент счётчика удерживаемых запросов по sid.
	g.heldCountAdd(sess.SID)
	g.log.Info("gateway: удержание началось", "sid", sess.SID, "hold_max_sec", g.cfg.Gateway.HoldMaxSec)
	wake := g.subscribeWake()
	defer g.unsubscribeWake(wake)
	timer := time.NewTimer(time.Duration(g.cfg.Gateway.HoldMaxSec) * time.Second)
	defer timer.Stop()

	heldMS := func() int64 { return g.clk.Now().Sub(now).Milliseconds() }
	for {
		// Аренду выдаёт ТОЛЬКО планировщик (LEASE_GRANT, «held»:true):
		// действующая аренда (создана планировщиком) → сразу в работу.
		if lease, err := g.st.LeaseGet(sess.SID); err == nil {
			if srv := g.servers.ByName(lease.Server); srv != nil {
				held := g.clk.Now().Sub(now).Milliseconds()
				g.metrics.RecordHold(float64(held) / msToSec)
				g.heldCountDec(sess.SID) // П.2: запрос получил аренду — декремент
				g.sessionGrantedEvents(sess)
				return &leaseDecision{server: srv, lease: lease, heldMS: held}, nil
			}
		}
		select {
		case <-ctx.Done():
			// П.4: Esc/отмена во время удержания — запись очереди удалена,
			// сессия IDLE, событие QUEUE_DEQUEUE (reason: client_gone),
			// request.error = CLIENT_GONE.
			g.clientGone(sess.SID, heldMS())
			return nil, ctx.Err()
		case <-timer.C:
			g.heldToResume(sess.SID, heldMS())
			return nil, errHoldTimeout
		case <-wake:
			// Слот мог освободиться (LEASE_GRANT планировщика) — проверять заново.
		}
	}
}

// heldCountAdd — инкремент счётчика удерживаемых запросов по sid.
func (g *Gateway) heldCountAdd(sid string) {
	g.hm2.Lock()
	g.heldCount[sid]++
	g.hm2.Unlock()
}

// heldCountGet — текущее значение счётчика (тесты).
func (g *Gateway) heldCountGet(sid string) int {
	g.hm2.Lock()
	defer g.hm2.Unlock()
	return g.heldCount[sid]
}

// heldCountDec — декремент счётчика; возвращает true, если это был
// последний удерживаемый запрос (счётчик стал 0).
func (g *Gateway) heldCountDec(sid string) bool {
	g.hm2.Lock()
	g.heldCount[sid]--
	last := g.heldCount[sid] <= 0
	if last {
		delete(g.heldCount, sid)
	}
	g.hm2.Unlock()
	return last
}

// clientGone — Esc/отмена во время удержания (П.4): запись очереди удалена,
// сессия IDLE, событие QUEUE_DEQUEUE (reason: client_gone),
// request.error = CLIENT_GONE.
// П.2: запись очереди удаляется, только когда обрывается последний
// удерживаемый запрос этой sid (два параллельных запроса одной сессии).
func (g *Gateway) clientGone(sid string, heldMS int64) {
	g.log.Info("gateway: client_gone во время удержания", "sid", sid, "held_ms", heldMS)
	g.metrics.RecordHold(float64(heldMS) / msToSec)
	last := g.heldCountDec(sid)
	if last {
		_ = g.st.QueueDelete(sid)
		payload, _ := json.Marshal(map[string]any{"reason": "client_gone"})
		_ = g.st.EventRecord(model.Event{TS: g.clk.Now(), Kind: model.KindQueueDequeue, SID: sid, Payload: payload})
		if sess, err := g.st.GetSession(sid); err == nil && sess.State == model.SessionQueued {
			g.applySessionEvent(sess, model.EvDequeue)
		}
		g.wake()
	}
	// П.4: request.error = CLIENT_GONE (запрос отменён клиентом до выдачи) —
	// всегда, для каждого оборванного запроса.
	_ = g.st.RequestError(sid, "CLIENT_GONE", g.clk.Now(), heldMS)
}

// grant — выбрать сервер (pin/prefer/accept/свободный слот) и создать
// аренду с заданным origin (атомарно: LeaseCreateIfAbsent — планировщик
// Э4 мог выдать аренду этой сессии параллельно; тогда берём её).
// Возвращает (nil, nil), если слота нет.
func (g *Gateway) grant(class model.QueueClass, constraint model.Constraint,
	exclude []string, sid string, origin model.LeaseOrigin) (*model.Lease, *Server) {
	for _, srv := range g.pickServers(constraint, exclude) {
		if !srv.candidateOK(class) {
			continue
		}
		leased, err := g.st.LeaseCountByServer(srv.Cfg.Name)
		if err != nil || leased >= srv.Cfg.Slots {
			continue
		}
		l := model.Lease{
			SID:       sid,
			Server:    srv.Cfg.Name,
			Slot:      leased + 1,
			State:     model.LeaseActive,
			Origin:    origin,
			GrantedAt: g.clk.Now(),
		}
		// Инвариант слотов (п.1): атомарная проверка «свободен слот» —
		// ErrSlotFull (слот занял планировщик/миграция в гонке) → следующий
		// сервер, не ошибка.
		got, err := g.st.LeaseCreateIfAbsent(l, srv.Cfg.Slots, true)
		if err != nil {
			if !errors.Is(err, store.ErrSlotFull) {
				g.log.Error("gateway: создать аренду", "sid", sid, "err", err)
			}
			continue
		}
		// Аренду мог создать планировщик на другом сервере — используем её.
		if got.Server != srv.Cfg.Name {
			if other := g.servers.ByName(got.Server); other != nil {
				return got, other
			}
			continue
		}
		return got, srv
	}
	return nil, nil
}

// heldToResume — удержание завершено без слота: запись остаётся в
// очереди с held_request=false и mode=resume (раздел 6 ТЗ).
// request.error = HOLD_TIMEOUT, held_ms = время удержания.
// П.2: декремент счётчика (запрос завершил удержание).
func (g *Gateway) heldToResume(sid string, heldMS int64) {
	g.log.Info("gateway: удержание истекло (hold_max_sec)", "sid", sid, "held_ms", heldMS)
	g.metrics.RecordHold(float64(heldMS) / msToSec)
	g.heldCountDec(sid)
	_ = g.st.QueueSetHeldRequest(sid, false, model.QueueModeResume)
	_ = g.st.RequestError(sid, "HOLD_TIMEOUT", g.clk.Now(), heldMS)
}

// sessionGrantedEvents — переходы сессии при выдаче аренды
// (автомат раздела 4, применяемый шлюзом):
//   - IDLE: ручной Enter, слот есть → RUNNING
//   - QUEUED: аренда выдана → DISPATCHING, старт подтверждён → RUNNING
//   - DISPATCHING: старт подтверждён → RUNNING
//   - DETACHED: новый запрос хода → RUNNING
func (g *Gateway) sessionGrantedEvents(sess store.SessionRecord) {
	switch sess.State {
	case model.SessionIdle:
		g.applySessionEvent(sess, model.EvEnterHasSlot)
	case model.SessionQueued:
		g.applySessionEvent(sess, model.EvLeaseGranted)
		if fresh, err := g.st.GetSession(sess.SID); err == nil {
			g.applySessionEvent(fresh, model.EvStartConfirmed)
		}
	case model.SessionDispatching:
		g.applySessionEvent(sess, model.EvStartConfirmed)
	case model.SessionDetached:
		g.applySessionEvent(sess, model.EvResumeRequest)
	}
}

// applySessionEvent — переход по автомату (проверяется NextSessionState;
// из HOLD/RUNNING шлюз сессию не двигает).
func (g *Gateway) applySessionEvent(sess store.SessionRecord, ev model.SessionEvent) {
	to, err := model.NextSessionState(sess.State, ev)
	if err != nil {
		return
	}
	_ = g.st.SetSessionState(sess.SID, to, "", g.clk.Now())
}
