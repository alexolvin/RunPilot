// Package model — сущности (раздел 4 ТЗ) и конечные автоматы как чистые функции.
//
// Поля токенов отсутствуют (строгое правило 3). Все времена — UTC.
package model

import "time"

// SessionState — состояния автомата сессии (раздел 4).
type SessionState string

const (
	SessionIdle        SessionState = "IDLE"
	SessionQueued      SessionState = "QUEUED"
	SessionDispatching SessionState = "DISPATCHING"
	SessionRunning     SessionState = "RUNNING"
	SessionDetached    SessionState = "DETACHED"
	SessionHold        SessionState = "HOLD"
	SessionGone        SessionState = "GONE"
)

// AllSessionStates — все состояния автомата (кроме начального «∅»).
var AllSessionStates = []SessionState{
	SessionIdle, SessionQueued, SessionDispatching,
	SessionRunning, SessionDetached, SessionHold, SessionGone,
}

// SessionKind — вид сессии (v2 разделы 2.4/2.5): PANE — кодер в tmux-панели
// (проверки панели действуют), JOB — задание cron (runpilot exec, без панели).
const (
	KindPane = "PANE"
	KindJob  = "JOB"
)

// ServerState — состояния сервера (раздел 4).
type ServerState string

const (
	ServerUp       ServerState = "UP"
	ServerDown     ServerState = "DOWN"
	ServerDraining ServerState = "DRAINING"
	// v2: оператор временно выводит сервер из выдачи под другой процесс.
	// Новые ходы не выдаются, идущие доделываются (как DRAINING); обратимо
	// командой «Возобновить». Команда оператора — sticky: health-серии не
	// сбрасывают в UP (как и DRAINING в ServerHealth.Observe).
	ServerPaused ServerState = "PAUSED"
	// v2 (S11): key_env не задана в окружении службы — сервер не годится.
	ServerKeyMissing ServerState = "KEY_MISSING"
	// v2 (S7/S8): модель недоступна (заданная не найдена / несколько при auto).
	ServerModelProblem ServerState = "MODEL_PROBLEM"
	// v2 (S4/S5): карантин по серии отказов (faults.quarantine_count за
	// faults.window_sec) на faults.quarantine_sec. Сервер не выдаёт аренды.
	ServerQuarantined ServerState = "QUARANTINED"
)

// FaultClass — класс отказа сервера (раздел 7.1 С4/С5/С6): классифицируется
// по телу ответа (шаблон) и коду HTTP.
type FaultClass string

const (
	FaultNone          FaultClass = ""
	FaultOOM           FaultClass = "OOM"
	FaultEngineDead    FaultClass = "ENGINE_DEAD"
	FaultContextLength FaultClass = "CONTEXT_LENGTH"
)

// SlotState — состояния слота (раздел 4).
type SlotState string

const (
	SlotFree     SlotState = "FREE"
	SlotLeased   SlotState = "LEASED"
	SlotCooldown SlotState = "COOLDOWN"
	SlotExternal SlotState = "EXTERNAL"
)

// LeaseState — состояния аренды (раздел 4).
type LeaseState string

const (
	LeasePending    LeaseState = "PENDING"
	LeaseActive     LeaseState = "ACTIVE"
	LeaseReleased   LeaseState = "RELEASED"
	LeaseRecovering LeaseState = "RECOVERING"
)

// LeaseOrigin — происхождение аренды.
type LeaseOrigin string

const (
	LeaseOriginDispatch LeaseOrigin = "DISPATCH"
	LeaseOriginImplicit LeaseOrigin = "IMPLICIT"
	LeaseOriginMigrated LeaseOrigin = "MIGRATED"
)

// TurnOutcome — исход хода (раздел 4).
type TurnOutcome string

const (
	TurnOK        TurnOutcome = "OK"
	TurnError     TurnOutcome = "ERROR"
	TurnCancelled TurnOutcome = "CANCELLED"
	TurnLost      TurnOutcome = "LOST"
)

// TurnResult — исходы по последнему генерирующему запросу (раздел 8).
type TurnResult string

const (
	ResultOK             TurnResult = "OK"
	ResultErrorTransient TurnResult = "ERROR_TRANSIENT"
	ResultErrorClient    TurnResult = "ERROR_CLIENT"
)

