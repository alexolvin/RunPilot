// Package config — строгая загрузка конфигурации runpilot (раздел 13 ТЗ).
//
// Неизвестный ключ — ошибка (код выхода CLI 2). Все значения по умолчанию
// живут только в defaults.go — единственном источнике чисел.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// ErrUnknownField — в YAML есть ключ, которого нет в схеме.
var ErrUnknownField = errors.New("config: unknown field")

// ErrInvalid — схема валидна, но значения не проходят семантическую проверку.
var ErrInvalid = errors.New("config: invalid")

// Config — корень конфигурации (~/.config/runpilot/config.yaml).
//
// v2 (раздел 4): ключи разделены на bootstrap (нужны до открытия БД) и
// рабочие (в БД, редактируются из веба). Рабочие ключи — см. schema.go;
// единственный источник значений по умолчанию — defaults.go.
type Config struct {
	Coordinator Coordinator `yaml:"coordinator"`
	Scheduler   Scheduler   `yaml:"scheduler"`
	Dispatch    Dispatch    `yaml:"dispatch"`
	Turn        Turn        `yaml:"turn"`
	Gateway     Gateway     `yaml:"gateway"`
	Monitor     Monitor     `yaml:"monitor"`
	Limits      Limits      `yaml:"limits"`
	Servers     []Server    `yaml:"servers"`
	Profiles    Profiles    `yaml:"profiles"`
	Notify      Notify      `yaml:"notify"`
	Retention   Retention   `yaml:"retention"`
	Client      Client      `yaml:"client"`
	Node        Node        `yaml:"node"`
	Web         Web         `yaml:"web"`
	// v2 (раздел 4.2): новые рабочие группы.
	Faults   Faults   `yaml:"faults"`
	Enroll   Enroll   `yaml:"enroll"`
	External External `yaml:"external"`
	Jobs     Jobs     `yaml:"jobs"`
	Backup   Backup   `yaml:"backup"`
}

type Coordinator struct {
	Bind             string   `yaml:"bind"`
	AllowCIDRs       []string `yaml:"allow_cidrs"`
	GatewayPort      int      `yaml:"gateway_port"`
	APIPort          int      `yaml:"api_port"`
	GatewayURL       string   `yaml:"gateway_url"`
	TokenEnv         string   `yaml:"token_env"`
	DBPath           string   `yaml:"db_path"`
	ShutdownGraceSec int      `yaml:"shutdown_grace_sec"`
	// v2 (раздел 2.3): повтор привязки API/шлюза к coordinator.bind и
	// встроенный узел.
	BindRetrySec  int    `yaml:"bind_retry_sec"`
	BindAlertMin  int    `yaml:"bind_alert_min"`
	HeartbeatSec  int    `yaml:"heartbeat_sec"`
	DistDir       string `yaml:"dist_dir"`
	EmbeddedNode  bool   `yaml:"embedded_node"`
}

// Web — параметры веб-слушателя (v2 раздел 2.1). Только петлевой адрес:
// доверять заголовкам Tailscale-User-* можно только через tailscale serve.
//
// Bind/Port/PublicURL — bootstrap (раздел 4.1); остальные ключи — рабочие
// (раздел 4.2), живут в БД.
type Web struct {
	Bind            string `yaml:"bind"`
	Port            int    `yaml:"port"`
	PublicURL       string `yaml:"public_url"`
	SessionTTLDays  int    `yaml:"session_ttl_days"`
	LoginRatePerMin int    `yaml:"login_rate_per_min"`
	// v2 (раздел 4.2): рабочие ключи веб-подсистемы.
	ScreenIntervalMS      int `yaml:"screen_interval_ms"`
	PasteVerifySec        int `yaml:"paste_verify_sec"`
	PasteMaxKB            int `yaml:"paste_max_kb"`
	SpawnReadySec         int `yaml:"spawn_ready_sec"`
	DirsMax               int `yaml:"dirs_max"`
	SSEReplay             int `yaml:"sse_replay"`
	SSEClientsMax         int `yaml:"sse_clients_max"`
	ScreensPerClientMax   int `yaml:"screens_per_client_max"`
	TerminalsPerNodeMax   int `yaml:"terminals_per_node_max"`
	TerminalIdleMin       int `yaml:"terminal_idle_min"`
	UndoSec               int `yaml:"undo_sec"`
	PageSize              int `yaml:"page_size"`
	IdempotencyTTLSec     int `yaml:"idempotency_ttl_sec"`
	// W6 (5.1/5.3/14.2): таймауты операций управления.
	ProbeTimeoutSec  int `yaml:"probe_timeout_sec"`  // пробный запрос сервера (5.1)
	NodeOpTimeoutSec int `yaml:"node_op_timeout_sec"` // dirs / uninstall узла (5.3, 13.2)
	UpdateTimeoutSec int `yaml:"update_timeout_sec"`  // self-update узла (14.2)
}

