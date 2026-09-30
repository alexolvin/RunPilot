// Package scheduler — планировщик runpilot (раздел 5 ТЗ).
//
// Тик каждые scheduler.tick_ms и на каждое событие: жадная выдача
// свободных слотов записям очереди в порядке ранга; пропуск записи
// обязан записать ineligible_reason (приложение А) и, при смене
// причины, событие QUEUE_SKIP.
//
// Строгие правила:
//   - все пороги — только из конфигурации (раздел 13); числовые
//     литералы в пакете запрещены scripts/check-literals, кроме 0 и 1;
//   - время «сейчас» — только через internal/clock.

package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"runpilot/internal/clock"
	"runpilot/internal/config"
	"runpilot/internal/model"
	"runpilot/internal/proto"
	"runpilot/internal/store"
)

// ServerView — сервер в представлении планировщика.
type ServerView struct {
	Name     string
	Priority int
	Slots    int
	Accept   []string
	State    model.ServerState

	// Э6 (раздел 10 ТЗ): метрики монитора; nil = «—» в TUI.
	Running *int     // vllm:num_requests_running
	Waiting *int     // vllm:num_requests_waiting
	KV      *float64 // vllm:kv_cache_usage_perc, %
	GenTokS *float64 // tok/s за monitor.rate_window_sec
	Ext     *int     // внешняя нагрузка; nil = metrics_missing
	Missing bool     // metrics_missing (раздел 5 ТЗ)

	// GPU-телеметрия узла (раздел 10 ТЗ): на строке сервера — максимум
	// утилизации и сумма VRAM; разбивка по картам — в карточке.
	GPUPct    *int
	VRAMUsed  *float64 // GB
	VRAMTotal *float64 // GB
	GPUCards  []proto.GPUCard
}

// Servers — источник сведений о серверах (в порядке убывания priority).
// Продакшн: адаптер gateway.Servers; тесты: фэйк.
type Servers interface {
	List() []ServerView
}

// PaneSnap — снимок панели.
type PaneSnap struct {
	PaneID     string
	SID        string
	State      model.PaneState
	Hash       uint64
	InputEmpty bool
	ReceivedAt time.Time
	Host       string
}

// PaneSource — снимки панелей (продакшн: адаптер api.Hub).
type PaneSource interface {
	PaneBySID(sid string) (PaneSnap, bool)
	PaneByPaneID(paneID string) (PaneSnap, bool)
}

// DispatchFunc — команда dispatch узлу с ожиданием reply
// (продакшн: Hub.Dispatch; таймаут держит вызывающий).
type DispatchFunc func(ctx context.Context, paneID string, m proto.Msg) (proto.Msg, error)

// Options — зависимости планировщика.
type Options struct {
	Cfg        *config.Config
	Store      *store.Store
	Clk        clock.Clock
	Servers    Servers
	Panes      PaneSource
	Dispatch   DispatchFunc
	ResumeText string   // profiles/qwen.yaml: resume_text
	CancelKeys []string // profiles/qwen.yaml: cancel_keys (runpilot cancel)
	Notify     func(text string)
	Wake       func() // разбудить ждущих слота в шлюзе
	Log        *slog.Logger
	// NoAsyncDispatch — тесты: dispatch не спавнит горутины, reply
	// подаётся через InjectReply (детерминизм виртуальных часов).
	NoAsyncDispatch bool
	// Reload — v2 (раздел 4.4): пере-read рабочих настроек из БД перед тиком.
	// nil — настройки статичны (cfg не меняется в работе).
	Reload func() error
	// OnServerIdle — v2 (W6, 5.2): сервер, помеченный на удаление, стал
	// свободен (нет активных ходов) → координатор завершает удаление.
	OnServerIdle func(name string)
}

type dispatchRec struct {
	paneID      string
	mode        string
	expectHash  uint64
	entry       model.QueueEntry
	grantedAt   time.Time
	leaseServer string
}

type confirmRec struct {
	deadline  time.Time
	entry     model.QueueEntry
	grantedAt time.Time
}

type paneRec struct {
	paneID     string
	host       string
	state      model.PaneState
	hash       uint64
	hashSince  time.Time
	stateSince time.Time
	receivedAt time.Time
}

