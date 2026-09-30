package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func writeTmp(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadUnknownKey(t *testing.T) {
	p := writeTmp(t, "bad.yaml", "coordinator:\n  bind: 127.0.0.1\n  wrong_key: 1\n")
	_, err := Load(p)
	if !errors.Is(err, ErrUnknownField) {
		t.Fatalf("ожидали ErrUnknownField, получили: %v", err)
	}
}

func TestLoadTypeMismatch(t *testing.T) {
	p := writeTmp(t, "badtype.yaml", "coordinator:\n  gateway_port: \"не число\"\n")
	_, err := Load(p)
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("ожидали ErrInvalid, получили: %v", err)
	}
}

func TestLoadMissingFileUsesDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
	if err != nil {
		t.Fatalf("нет файла — должны работать defaults: %v", err)
	}
	if cfg.Coordinator.GatewayPort != 8787 || cfg.Coordinator.APIPort != 8788 {
		t.Fatalf("defaults: %d %d", cfg.Coordinator.GatewayPort, cfg.Coordinator.APIPort)
	}
}

func TestLoadExampleFromTZ(t *testing.T) {
	// Пример координатора из раздела 13 ТЗ должен пройти строгий разбор.
	p := filepath.Join("..", "..", "testdata", "config.example.yaml")
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("пример из ТЗ: %v", err)
	}
	if len(cfg.Servers) != 2 {
		t.Fatalf("servers = %d", len(cfg.Servers))
	}
	s0 := cfg.Servers[0]
	if s0.Name != "node-a" || s0.Priority != 100 || s0.Slots != 1 || !s0.RequireDirect {
		t.Fatalf("node-a: %+v", s0)
	}
	if s0.Upstreams.OpenAI.Model != "qwen3.6-27b" || s0.Upstreams.OpenAI.KeyEnv != "RUNPILOT_KEY_VLLM" {
		t.Fatalf("upstream node-a: %+v", s0.Upstreams.OpenAI)
	}
	if cfg.Scheduler.TickMS != 500 || cfg.Scheduler.AutoRequeueMax != 3 {
		t.Fatalf("scheduler: %+v", cfg.Scheduler)
	}
	if cfg.Retention.GoneSessionsDays != 7 {
		t.Fatalf("retention: %+v", cfg.Retention)
	}
}

// configKeys — все ключи структуры Config (v2: bootstrap раздел 4.1 +
// рабочие раздел 4.2): топ-уровень, вложенные и листы.
var configKeys = []string{
	"coordinator", "coordinator.bind", "coordinator.allow_cidrs",
	"coordinator.gateway_port", "coordinator.api_port", "coordinator.gateway_url",
	"coordinator.token_env", "coordinator.db_path", "coordinator.shutdown_grace_sec",
	"coordinator.bind_retry_sec", "coordinator.bind_alert_min", "coordinator.heartbeat_sec",
	"coordinator.dist_dir", "coordinator.embedded_node",
	"scheduler", "scheduler.tick_ms", "scheduler.aging_sec", "scheduler.prefer_wait_sec",
	"scheduler.pin_unavailable_sec", "scheduler.affinity_ttl_sec", "scheduler.resume_backoff_sec",
	"scheduler.auto_requeue_max", "scheduler.snapshot_max_age_sec", "scheduler.dispatch_stable_sec",
	"scheduler.external_confirm_sec", "scheduler.cooldown_sec", "scheduler.recovery_window_sec",
	"dispatch", "dispatch.node_reply_timeout_sec", "dispatch.start_confirm_sec",
	"dispatch.resume_key_delay_ms", "dispatch.budget_p95_ms",
	"turn", "turn.done_quiet_sec", "turn.done_stable_sec", "turn.tool_hold_max_sec",
	"turn.approval_hold_max_sec", "turn.unknown_max_sec", "turn.node_lost_release_sec",
	"turn.auto_enqueue_stable_sec", "turn.kill_grace_sec",
	"gateway", "gateway.hold_max_sec", "gateway.max_body_mb", "gateway.retry_after_sec",
	"gateway.max_inflight",
	"monitor", "monitor.health_interval_sec", "monitor.health_timeout_sec",
	"monitor.health_down_after", "monitor.health_up_after", "monitor.metrics_interval_sec",
	"monitor.metrics_timeout_sec", "monitor.rate_window_sec", "monitor.history_window_min",
	"monitor.models_refresh_sec", "monitor.disk_free_min_mb", "monitor.disk_check_sec",
	"monitor.safe_probe_sec",
	"limits", "limits.max_sessions_per_node", "limits.max_queue_length",
	"limits.max_pane_snapshot_bytes", "limits.token_min_len",
	"servers", "servers.name", "servers.priority", "servers.slots", "servers.accept",
	"servers.health_url", "servers.metrics_url", "servers.max_output_tokens",
	"servers.first_byte_timeout_sec", "servers.require_direct", "servers.upstreams",
	"servers.upstreams.openai", "servers.upstreams.openai.url", "servers.upstreams.openai.model",
	"servers.upstreams.openai.key_env",
	"profiles", "profiles.qwen", "profiles.qwen.model_alias",
	"notify", "notify.telegram", "notify.telegram.bot_token_env", "notify.telegram.chat_ids",
	"notify.telegram.min_turn_sec", "notify.telegram.coalesce_sec", "notify.telegram.kinds",
	"notify.alerts", "notify.alerts.queue_wait_min", "notify.alerts.wait_ui_sec",
	"notify.alerts.prompt_remind_min",
	"retention", "retention.requests_days", "retention.events_days",
	"retention.gone_sessions_days", "retention.audit_days",
	"retention.notifications_days", "retention.external_days",
	"client", "client.coordinator", "client.token_env",
	"node", "node.host", "node.tmux_sockets", "node.scan_interval_sec", "node.gpu",
	"node.gpu_server", "node.offline_after_sec", "node.health_interval_sec",
	"node.project_roots_default",
	"web", "web.bind", "web.port", "web.public_url",
	"web.session_ttl_days", "web.login_rate_per_min",
	"web.screen_interval_ms", "web.paste_verify_sec", "web.paste_max_kb",
	"web.spawn_ready_sec", "web.dirs_max", "web.sse_replay", "web.sse_clients_max",
	"web.screens_per_client_max", "web.terminals_per_node_max", "web.terminal_idle_min",
	"web.undo_sec", "web.page_size", "web.idempotency_ttl_sec",
	"web.probe_timeout_sec", "web.node_op_timeout_sec", "web.update_timeout_sec",
	"faults", "faults.window_sec", "faults.quarantine_count", "faults.quarantine_sec",
	"faults.flap_count", "faults.flap_window_sec",
	"enroll", "enroll.token_ttl_min", "enroll.update_retry_min",
	"enroll.selftest_timeout_sec",
	"external", "external.detect", "external.kill_grace_sec",
	"jobs", "jobs.slots", "jobs.heartbeat_sec", "jobs.heartbeat_timeout_sec", "jobs.wait_max_default_sec",
	"backup", "backup.daily_at_utc", "backup.keep",
}