type Scheduler struct {
	TickMS             int `yaml:"tick_ms"`
	AgingSec           int `yaml:"aging_sec"`
	PreferWaitSec      int `yaml:"prefer_wait_sec"`
	PinUnavailableSec  int `yaml:"pin_unavailable_sec"`
	AffinityTTLSec     int `yaml:"affinity_ttl_sec"`
	ResumeBackoffSec   int `yaml:"resume_backoff_sec"`
	AutoRequeueMax     int `yaml:"auto_requeue_max"`
	SnapshotMaxAgeSec  int `yaml:"snapshot_max_age_sec"`
	DispatchStableSec  int `yaml:"dispatch_stable_sec"`
	ExternalConfirmSec int `yaml:"external_confirm_sec"`
	CooldownSec        int `yaml:"cooldown_sec"`
	RecoveryWindowSec  int `yaml:"recovery_window_sec"`
}

type Dispatch struct {
	NodeReplyTimeoutSec int `yaml:"node_reply_timeout_sec"`
	StartConfirmSec     int `yaml:"start_confirm_sec"`
	ResumeKeyDelayMS    int `yaml:"resume_key_delay_ms"`
	BudgetP95MS         int `yaml:"budget_p95_ms"`
}

type Turn struct {
	DoneQuietSec         int `yaml:"done_quiet_sec"`
	DoneStableSec        int `yaml:"done_stable_sec"`
	ToolHoldMaxSec       int `yaml:"tool_hold_max_sec"`
	ApprovalHoldMaxSec   int `yaml:"approval_hold_max_sec"`
	UnknownMaxSec        int `yaml:"unknown_max_sec"`
	NodeLostReleaseSec   int `yaml:"node_lost_release_sec"`
	AutoEnqueueStableSec int `yaml:"auto_enqueue_stable_sec"`
	// v2 (раздел 4.2): период грации принудительного завершения кодера.
	KillGraceSec int `yaml:"kill_grace_sec"`
}

type Gateway struct {
	HoldMaxSec    int `yaml:"hold_max_sec"`
	MaxBodyMB     int `yaml:"max_body_mb"`
	RetryAfterSec int `yaml:"retry_after_sec"`
	MaxInflight   int `yaml:"max_inflight"`
}

// MaxBodyBytes — gateway.max_body_mb в байтах (раздел 6 ТЗ).
// Константа живёт здесь, а не в шлюзе: в internal/gateway запрещены
// числовые литералы кроме 0 и 1 (строгое правило 2).
func (g Gateway) MaxBodyBytes() int64 { return int64(g.MaxBodyMB) * mib }

// mib — мегабайт в двоичном счёте.
const mib = kibi * kibi

type Monitor struct {
	HealthIntervalSec  int `yaml:"health_interval_sec"`
	HealthTimeoutSec   int `yaml:"health_timeout_sec"`
	HealthDownAfter    int `yaml:"health_down_after"`
	HealthUpAfter      int `yaml:"health_up_after"`
	MetricsIntervalSec int `yaml:"metrics_interval_sec"`
	MetricsTimeoutSec  int `yaml:"metrics_timeout_sec"`
	RateWindowSec      int `yaml:"rate_window_sec"`
	HistoryWindowMin   int `yaml:"history_window_min"`
	// v2 (раздел 4.2): рабочие ключи мониторинга.
	ModelsRefreshSec int `yaml:"models_refresh_sec"`
	DiskFreeMinMB    int `yaml:"disk_free_min_mb"`
	DiskCheckSec     int `yaml:"disk_check_sec"`
	SafeProbeSec     int `yaml:"safe_probe_sec"`
}