// Mode — режим координатора (v2 раздел 3.1). NORMAL/PAUSED — базовое ТЗ
// (6.1); EMERGENCY (6.2) и SAFE_MODE (6.4) — W7. Приоритет: принудительный
// (стенд/тест) > EMERGENCY > SAFE_MODE > PAUSED > NORMAL.
type Mode string

const (
	ModeNormal    Mode = "NORMAL"
	ModePaused    Mode = "PAUSED"
	ModeEmergency Mode = "EMERGENCY"
	ModeSafeMode  Mode = "SAFE_MODE"
)

// QueueClass — класс записи очереди (раздел 5).
type QueueClass int

// String возвращает имя класса, как в TUI/API.
func (c QueueClass) String() string {
	switch c {
	case ClassResume:
		return "resume"
	case ClassHigh:
		return "high"
	case ClassNormal:
		return "normal"
	case ClassLow:
		return "low"
	}
	return "unknown"
}

// QueueClassParse разбирает имя класса.
func QueueClassParse(s string) (QueueClass, bool) {
	switch s {
	case "resume":
		return ClassResume, true
	case "high":
		return ClassHigh, true
	case "normal":
		return ClassNormal, true
	case "low":
		return ClassLow, true
	}
	return 0, false
}

// HoldReason — коды причин удержания (раздел 4).
type HoldReason string

const (
	HoldEmptyInput        HoldReason = "EMPTY_INPUT"
	HoldStartNotConfirmed HoldReason = "START_NOT_CONFIRMED"
	HoldRequeueLimit      HoldReason = "REQUEUE_LIMIT"
	HoldPaneUnknown       HoldReason = "PANE_UNKNOWN"
	HoldNodeLost          HoldReason = "NODE_LOST"
	HoldNodeDrain         HoldReason = "NODE_DRAIN"
	HoldOperator          HoldReason = "OPERATOR"
	HoldUpstream4XX       HoldReason = "UPSTREAM_4XX"
	HoldDependencyFailed  HoldReason = "DEPENDENCY_FAILED"
	HoldPinUnavailable    HoldReason = "PIN_UNAVAILABLE"
	// v2 (раздел 3.3): новые коды hold_reason.
	HoldAgentExited HoldReason = "AGENT_EXITED" // кодер в панели завершился
	HoldEmergency   HoldReason = "EMERGENCY"    // ход прерван аварийной остановкой
)

// ConstraintKind — вид ограничения записи (раздел 4).
type ConstraintKind string

const (
	ConstraintNone   ConstraintKind = "none"
	ConstraintPin    ConstraintKind = "pin"
	ConstraintPrefer ConstraintKind = "prefer"
)

// Constraint — ограничение pin:<srv> / prefer:<srv>.
type Constraint struct {
	Kind   ConstraintKind
	Server string
}

// NoConstraint — отсутствие ограничения.
var NoConstraint = Constraint{Kind: ConstraintNone}

// String даёт каноническую запись: none | pin:X | prefer:X.
func (c Constraint) String() string {
	if c.Kind == ConstraintNone || c.Server == "" {
		return string(ConstraintNone)
	}
	return string(c.Kind) + ":" + c.Server
}

// ConstraintParse разбирает "none" | "pin:X" | "prefer:X".
func ConstraintParse(s string) (Constraint, bool) {
	switch s {
	case "", string(ConstraintNone):
		return NoConstraint, true
	}
	for _, kind := range []ConstraintKind{ConstraintPin, ConstraintPrefer} {
		prefix := string(kind) + ":"
		if len(s) > len(prefix) && s[:len(prefix)] == prefix {
			return Constraint{Kind: kind, Server: s[len(prefix):]}, true
		}
	}
	return Constraint{}, false
}

// QueueMode — режим записи очереди (раздел 4).
type QueueMode string

const (
	QueueModeSubmit QueueMode = "submit"
	QueueModeResume QueueMode = "resume"
)

// PaneState — состояния панели по детектору (раздел 4).
type PaneState string

const (
	PaneBusy    PaneState = "BUSY"
	PanePrompt  PaneState = "PROMPT"
	PaneIdle    PaneState = "IDLE"
	PaneWaitUI  PaneState = "WAIT_UI"
	PaneUnknown PaneState = "UNKNOWN"
)

// PaneUnmanaged — панель с кодером, не запущенным через runpilot run (раздел 4).
// Состояние отображения, не входит в автомат сессии.
const PaneUnmanaged PaneState = "UNMANAGED"

