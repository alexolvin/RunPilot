package config

import (
	"strings"
	"testing"
)

// Строки 4.4 «момент применения»: схема указывает верный apply_when для
// каждой группы. Строки 5–7 (gateway-запросы, model_alias, web) покрываются
// метаданными схемы, по которой веб строит формы.
func TestSchemaApplyWhen(t *testing.T) {
	byPath := map[string]SettingField{}
	for _, f := range Schema() {
		byPath[f.Path] = f
	}
	cases := []struct {
		path string
		want ApplyWhen
	}{
		// Строка 1: scheduler/monitor/faults/notify — со следующего тика.
		{"scheduler.aging_sec", ApplyNextTick},
		{"monitor.health_interval_sec", ApplyNextTick},
		{"faults.quarantine_sec", ApplyNextTick},
		{"notify.telegram.min_turn_sec", ApplyNextTick},
		{"notify.alerts.queue_wait_min", ApplyNextTick},
		// Строка 2: dispatch/turn — для новых диспетчеризаций.
		{"dispatch.start_confirm_sec", ApplyNewDispatch},
		{"turn.done_quiet_sec", ApplyNewDispatch},
		{"turn.kill_grace_sec", ApplyNewDispatch},
		// Строка 5 (шлюз): gateway — для новых запросов.
		{"gateway.hold_max_sec", ApplyNewRequest},
		// Строка 6: profiles.qwen.model_alias — для новых сессий.
		{"profiles.qwen.model_alias", ApplyNewSession},
		// Строка 7: web — для новых соединений и сессий браузера.
		{"web.session_ttl_days", ApplyNewConn},
		{"web.screen_interval_ms", ApplyNewConn},
		{"web.paste_max_kb", ApplyNewConn},
	}
	for _, c := range cases {
		f, ok := byPath[c.path]
		if !ok {
			t.Fatalf("нет поля %s в схеме", c.path)
		}
		if f.ApplyWhen != c.want {
			t.Errorf("%s: apply_when = %q, ждём %q", c.path, f.ApplyWhen, c.want)
		}
	}
}

// Все ключи gateway.* — ApplyNewRequest, все web.* (рабочие) — ApplyNewConn.
func TestSchemaApplyWhenGroups(t *testing.T) {
	for _, f := range Schema() {
		switch {
		case strings.HasPrefix(f.Path, "gateway."):
			if f.ApplyWhen != ApplyNewRequest {
				t.Errorf("%s: gateway должен быть new_request, а %q", f.Path, f.ApplyWhen)
			}
		case strings.HasPrefix(f.Path, "web."):
			if f.ApplyWhen != ApplyNewConn {
				t.Errorf("%s: web должен быть new_conn, а %q", f.Path, f.ApplyWhen)
			}
		case strings.HasPrefix(f.Path, "dispatch."):
			if f.ApplyWhen != ApplyNewDispatch {
				t.Errorf("%s: dispatch должен быть new_dispatch, а %q", f.Path, f.ApplyWhen)
			}
		case strings.HasPrefix(f.Path, "turn."):
			if f.ApplyWhen != ApplyNewDispatch {
				t.Errorf("%s: turn должен быть new_dispatch, а %q", f.Path, f.ApplyWhen)
			}
		}
	}
}