type whyRec struct {
	reason     string
	candidates []string
	predicates map[string]any
	ts         time.Time
}

// Scheduler — планировщик (раздел 5 ТЗ).
type Scheduler struct {
	cfg        *config.Config
	st         *store.Store
	clk        clock.Clock
	src        Servers
	panes      PaneSource
	dispatch   DispatchFunc
	resumeText string
	cancelKeys []string
	notify     func(text string)
	wake       func()
	log        *slog.Logger
	// reload — v2 (раздел 4.4): источник рабочих настроек (БД); вызывается
	// в начале тика. nil — настройки статичны.
	reload func() error
	// W6 (5.2): серверы, ожидающие удаления; onServerIdle — хук завершения.
	onServerIdle   func(name string)
	serverRemoving map[string]bool

	mu sync.Mutex

	// Серверы: момент ухода в DOWN (pin_down / PIN_UNAVAILABLE).
	downSince map[string]time.Time

	// Узлы: видимость, момент отключения, режим drain.
	nodeSeen  map[string]time.Time
	nodeDown  map[string]time.Time
	nodeDrain map[string]bool

	// Слоты: COOLDOWN (server → slot → до какого времени),
	// момент освобождения (LRU, правило 4), EXTERNAL.
	cooldown  map[string]map[int]time.Time
	freeSince map[string]map[int]time.Time
	extTarget map[string]int
	extCount  map[string]int
	extSince  map[string]time.Time

	// Диспетчеризация и подтверждение старта.
	dispatched map[string]*dispatchRec
	confirm    map[string]*confirmRec

	// Панели (правила снятия аренды, стабильность, автопостановка).
	paneRec map[string]*paneRec

	// Запросы (завершение хода: inflight и last_request_end).
	inflight   map[string]int
	lastReqEnd map[string]time.Time

	// Автопостановка: момент, с которого условия непрерывно выполняются.
	autoSince map[string]time.Time

	// queueWaitNotified (C14): sid → enqueued_at, за которую уже отправлено
	// уведомление «Ждёт N мин» (раз по постановке в очередь).
	queueWaitNotified map[string]time.Time

	// why: последний результат оценки записи.
	why map[string]*whyRec

	// Пауза (runpilot pause).
	paused bool

	// jobUsed — число активных JOB-аренд (раздел 8.1): вычисляется в начале
	// tickGrant, растёт на каждую JOB-выдачу; потолок = jobs.slots.
	jobUsed int

	// ModeForced — принудительный режим (стенд/тесты; W7 — аварийный режим).
	// Пусто — режим вычисляется из safety-mode и паузы.
	modeForced string

	// mode — режим безопасности (W7, 6.2/6.4): "" | EMERGENCY | SAFE_MODE.
	// Загружается из meta.mode при старте (LoadMode); меняет выдачу аренд
	// и, при EMERGENCY, снимает идущие (emergencyStopLocked).
	mode string

	// safeSince — вход в SAFE_MODE (6.4): для интервала safe_probe_sec.
	safeSince time.Time

	// W7 (6.2): хук аварийной остановки — координатор зовёт
	// gateway.CancelAll() (отмена идущих потоков к апстримам).
	emergencyHook func()

	// Тесты: без async-горутины dispatch.
	noAsyncDispatch bool

	// Задержки диспетчеризации, мс (grant → SENT): приёмка p95.
	latencies []int64
}

// New создаёт планировщик.
func New(o Options) *Scheduler {
	if o.Notify == nil {
		o.Notify = func(string) {}
	}
	if o.Wake == nil {
		o.Wake = func() {}
	}
	if o.Log == nil {
		o.Log = slog.Default()
	}
	return &Scheduler{
		cfg:               o.Cfg,
		st:                o.Store,
		clk:               o.Clk,
		src:               o.Servers,
		panes:             o.Panes,
		dispatch:          o.Dispatch,
		resumeText:        o.ResumeText,
		cancelKeys:        o.CancelKeys,
		notify:            o.Notify,
		wake:              o.Wake,
		log:               o.Log,
		reload:            o.Reload,
		onServerIdle:      o.OnServerIdle,
		serverRemoving:    map[string]bool{},
		downSince:         map[string]time.Time{},
		nodeSeen:          map[string]time.Time{},
		nodeDown:          map[string]time.Time{},
		nodeDrain:         map[string]bool{},
		cooldown:          map[string]map[int]time.Time{},
		freeSince:         map[string]map[int]time.Time{},
		extTarget:         map[string]int{},
		extCount:          map[string]int{},
		extSince:          map[string]time.Time{},
		dispatched:        map[string]*dispatchRec{},
		confirm:           map[string]*confirmRec{},
		paneRec:           map[string]*paneRec{},
		inflight:          map[string]int{},
		lastReqEnd:        map[string]time.Time{},
		autoSince:         map[string]time.Time{},
		queueWaitNotified: map[string]time.Time{},
		why:               map[string]*whyRec{},
		noAsyncDispatch:   o.NoAsyncDispatch,
	}
}