// Session — сущность session (раздел 4).
type Session struct {
	SID              string
	Name             string
	Host             string
	HostIP           string
	TmuxSession      string
	Window           int
	PaneID           string
	Profile          string
	AgentVersion     string
	State            SessionState
	StateChangedAt   time.Time
	HoldReason       HoldReason
	Class            QueueClass
	Constraint       Constraint
	AutoEnqueue      bool
	LastServer       string
	LastTurnEnd      time.Time
	LastMigratedFrom string
	Attempts         int
	CreatedAt        time.Time
}

// QueueEntry — запись очереди (раздел 4).
type QueueEntry struct {
	SID              string
	Class            QueueClass
	EnqueuedAt       time.Time
	NotBefore        time.Time
	Constraint       Constraint
	AfterSID         string
	Mode             QueueMode
	HeldRequest      bool
	IneligibleReason string
	IneligibleSince  time.Time
}

// Lease — аренда слота (раздел 4).
type Lease struct {
	ID            int64
	SID           string
	Server        string
	Slot          int
	State         LeaseState
	Origin        LeaseOrigin
	GrantedAt     time.Time
	ReleasedAt    time.Time
	ReleaseReason string
	Requests      int
}

// Turn — ход (раздел 4).
type Turn struct {
	ID          int64
	SID         string
	SessionName string
	StartedAt   time.Time
	EndedAt     time.Time
	Servers     []string // > 1 при миграции
	Outcome     TurnOutcome
	Requests    int
}

// Request — запрос хода. Тело не хранится (раздел 4, строгое правило 7).
type Request struct {
	ID         int64
	SID        string
	TurnID     int64
	Server     string
	Path       string
	Status     int
	TStart     time.Time
	TFirstByte time.Time
	TEnd       time.Time
	HeldMS     int64
	Error      string
}

// Event — событие (раздел 4, схема приложения Б).
type Event struct {
	ID      int64 // в БД (SSE: id/after); 0 при записи
	TS      time.Time
	Kind    string
	SID     string
	Server  string
	Payload []byte
}

// Виды событий (приложение Б ТЗ).
const (
	KindSessionState = "SESSION_STATE"
	KindQueueEnqueue = "QUEUE_ENQUEUE"
	KindQueueDequeue = "QUEUE_DEQUEUE"
	KindQueueSkip    = "QUEUE_SKIP"
	KindLeaseGrant   = "LEASE_GRANT"
	KindLeaseRelease = "LEASE_RELEASE"
	KindDispatch     = "DISPATCH"
	KindTurnEnd      = "TURN_END"
	KindServerState  = "SERVER_STATE"
	// v2 (S7/S8): наблюдение моделей — SERVER_MODEL_CHANGED / MODEL_PROBLEM.
	KindServerModel = "SERVER_MODEL"
	// v2 (S4/S5): отказ класса OOM/ENGINE_DEAD — SERVER_FAULT (payload:
	// class, count, window); при пороге — переход в карантин.
	KindServerFault = "SERVER_FAULT"
	KindNodeState   = "NODE_STATE"
	KindHold        = "HOLD"
	KindNotify      = "NOTIFY"
	// v2 (6.2/6.4): смена режима координатора (NORMAL/PAUSED/EMERGENCY/
	// SAFE_MODE). Пишется в БД — виден в журнале и телем.
	KindMode = "MODE"
	// v2 (раздел 15.4): RESYNC — досылка больше буфера SSE; клиент заново
	// берёт GET /api/v1/state. Событие не пишется в БД — только в поток.
	KindResync = "RESYNC"
	// v2 (раздел 15.4): SCREEN — кадр живого экрана по подписке ?screens=
	// (координатор просит узел снять панель с -e). Не пишется в БД.
	KindScreen = "SCREEN"
	// v2 (8.1): состояние задания runpilot exec (JOB): finish (OK/ERROR),
	// CLIENT_LOST (X5). payload: outcome, exit_code, signal.
	KindJobState = "JOB_STATE"
	// v2 (8.2): внешний кодер вне tmux/runpilot. payload: source, target, exe.
	KindExternalProc = "EXTERNAL_PROCESS"
)