// flattenYAMLKeys — рекурсивно собирает yaml-пути полей структуры.
func flattenYAMLKeys(t reflect.Type, prefix string) []string {
	var out []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		if tag == "-" {
			continue
		}
		path := tag
		if prefix != "" {
			path = prefix + "." + tag
		}
		ft := f.Type
		if ft.Kind() == reflect.Slice || ft.Kind() == reflect.Ptr {
			ft = ft.Elem()
		}
		if ft.Kind() == reflect.Struct {
			out = append(out, path)
			out = append(out, flattenYAMLKeys(ft, path)...)
		} else {
			out = append(out, path)
		}
	}
	return out
}

func TestDefaultsCoverSection13(t *testing.T) {
	got := map[string]bool{}
	for _, k := range flattenYAMLKeys(reflect.TypeOf(Config{}), "") {
		got[k] = true
	}
	want := map[string]bool{}
	for _, k := range configKeys {
		want[k] = true
	}
	var missing, extra []string
	for k := range want {
		if !got[k] {
			missing = append(missing, k)
		}
	}
	for k := range got {
		if !want[k] {
			extra = append(extra, k)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 || len(extra) > 0 {
		t.Fatalf("расхождение с разделом 13: отсутствуют %v, лишние %v", missing, extra)
	}
}

func TestValidateBind(t *testing.T) {
	cfg := Defaults()
	if err := cfg.ValidateBind(); err != nil {
		t.Fatalf("bind по умолчанию: %v", err)
	}
	cfg.Coordinator.Bind = "192.0.2.10"
	cfg.Coordinator.AllowCIDRs = append(cfg.Coordinator.AllowCIDRs, "192.0.2.0/24")
	if err := cfg.ValidateBind(); err != nil {
		t.Fatalf("192.0.2.10 в 192.0.2.0/24: %v", err)
	}
	cfg.Coordinator.Bind = "8.8.8.8"
	if err := cfg.ValidateBind(); err == nil {
		t.Fatal("публичный IP вне allow_cidrs должен отклоняться")
	}
}

func TestEmptyAllowCIDRsRejected(t *testing.T) {
	cfg := Defaults()
	cfg.Coordinator.AllowCIDRs = nil
	err := cfg.Validate()
	if err == nil || !errors.Is(err, ErrInvalid) {
		t.Fatalf("пустой allow_cidrs: %v", err)
	}
}

func TestValidateServers(t *testing.T) {
	cfg := Defaults()
	cfg.Servers = []Server{{
		Name: "s1", Slots: 1, Accept: []string{"high"},
		Upstreams: Upstreams{OpenAI: UpstreamOpenAI{URL: "http://x", Model: "m", KeyEnv: "K"}},
	}}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("валидный сервер: %v", err)
	}
	cfg.Servers[0].Accept = []string{"weird"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("неизвестный класс в accept должен отклоняться")
	}
}