// Start — цикл тиков (каждый scheduler.tick_ms) до отмены ctx.
// В тестах Ticks вызываются вручную (виртуальные часы).
func (s *Scheduler) Start(ctx context.Context) {
	tick := time.Duration(s.cfg.Scheduler.TickMS) * time.Millisecond
	go func() {
		t := time.NewTicker(tick)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.Tick()
			}
		}
	}()
}

// Tick — один проход планировщика: таймеры + основной цикл выдачи.
func (s *Scheduler) Tick() {
	s.mu.Lock()
	defer s.mu.Unlock()
	// v2 (раздел 4.4): свежие рабочие настройки из БД — со следующего тика
	// действуют новые пороги scheduler/monitor/notify и новые значения
	// dispatch/turn/gateway для новых диспетчеризаций.
	if s.reload != nil {
		if err := s.reload(); err != nil {
			s.log.Error("scheduler: пере-read настроек", "err", err)
		}
	}
	now := s.clk.Now()
	s.tickExternal(now)
	s.tickCooldown(now)
	s.tickDependencyAndPin(now)
	if s.mode == string(model.ModeSafeMode) {
		s.tickSafeProbe(now)
	}
	s.tickGrant(now)
	s.tickConfirm(now)
	s.tickComplete(now)
	s.tickQueueWait(now)
	s.tickNodeLost(now)
	s.tickJobStale(now)
	s.tickAutoEnqueue(now)
	s.tickServerRemoval(now)
}

// --- События извне (узел, шлюз, оператор) ---

// RequestTick — генерирующий запрос начался (true) или закончился
// (false). Хуки шлюза; вне блокировок шлюза.
func (s *Scheduler) RequestTick(sid string, started bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if started {
		s.inflight[sid]++
	} else {
		s.inflight[sid]--
		if s.inflight[sid] <= 0 {
			delete(s.inflight, sid)
		}
		s.lastReqEnd[sid] = s.clk.Now()
	}
}

// PaneUpdate — новый снимок панели (узел). paneID или sid — один из
// них обязателен; gone — панель исчезла.
func (s *Scheduler) PaneUpdate(paneID, sid string, st model.PaneState, hash uint64, receivedAt time.Time) {
	s.mu.Lock()
	now := s.clk.Now()
	pr := s.paneRec[sid]
	if pr == nil {
		pr = &paneRec{}
		if snap, ok := s.panes.PaneByPaneID(paneID); ok && snap.SID != "" {
			sid = snap.SID
		}
		s.paneRec[sid] = pr
		pr = s.paneRec[sid]
	}
	pr.paneID = paneID
	if st != pr.state {
		pr.state = st
		pr.stateSince = now
		// Вход в PROMPT: уведомление сразу (раздел 8 ТЗ), до таймаута.
		if st == model.PanePrompt {
			if sess, err := s.st.GetSession(sid); err == nil && sess.State == model.SessionRunning {
				s.notifyf("runpilot: %s: ожидает подтверждения (PROMPT)", sess.Name)
			}
		}
	}
	if hash != pr.hash {
		pr.hash = hash
		pr.hashSince = now
		// Оператор печатает — отсчёт автопостановки сбрасывается.
		delete(s.autoSince, sid)
	}
	pr.receivedAt = receivedAt
	if host, ok := s.panes.PaneByPaneID(paneID); ok && host.Host != "" {
		pr.host = host.Host
	}
	s.mu.Unlock()
}