type Limits struct {
	MaxSessionsPerNode   int `yaml:"max_sessions_per_node"`
	MaxQueueLength       int `yaml:"max_queue_length"`
	MaxPaneSnapshotBytes int `yaml:"max_pane_snapshot_bytes"`
	// TokenMinLen — минимальная длина RUNPILOT_TOKEN (v2 раздел 16): короче →
	// serve не стартует.
	TokenMinLen int `yaml:"token_min_len"`
}

// Server — локальный OpenAI-совместимый бэкенд (vLLM).
type Server struct {
	Name                string    `yaml:"name"`
	Priority            int       `yaml:"priority"`
	Slots               int       `yaml:"slots"`
	Accept              []string  `yaml:"accept"`
	HealthURL           string    `yaml:"health_url"`
	MetricsURL          string    `yaml:"metrics_url"`
	MaxOutputTokens     int       `yaml:"max_output_tokens"`
	FirstByteTimeoutSec int       `yaml:"first_byte_timeout_sec"`
	RequireDirect       bool      `yaml:"require_direct"`
	Upstreams           Upstreams `yaml:"upstreams"`
}

type Upstreams struct {
	OpenAI UpstreamOpenAI `yaml:"openai"`
}

type UpstreamOpenAI struct {
	URL    string `yaml:"url"`
	Model  string `yaml:"model"`
	KeyEnv string `yaml:"key_env"`
}

type Profiles struct {
	Qwen ProfileQwen `yaml:"qwen"`
}

// ProfileQwen — профиль агента qwen (единственный в этой редакции).
type ProfileQwen struct {
	ModelAlias string `yaml:"model_alias"`
}

type Notify struct {
	Telegram Telegram `yaml:"telegram"`
	// v2 (раздел 4.2): рабочие ключи оповещений о событиях.
	Alerts Alerts `yaml:"alerts"`
}

type Telegram struct {
	// BotTokenEnv — bootstrap (раздел 4.1): имя переменной с секретом.
	BotTokenEnv string `yaml:"bot_token_env"`
	// v2 (раздел 4.2): рабочие ключи.
	ChatIDs     []string `yaml:"chat_ids"`
	MinTurnSec  int      `yaml:"min_turn_sec"`
	CoalesceSec int      `yaml:"coalesce_sec"`
	Kinds       []string `yaml:"kinds"`
}

// Alerts — рабочие ключи оповещений (v2 раздел 4.2).
type Alerts struct {
	QueueWaitMin    int `yaml:"queue_wait_min"`
	WaitUISec       int `yaml:"wait_ui_sec"`
	PromptRemindMin int `yaml:"prompt_remind_min"`
}

type Retention struct {
	RequestsDays     int `yaml:"requests_days"`
	EventsDays       int `yaml:"events_days"`
	GoneSessionsDays int `yaml:"gone_sessions_days"`
	// v2 (раздел 4.2): сроки хранения рабочих данных.
	AuditDays         int `yaml:"audit_days"`
	NotificationsDays int `yaml:"notifications_days"`
	ExternalDays      int `yaml:"external_days"`
}

type Client struct {
	Coordinator string `yaml:"coordinator"`
	TokenEnv    string `yaml:"token_env"`
}

// Node — параметры узла. Host/TmuxSockets/GPU/GPUServer — bootstrap
// (раздел 4.1); ScanIntervalSec и ниже — рабочие (раздел 4.2).
type Node struct {
	Host            string   `yaml:"host"`
	TmuxSockets     []string `yaml:"tmux_sockets"`
	ScanIntervalSec int      `yaml:"scan_interval_sec"`
	GPU             string   `yaml:"gpu"`
	GPUServer       string   `yaml:"gpu_server"`
	// v2 (раздел 4.2): рабочие ключи.
	OfflineAfterSec     int      `yaml:"offline_after_sec"`
	HealthIntervalSec   int      `yaml:"health_interval_sec"`
	ProjectRootsDefault []string `yaml:"project_roots_default"`
}

// Faults — параметры обработки отказов серверов (v2 раздел 4.2).
type Faults struct {
	WindowSec       int `yaml:"window_sec"`
	QuarantineCount int `yaml:"quarantine_count"`
	QuarantineSec   int `yaml:"quarantine_sec"`
	FlapCount       int `yaml:"flap_count"`
	FlapWindowSec   int `yaml:"flap_window_sec"`
}