// Коды снятия аренды (раздел 8 ТЗ + диспетчеризация).
const (
	ReleaseTurnDone        = "TURN_DONE"
	ReleaseToolTimeout     = "TOOL_TIMEOUT"
	ReleaseApprovalTimeout = "APPROVAL_TIMEOUT"
	ReleaseServerDown      = "SERVER_DOWN"
	ReleasePaneUnknown     = "PANE_UNKNOWN"
	ReleaseNodeLost        = "NODE_LOST"
	ReleasePaneGone        = "PANE_GONE"
	ReleaseStartNotConf    = "START_NOT_CONFIRMED"
	ReleaseEmptyInput      = "EMPTY_INPUT"
	// v2 (6.2): аварийная остановка снимает аренды этой причиной, исход
	// хода CANCELLED, сессия → HOLD(EMERGENCY).
	ReleaseEmergency = "EMERGENCY"
	// v2 (6.5): восстановление после аварийного завершения координатора —
	// идущий ход LOST (= ERROR_TRANSIENT), сессия → очередь (resume).
	ReleaseCrash = "CRASH"
	// v2 (8.1, X5): клиент runpilot exec пропал (нет пульса
	// jobs.heartbeat_timeout_sec) — аренда снимается, исход LOST, без
	// перепостановки (повтор — дело cron).
	ReleaseClientLost = "CLIENT_LOST"
	// v2 (7.3, C1): кодер в панели завершился/убит (@runpilot_exit не пусто) —
	// аренда снята, ход LOST, сессия → HOLD(AGENT_EXITED).
	ReleaseAgentExited     = "AGENT_EXITED"
	ReleasePaneChanged     = "PANE_CHANGED"
	ReleasePaneWaitUI      = "PANE_WAIT_UI"
	ReleaseDispatchNoReply = "DISPATCH_NO_REPLY"
)

// Коды ineligible_reason (приложение А ТЗ).
const (
	ReasonPause          = "pause"
	ReasonNotBefore      = "not_before"
	ReasonAfterWait      = "after_wait"
	ReasonStaleSnapshot  = "stale_snapshot"
	ReasonNotIdle        = "not_idle"
	ReasonOperatorTyping = "operator_typing"
	ReasonWaitUI         = "wait_ui"
	ReasonPinMismatch    = "pin_mismatch"
	ReasonPinDown        = "pin_down"
	ReasonPreferWait     = "prefer_wait"
	ReasonAccept         = "accept"
	ReasonCooldown       = "cooldown"
	ReasonExternal       = "external"
	ReasonNoUpServer     = "no_up_server"
	ReasonNodeDrain      = "node_drain"
	ReasonHeldOK         = "held_ok"
	// v2 (раздел 3.3): новые коды ineligible_reason.
	ReasonEmergency        = "emergency"
	ReasonSafeMode         = "safe_mode"
	ReasonServerDisabled   = "server_disabled"
	ReasonServerQuarantine = "server_quarantine"
	ReasonModelProblem     = "model_problem"
	ReasonKeyMissing       = "key_missing"
	// v2 (раздел 8.1): число одновременных JOB-аренд = jobs.slots.
	ReasonJobSlots = "job_slots"
)

// ServerRuntime — runtime-состояние сервера (раздел 4).
// Хранится в памяти координатора и восстанавливается монитором при старте
// (раздел 14: состояние сервера — не персистентная очередь/аренды).
type ServerRuntime struct {
	Name           string
	State          ServerState
	DownSince      time.Time
	MetricsMissing bool
}

// Статусы внешнего кодера (8.2 ТЗ).
const (
	ExtActive  = "ACTIVE"
	ExtKilled  = "KILLED"
	ExtGone    = "GONE"
	ExtIgnored = "IGNORED"
)

// Цели внешнего кодера (8.2 ТЗ): вывод координатора по OPENAI_BASE_URL.
const (
	ExtTargetUnknown = "unknown"
	ExtTargetGateway = "gateway:" // gateway:<sid>
	ExtTargetServer  = "server:"  // server:<name>
)

// ExternalProcess — внешний кодер вне tmux/runpilot (8.2 ТЗ). Устойчивый ключ —
// host+pid+start_time. source/target — вывод координатора; exe + flags —
// без значений аргументов (R5).
type ExternalProcess struct {
	Host      string
	PID       int
	UID       int
	StartTime int64
	Source    string
	Exe       string
	Flags     string // пробельный список имён флагов
	Target    string
	Status    string
	FirstSeen time.Time
	LastSeen  time.Time
}

// IgnoreRule — правило «Игнорировать» (8.2 ТЗ): host/exe/flags; пустое поле —
// любое значение, flags — список флагов, которые должны входить в кандидата.
type IgnoreRule struct {
	ID      int
	Host    string
	Exe     string
	Flags   string
	Created time.Time
}