// PaneGone — панель исчезла: аренда, очередь, сессия → GONE.
func (s *Scheduler) PaneGone(paneID, sid string) {
	s.mu.Lock()
	now := s.clk.Now()
	if sid == "" {
		if snap, ok := s.panes.PaneByPaneID(paneID); ok {
			sid = snap.SID
		}
	}
	s.releaseAndGoneLocked(sid, now)
	s.mu.Unlock()
}

// NodeSeen — узел зарегистрирован / шлёт трафик.
func (s *Scheduler) NodeSeen(host string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clk.Now()
	_, wasDown := s.nodeDown[host]
	s.nodeSeen[host] = now
	delete(s.nodeDown, host)
	if wasDown {
		s.event(model.KindNodeState, "", host, map[string]any{"host": host, "state": "UP"})
	}
}

// NodeLost — узел отключился: аренды его RUNNING-сессий снимаются
// через turn.node_lost_release_sec без трафика (раздел 8 ТЗ).
func (s *Scheduler) NodeLost(host string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, isDown := s.nodeDown[host]; !isDown {
		s.event(model.KindNodeState, "", host, map[string]any{"host": host, "state": "DOWN"})
	}
	s.nodeDown[host] = s.clk.Now()
}

// NodeDrain — режим drain узла (runpilot node drain): новые выдачи не
// выдаются; идущие ходы живут (раздел 8, приложение Д).
func (s *Scheduler) NodeDrain(host string, on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clk.Now()
	if on {
		s.nodeDrain[host] = true
	} else {
		delete(s.nodeDrain, host)
	}
	s.event(model.KindNodeState, "", host, map[string]any{"host": host, "drain": on, "ts": now})
	sessions, err := s.st.ListSessions(false)
	if err != nil {
		return
	}
	for _, sess := range sessions {
		if sess.Host != host {
			continue
		}
		if on && (sess.State == model.SessionIdle || sess.State == model.SessionQueued) {
			s.sessionEvent(sess, model.EvNodeDrain, model.HoldNodeDrain)
		}
	}
}

// ServerState — смена состояния сервера (шлюз/монитор).
func (s *Scheduler) ServerState(name string, st model.ServerState) {
	s.mu.Lock()
	now := s.clk.Now()
	if st == model.ServerDown {
		s.downSince[name] = now
	} else {
		delete(s.downSince, name)
	}
	s.event(model.KindServerState, "", name, map[string]any{"server": name, "state": string(st)})
	s.serverDownLocked(name, now)
	s.mu.Unlock()
}

// CancelServerTurns — оператор: снять ходы с сервера (раздел 7.1 С4):
// аренды RUNNING-сессий сервера (inflight = 0) снимаются (SERVER_DOWN),
// сессии → DETACHED (в очередь на resume). Возвращает число снятых.
func (s *Scheduler) CancelServerTurns(server string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clk.Now()
	n := 0
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
		n++
	}
	return n
}

// MarkServerRemoving — сервер помечен на удаление (5.2, after_turns): новые
// ходы не выдаются; удаление завершится, когда активных ходов не останется.
func (s *Scheduler) MarkServerRemoving(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.serverRemoving[name] = true
	s.event(model.KindServerState, "", name, map[string]any{"server": name, "state": "REMOVING"})
}

// ServerRemoving — помечен ли сервер на удаление (для вида/веба).
func (s *Scheduler) ServerRemoving(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.serverRemoving[name]
}

// serverUsedLocked — активные ходы сервера (аренды) — вызывается с mu.
func (s *Scheduler) serverUsedLocked(name string) int {
	leases, _ := s.st.LeaseListActive()
	n := 0
	for _, l := range leases {
		if l.Server == name {
			n++
		}
	}
	return n
}

// tickServerRemoval — завершить удаление помеченных серверов, у которых нет
// активных ходов (W6, 5.2). Вызывается из Tick (s.mu уже держится); хук
// (удаление из БД/реестра) вызывается вне блокировки.
func (s *Scheduler) tickServerRemoval(now time.Time) {
	var idle []string
	for name := range s.serverRemoving {
		if s.serverUsedLocked(name) == 0 {
			idle = append(idle, name)
			delete(s.serverRemoving, name)
		}
	}
	hook := s.onServerIdle
	if len(idle) == 0 || hook == nil {
		return
	}
	s.mu.Unlock() // Tick отпустит через defer после re-lock
	for _, name := range idle {
		hook(name)
	}
	s.mu.Lock()
}