// Enroll — параметры подключения и самообновления узлов (v2 раздел 4.2).
type Enroll struct {
	TokenTTLMin        int `yaml:"token_ttl_min"`
	UpdateRetryMin     int `yaml:"update_retry_min"`
	SelftestTimeoutSec int `yaml:"selftest_timeout_sec"`
}

// External — обработка внешних процессов (v2 раздел 4.2).
type External struct {
	Detect       bool `yaml:"detect"`
	KillGraceSec int  `yaml:"kill_grace_sec"`
}

// Jobs — задания cron (runpilot exec, v2 раздел 4.2).
type Jobs struct {
	HeartbeatSec        int `yaml:"heartbeat_sec"`
	HeartbeatTimeoutSec int `yaml:"heartbeat_timeout_sec"`
	WaitMaxDefaultSec   int `yaml:"wait_max_default_sec"`
	// Slots — максимум одновременных JOB-аренд (раздел 8.1, CONTROL 6).
	Slots int `yaml:"slots"`
}

// Backup — копии БД (v2 раздел 4.2).
type Backup struct {
	// DailyAtUTC — время ежедневной копии в UTC, формат "HH:MM".
	DailyAtUTC string `yaml:"daily_at_utc"`
	Keep       int    `yaml:"keep"`
}

// DefaultPath — путь конфигурации машины по умолчанию.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "runpilot", "config.yaml"), nil
}

