package client

// Ответы координатора для CLI и TUI. Имена полей зеркалят JSON сервера:
// store.SessionRecord маршализуется без тегов (ключи = имена полей Go),
// proto.Pane — с тегами, записи планировщика — с тегами из internal/scheduler.

// Action — доступное действие сущности (v2 15.2).
type Action struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
	Reason  string `json:"reason,omitempty"`
}

// State — GET /api/v1/state (срез координатора; v2: mode).
type State struct {
	Mode     string    `json:"mode"`
	Sessions []Session `json:"sessions"`
	Panes    []Pane    `json:"panes"`
	Nodes    []Node    `json:"nodes"`
}

// Session — запись session (store.SessionRecord, ключи без тегов).
type Session struct {
	SID              string `json:"SID"`
	Name             string `json:"Name"`
	Host             string `json:"Host"`
	HostIP           string `json:"HostIP"`
	TmuxSession      string `json:"TmuxSession"`
	Window           int    `json:"Window"`
	PaneID           string `json:"PaneID"`
	Profile          string `json:"Profile"`
	AgentVersion     string `json:"AgentVersion"`
	State            string `json:"State"`
	StateChangedAt   string `json:"StateChangedAt"`
	HoldReason       string `json:"HoldReason"`
	Class            int    `json:"Class"`
	ConstraintKind   string `json:"ConstraintKind"`
	ConstraintServer string `json:"ConstraintServer"`
	AutoEnqueue      bool   `json:"AutoEnqueue"`
	LastServer       string `json:"LastServer"`
	LastTurnEnd      string `json:"LastTurnEnd"`
	LastMigratedFrom string `json:"LastMigratedFrom"`
	Attempts         int    `json:"Attempts"`
	CreatedAt        string `json:"CreatedAt"`
	Actions          []Action `json:"actions"` // v2 15.2
}

// Pane — снимок панели (api.PaneInfo: {Pane, Host, ReceivedAt}).
type Pane struct {
	Pane       ProtoPane `json:"Pane"`
	Host       string    `json:"Host"`
	ReceivedAt string    `json:"ReceivedAt"`
}

// ProtoPane — proto.Pane (с json-тегами).
type ProtoPane struct {
	PaneID       string `json:"pane_id"`
	SID          string `json:"sid,omitempty"`
	State        string `json:"state"`
	Hash         uint64 `json:"hash"`
	StableSince  string `json:"stable_since"`
	InputPreview string `json:"input_preview,omitempty"`
	InputEmpty   bool   `json:"input_empty"`
	AgentVersion string `json:"agent_version,omitempty"`
}

// Node — узел (api.NodeInfo без Conn).
type Node struct {
	Host        string   `json:"Host"`
	HostIP      string   `json:"HostIP"`
	RUNPILOTVersion  string   `json:"RUNPILOTVersion"`
	TmuxVersion string   `json:"TmuxVersion"`
	OS          string   `json:"OS"`
	Sockets     []string `json:"Sockets"`
	Actions     []Action `json:"actions"` // v2 15.2
}

// QueueRow — строка очереди (GET /api/v1/queue, колонка WHY).
type QueueRow struct {
	Rank     int    `json:"rank"`
	Mode     string `json:"mode"`
	Class    string `json:"class"`
	Session  string `json:"session"`
	Host     string `json:"host"`
	SID      string `json:"sid"`
	Why      string `json:"why,omitempty"`
	WaitSec  int    `json:"wait_sec"`
	Attempts int    `json:"attempts"`
	Prompt   string `json:"prompt,omitempty"`
	Actions  []Action `json:"actions"` // v2 15.2
}

// ServerSlotRow — слот сервера.
type ServerSlotRow struct {
	Slot      int    `json:"slot"`
	State     string `json:"state"`
	Session   string `json:"session,omitempty"`
	TurnSince string `json:"turn_since,omitempty"`
}

// GPUCard — карта GPU (GET /api/v1/servers, gpu_cards; раздел 10 ТЗ).
type GPUCard struct {
	Index       int     `json:"index"`
	UtilPercent int     `json:"util_percent"`
	TempC       int     `json:"temp_c"`
	PowerW      int     `json:"power_w"`
}

// ServerRow — сервер (GET /api/v1/servers). Метрики Э6: nil = «—».
type ServerRow struct {
	Name     string          `json:"name"`
	Priority int             `json:"priority"`
	State    string          `json:"state"`
	Slots    int             `json:"slots"`
	Used     int             `json:"used"`
	SlotInfo []ServerSlotRow `json:"slots_info"`

	GPU   *int     `json:"gpu,omitempty"`
	KV    *float64 `json:"kv_pct,omitempty"`
	GEN   *float64 `json:"gen_tok_s,omitempty"`
	EXT   *int     `json:"ext,omitempty"`
	Missing bool   `json:"metrics_missing,omitempty"`

	GPUCards []GPUCard `json:"gpu_cards,omitempty"`
	Actions  []Action  `json:"actions"` // v2 15.2
}

// StatsServer — итоги по серверу (GET /api/v1/stats, без токенов).
type StatsServer struct {
	Server   string  `json:"server"`
	Turns    int     `json:"turns"`
	OK       int     `json:"ok"`
	Err      int     `json:"err"`
	TotalSec int64   `json:"total_sec"`
	AvgSec   float64 `json:"avg_sec"`
}

// StatsSession — итоги по сессии.
type StatsSession struct {
	Session  string `json:"session"`
	SID      string `json:"sid"`
	Turns    int    `json:"turns"`
	OK       int     `json:"ok"`
	Err      int     `json:"err"`
	TotalSec int64   `json:"total_sec"`
}

// StatsResult — ходы и длительности по серверам и сессиям.
type StatsResult struct {
	ByServer  []StatsServer  `json:"by_server"`
	BySession []StatsSession `json:"by_session"`
}

// Why — ответ GET /api/v1/sessions/{t}/why (приложение А ТЗ).
type Why struct {
	SID        string            `json:"sid"`
	State      string            `json:"state"`
	Reason     string            `json:"reason,omitempty"`
	Predicates map[string]any    `json:"predicates,omitempty"`
	Candidates []string          `json:"candidates"`
}

// EnqueueBody — тело POST /api/v1/sessions/{t}/enqueue.
type EnqueueBody struct {
	Class  string `json:"class,omitempty"`
	Pin    string `json:"pin,omitempty"`
	Prefer string `json:"prefer,omitempty"`
	Front  bool   `json:"front,omitempty"`
	After  string `json:"after,omitempty"`
}