// SetExternal — внешняя нагрузка сервера (monitor, раздел 5 ТЗ):
// ext = max(0, running + waiting − inflight_gw(s)). EXTERNAL — после
// scheduler.external_confirm_sec, обратно — после того же интервала.
func (s *Scheduler) SetExternal(name string, ext int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.extTarget[name] = ext
	s.tickExternal(s.clk.Now())
}

// Pause — пауза (runpilot pause): submit/resume-записи не выдаются; неявные
// аренды и held-запросы выдаются.
func (s *Scheduler) Pause(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.paused = on
}

// Mode — режим координатора (v2 раздел 3.1). Приоритет (W7): принудительный
// (ModeForced, стенд/тест) > EMERGENCY > SAFE_MODE > PAUSED > NORMAL.
func (s *Scheduler) Mode() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.modeForced != "" {
		return s.modeForced
	}
	if s.mode == string(model.ModeEmergency) {
		return string(model.ModeEmergency)
	}
	if s.mode == string(model.ModeSafeMode) {
		return string(model.ModeSafeMode)
	}
	if s.paused {
		return string(model.ModePaused)
	}
	return string(model.ModeNormal)
}

// SetModeForced — принудительный режим (стенд/тесты). Пустая строка снимает
// принудительный режим (режим из safety-mode и паузы).
func (s *Scheduler) SetModeForced(m string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.modeForced = m
}

// LoadMode — режим безопасности из БД (meta.mode) при старте (6.2: EMERGENCY
// переживает рестарт; 6.4 — SAFE_MODE). Вызывается до Start.
func (s *Scheduler) LoadMode() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.st.Mode()
	if err != nil {
		return err
	}
	s.mode = m
	return nil
}

// SetEmergencyHook — хук аварийной остановки (W7 6.2): координатор зовёт
// gateway.CancelAll() для отмены идущих потоков к апстримам.
func (s *Scheduler) SetEmergencyHook(fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.emergencyHook = fn
}

// grantsBlocked — не выдавать НОВЫХ аренд (6.2: EMERGENCY — никаких, вкл.
// неявные; 6.4: SAFE_MODE — нет новых, идущие обслуживаются).
func (s *Scheduler) grantsBlocked() bool {
	return s.mode == string(model.ModeEmergency) || s.mode == string(model.ModeSafeMode)
}

// --- Вспомогательные ---

func (s *Scheduler) notifyf(format string, args ...any) {
	// text-only уведомления (Э7: Telegram). Аргументы ОБЯЗАТЕЛЬНЫ для
	// форматирования: до фикса CRIT шёл с дословным «(%s)» и реальная
	// ошибка записи терялась (W9 доп-3b, репорт оператора).
	s.notify(fmt.Sprintf(format, args...))
}

func (s *Scheduler) sessionEvent(sess store.SessionRecord, ev model.SessionEvent, hold model.HoldReason) {
	to, err := model.NextSessionState(sess.State, ev)
	if err != nil {
		s.log.Warn("scheduler: недопустимый переход", "sid", sess.SID,
			"state", string(sess.State), "event", string(ev), "err", err)
		return
	}
	if err := s.st.SetSessionState(sess.SID, to, hold, s.clk.Now()); err != nil {
		s.log.Error("scheduler: состояние сессии", "sid", sess.SID, "err", err)
		return
	}
	s.event(model.KindSessionState, sess.SID, "", map[string]any{
		"from": string(sess.State), "to": string(to),
	})
	if to == model.SessionHold && hold != "" {
		s.event(model.KindHold, sess.SID, "", map[string]any{"reason": string(hold)})
	}
}

// event — запись события (приложение Б ТЗ).
func (s *Scheduler) event(kind, sid, server string, payload map[string]any) {
	data, _ := json.Marshal(payload)
	_ = s.st.EventRecord(model.Event{TS: s.clk.Now(), Kind: kind, SID: sid, Server: server, Payload: data})
}