// Load читает конфиг из path; если файла нет — только defaults.
// Разбор строгий: KnownFields(true), неизвестный ключ — ErrUnknownField.
func Load(path string) (*Config, error) {
	cfg := Defaults()
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return cfg, cfg.Validate()
	case err != nil:
		return nil, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil {
		var terr *yaml.TypeError
		if errors.As(err, &terr) {
			for _, msg := range terr.Errors {
				if strings.Contains(msg, "not found in type") {
					return nil, fmt.Errorf("%w: %s", ErrUnknownField, strings.Join(terr.Errors, "; "))
				}
			}
			return nil, fmt.Errorf("%w: %s", ErrInvalid, strings.Join(terr.Errors, "; "))
		}
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return cfg, cfg.Validate()
}

// Validate — семантические проверки. Числа берёт из конфига, не из литералов
// (проверяются свойства, а не пороговые значения).
func (c *Config) Validate() error {
	var problems []string

	if len(c.Coordinator.AllowCIDRs) == 0 {
		problems = append(problems, "coordinator.allow_cidrs пуст после загрузки defaults")
	} else if _, err := ParseCIDRs(c.Coordinator.AllowCIDRs); err != nil {
		problems = append(problems, fmt.Sprintf("coordinator.allow_cidrs: %v", err))
	}

	ports := map[string]int{
		"coordinator.gateway_port": c.Coordinator.GatewayPort,
		"coordinator.api_port":     c.Coordinator.APIPort,
		"web.port":                 c.Web.Port,
	}
	for name, p := range ports {
		if p < 0 || p > maxPort {
			problems = append(problems, name+" вне диапазона портов")
		}
	}

	positive := map[string]int{
		"scheduler.tick_ms":               c.Scheduler.TickMS,
		"scheduler.aging_sec":             c.Scheduler.AgingSec,
		"scheduler.prefer_wait_sec":       c.Scheduler.PreferWaitSec,
		"scheduler.pin_unavailable_sec":   c.Scheduler.PinUnavailableSec,
		"scheduler.affinity_ttl_sec":      c.Scheduler.AffinityTTLSec,
		"scheduler.resume_backoff_sec":    c.Scheduler.ResumeBackoffSec,
		"scheduler.auto_requeue_max":      c.Scheduler.AutoRequeueMax,
		"scheduler.snapshot_max_age_sec":  c.Scheduler.SnapshotMaxAgeSec,
		"scheduler.dispatch_stable_sec":   c.Scheduler.DispatchStableSec,
		"scheduler.external_confirm_sec":  c.Scheduler.ExternalConfirmSec,
		"scheduler.cooldown_sec":          c.Scheduler.CooldownSec,
		"scheduler.recovery_window_sec":   c.Scheduler.RecoveryWindowSec,
		"dispatch.node_reply_timeout_sec": c.Dispatch.NodeReplyTimeoutSec,
		"dispatch.start_confirm_sec":      c.Dispatch.StartConfirmSec,
		"dispatch.resume_key_delay_ms":    c.Dispatch.ResumeKeyDelayMS,
		"dispatch.budget_p95_ms":          c.Dispatch.BudgetP95MS,
		"turn.done_quiet_sec":             c.Turn.DoneQuietSec,
		"turn.done_stable_sec":            c.Turn.DoneStableSec,
		"turn.tool_hold_max_sec":          c.Turn.ToolHoldMaxSec,
		"turn.approval_hold_max_sec":      c.Turn.ApprovalHoldMaxSec,
		"turn.unknown_max_sec":            c.Turn.UnknownMaxSec,
		"turn.node_lost_release_sec":      c.Turn.NodeLostReleaseSec,
		"turn.auto_enqueue_stable_sec":    c.Turn.AutoEnqueueStableSec,
		"gateway.hold_max_sec":            c.Gateway.HoldMaxSec,
		"gateway.max_body_mb":             c.Gateway.MaxBodyMB,
		"gateway.retry_after_sec":         c.Gateway.RetryAfterSec,
		"gateway.max_inflight":            c.Gateway.MaxInflight,
		"monitor.health_interval_sec":     c.Monitor.HealthIntervalSec,
		"monitor.health_timeout_sec":      c.Monitor.HealthTimeoutSec,
		"monitor.health_down_after":       c.Monitor.HealthDownAfter,
		"monitor.health_up_after":         c.Monitor.HealthUpAfter,
		"monitor.metrics_interval_sec":    c.Monitor.MetricsIntervalSec,
		"monitor.metrics_timeout_sec":     c.Monitor.MetricsTimeoutSec,
		"monitor.rate_window_sec":         c.Monitor.RateWindowSec,
		"monitor.history_window_min":      c.Monitor.HistoryWindowMin,
		"limits.max_sessions_per_node":    c.Limits.MaxSessionsPerNode,
		"limits.max_queue_length":         c.Limits.MaxQueueLength,
		"limits.max_pane_snapshot_bytes":  c.Limits.MaxPaneSnapshotBytes,
		"retention.requests_days":         c.Retention.RequestsDays,
		"retention.events_days":           c.Retention.EventsDays,
		"retention.gone_sessions_days":    c.Retention.GoneSessionsDays,
		"notify.telegram.min_turn_sec":    c.Notify.Telegram.MinTurnSec,
		"notify.telegram.coalesce_sec":    c.Notify.Telegram.CoalesceSec,
		"node.scan_interval_sec":          c.Node.ScanIntervalSec,
		"coordinator.shutdown_grace_sec":  c.Coordinator.ShutdownGraceSec,
		"coordinator.bind_retry_sec":      c.Coordinator.BindRetrySec,
		"coordinator.bind_alert_min":      c.Coordinator.BindAlertMin,
		"coordinator.heartbeat_sec":       c.Coordinator.HeartbeatSec,
		"limits.token_min_len":            c.Limits.TokenMinLen,
		"web.session_ttl_days":            c.Web.SessionTTLDays,
		"web.login_rate_per_min":          c.Web.LoginRatePerMin,
		// v2 (раздел 4.2): новые рабочие ключи.
		"turn.kill_grace_sec":            c.Turn.KillGraceSec,
		"monitor.models_refresh_sec":     c.Monitor.ModelsRefreshSec,
		"monitor.disk_free_min_mb":       c.Monitor.DiskFreeMinMB,
		"monitor.disk_check_sec":         c.Monitor.DiskCheckSec,
		"monitor.safe_probe_sec":         c.Monitor.SafeProbeSec,
		"faults.window_sec":              c.Faults.WindowSec,
		"faults.quarantine_count":        c.Faults.QuarantineCount,
		"faults.quarantine_sec":          c.Faults.QuarantineSec,
		"faults.flap_count":              c.Faults.FlapCount,
		"faults.flap_window_sec":         c.Faults.FlapWindowSec,
		"web.screen_interval_ms":         c.Web.ScreenIntervalMS,
		"web.paste_verify_sec":           c.Web.PasteVerifySec,
		"web.paste_max_kb":               c.Web.PasteMaxKB,
		"web.spawn_ready_sec":            c.Web.SpawnReadySec,
		"web.dirs_max":                   c.Web.DirsMax,
		"web.sse_replay":                 c.Web.SSEReplay,
		"web.sse_clients_max":            c.Web.SSEClientsMax,
		"web.screens_per_client_max":     c.Web.ScreensPerClientMax,
		"web.terminals_per_node_max":     c.Web.TerminalsPerNodeMax,
		"web.terminal_idle_min":          c.Web.TerminalIdleMin,
		"web.undo_sec":                   c.Web.UndoSec,
		"web.page_size":                  c.Web.PageSize,
		"web.idempotency_ttl_sec":        c.Web.IdempotencyTTLSec,
		"enroll.token_ttl_min":           c.Enroll.TokenTTLMin,
		"enroll.update_retry_min":        c.Enroll.UpdateRetryMin,
		"enroll.selftest_timeout_sec":    c.Enroll.SelftestTimeoutSec,
		"node.offline_after_sec":         c.Node.OfflineAfterSec,
		"node.health_interval_sec":       c.Node.HealthIntervalSec,
		"external.kill_grace_sec":        c.External.KillGraceSec,
		"jobs.heartbeat_sec":             c.Jobs.HeartbeatSec,
		"jobs.heartbeat_timeout_sec":     c.Jobs.HeartbeatTimeoutSec,
		"jobs.wait_max_default_sec":      c.Jobs.WaitMaxDefaultSec,
		"jobs.slots":                     c.Jobs.Slots,
		"notify.alerts.queue_wait_min":   c.Notify.Alerts.QueueWaitMin,
		"notify.alerts.wait_ui_sec":      c.Notify.Alerts.WaitUISec,
		"notify.alerts.prompt_remind_min": c.Notify.Alerts.PromptRemindMin,
		"retention.audit_days":           c.Retention.AuditDays,
		"retention.notifications_days":   c.Retention.NotificationsDays,
		"retention.external_days":        c.Retention.ExternalDays,
		"backup.keep":                    c.Backup.Keep,
	}
	for name, v := range positive {
		if v <= 0 {
			problems = append(problems, name+" должен быть > 0")
		}
	}

	if c.Profiles.Qwen.ModelAlias == "" {
		problems = append(problems, "profiles.qwen.model_alias пуст")
	}
	if c.Node.GPU != "none" && c.Node.GPU != "nvidia" && c.Node.GPU != "amd" {
		problems = append(problems, "node.gpu должен быть none|nvidia|amd")
	}
	if !validClockHM(c.Backup.DailyAtUTC) {
		problems = append(problems, `backup.daily_at_utc должен быть в формате "HH:MM"`)
	}

	// Раздел 4.5: перекрёстные проверки.
	if c.Turn.DoneQuietSec >= c.Dispatch.StartConfirmSec {
		problems = append(problems, "turn.done_quiet_sec должен быть меньше dispatch.start_confirm_sec")
	}
	if c.Turn.DoneStableSec >= c.Dispatch.StartConfirmSec {
		problems = append(problems, "turn.done_stable_sec должен быть меньше dispatch.start_confirm_sec")
	}

	seen := map[string]bool{}
	for i, s := range c.Servers {
		if s.Name == "" {
			problems = append(problems, fmt.Sprintf("servers[%d].name пуст", i))
		} else if !validServerName(s.Name) {
			problems = append(problems, fmt.Sprintf("servers[%d].name %q не соответствует шаблону", i, s.Name))
		} else if seen[s.Name] {
			problems = append(problems, "servers: дублирующееся имя "+s.Name)
		}
		seen[s.Name] = true
		if s.Slots < 1 {
			problems = append(problems, s.Name+": slots < 1")
		}
		if len(s.Accept) == 0 {
			problems = append(problems, s.Name+": accept пуст")
		}
		for _, a := range s.Accept {
			if a != "resume" && a != "high" && a != "normal" && a != "low" {
				problems = append(problems, s.Name+": accept содержит "+a)
			}
		}
		if s.Upstreams.OpenAI.URL == "" || s.Upstreams.OpenAI.Model == "" || s.Upstreams.OpenAI.KeyEnv == "" {
			problems = append(problems, s.Name+": upstreams.openai требует url, model и key_env")
		} else if !isAbsoluteHTTPURL(s.Upstreams.OpenAI.URL) {
			problems = append(problems, s.Name+": upstreams.openai.url должен быть абсолютным http/https")
		}
		// Раздел 4.5: gateway.hold_max_sec < first_byte_timeout_sec каждого сервера.
		if s.FirstByteTimeoutSec > 0 && c.Gateway.HoldMaxSec >= s.FirstByteTimeoutSec {
			problems = append(problems, s.Name+": gateway.hold_max_sec должен быть меньше first_byte_timeout_sec")
		}
	}

	if len(problems) > 0 {
		return fmt.Errorf("%w: %s", ErrInvalid, strings.Join(problems, "; "))
	}
	return nil
}

// serverNameRe — шаблон имени сервера (v2 раздел 3.4).
var serverNameRe = regexp.MustCompile(serverNamePattern)

// validServerName — имя соответствует шаблону раздела 3.4.
func validServerName(s string) bool { return serverNameRe.MatchString(s) }

// validClockHM — строка в формате "HH:MM" с валидными часами и минутами.
func validClockHM(s string) bool {
	parts := strings.Split(s, ":")
	if len(parts) != timeFieldsInHM {
		return false
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return false
	}
	return h >= 0 && h < hoursPerDay && m >= 0 && m < minutesPerHour
}

// isAbsoluteHTTPURL — абсолютный URL с схемой http или https.
func isAbsoluteHTTPURL(s string) bool {
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return false
	}
	return u.Scheme == "http" || u.Scheme == "https"
}

// ParseCIDRs разбирает список CIDR.
func ParseCIDRs(list []string) ([]*net.IPNet, error) {
	out := make([]*net.IPNet, 0, len(list))
	for _, s := range list {
		_, ipnet, err := net.ParseCIDR(s)
		if err != nil {
			return nil, fmt.Errorf("некорректный CIDR %q: %v", s, err)
		}
		out = append(out, ipnet)
	}
	return out, nil
}

// BindInCIDRs — входит ли адрес в один из CIDR.
func BindInCIDRs(bind string, cidrs []*net.IPNet) bool {
	ip := net.ParseIP(bind)
	if ip == nil {
		return false
	}
	for _, c := range cidrs {
		if c.Contains(ip) {
			return true
		}
	}
	return false
}

// ValidateBind — запуск serve отклоняется, если bind вне allow_cidrs (ТЗ раздел 13).
func (c *Config) ValidateBind() error {
	if c.Coordinator.Bind == "" {
		return fmt.Errorf("%w: coordinator.bind пуст", ErrInvalid)
	}
	cidrs, err := ParseCIDRs(c.Coordinator.AllowCIDRs)
	if err != nil {
		return err
	}
	if !BindInCIDRs(c.Coordinator.Bind, cidrs) {
		return fmt.Errorf("%w: coordinator.bind %s не входит ни в один CIDR из coordinator.allow_cidrs",
			ErrInvalid, c.Coordinator.Bind)
	}
	return nil
}

// IsLoopback — петлевой ли IP-адрес (раздел 2.1 v2).
func IsLoopback(s string) bool {
	ip := net.ParseIP(s)
	return ip != nil && ip.IsLoopback()
}

// ValidateWeb — запуск serve отклоняется, если веб-слушатель не петлевой
// (раздел 2.1 v2: заголовкам Tailscale-User-* можно доверять только через
// единственное петлевое отверстие tailscale serve).
func (c *Config) ValidateWeb() error {
	if c.Web.Bind == "" {
		return fmt.Errorf("%w: web.bind пуст", ErrInvalid)
	}
	if !IsLoopback(c.Web.Bind) {
		return fmt.Errorf("%w: web.bind %s не петлевой (веб — только через tailscale serve)",
			ErrInvalid, c.Web.Bind)
	}
	return nil
}

// ValidateToken — пустой или короче limits.token_min_len токен (v2 раздел 16):
// serve не стартует. token — значение из env (token_env).
func (c *Config) ValidateToken(token string) error {
	if token == "" {
		return fmt.Errorf("%w: токен оператора пуст — локальный режим запрещён", ErrInvalid)
	}
	if len(token) < c.Limits.TokenMinLen {
		return fmt.Errorf("%w: токен короче limits.token_min_len (%d)",
			ErrInvalid, c.Limits.TokenMinLen)
	}
	return nil
}

// ExpandPath раскрывает "~" в начале пути.
func ExpandPath(p string) (string, error) {
	if !strings.HasPrefix(p, "~") {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, strings.TrimPrefix(p, "~")), nil
}

// DBPath — абсолютный путь к файлу БД.
func (c *Config) DBPath() (string, error) {
	return ExpandPath(c.Coordinator.DBPath)
}
