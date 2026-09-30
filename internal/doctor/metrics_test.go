package doctor

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"runpilot/internal/config"
)

// minimalVLLM — тело со всеми пятью required-метриками.
const minimalVLLM = `
# TYPE vllm:num_requests_running gauge
vllm:num_requests_running{engine="0"} 3
# TYPE vllm:num_requests_waiting gauge
vllm:num_requests_waiting{engine="0"} 1
# TYPE vllm:kv_cache_usage_perc gauge
vllm:kv_cache_usage_perc{engine="0"} 0.4
# TYPE vllm:generation_tokens_total counter
vllm:generation_tokens_total{engine="0"} 100
# TYPE vllm:prompt_tokens_total counter
vllm:prompt_tokens_total{engine="0"} 200
`

// serverCfg — конфиг с одним сервером.
func serverCfg(t *testing.T, metricsURL, openaiURL, model string, direct bool) *config.Config {
	t.Helper()
	cfg := config.Defaults()
	cfg.Servers = []config.Server{{
		Name: "s1",
		MetricsURL: metricsURL,
		RequireDirect: direct,
		Upstreams: config.Upstreams{
			OpenAI: config.UpstreamOpenAI{URL: openaiURL, Model: model},
		},
	}}
	return cfg
}

func TestCheckMetricsAllPresent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(minimalVLLM))
	}))
	defer srv.Close()
	r := checkMetrics(&Context{Cfg: serverCfg(t, srv.URL+"/metrics", "http://127.0.0.1:1", "m", false)})
	if r.Level != PASS {
		t.Fatalf("%s", r)
	}
}

func TestCheckMetricsMissingMetric(t *testing.T) {
	body := strings.Replace(minimalVLLM, "vllm:generation_tokens_total{engine=\"0\"} 100\n", "", 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	r := checkMetrics(&Context{Cfg: serverCfg(t, srv.URL+"/metrics", "http://127.0.0.1:1", "m", false)})
	if r.Level != WARN {
		t.Fatalf("%s", r)
	}
	if !strings.Contains(r.Detail, "vllm:generation_tokens_total") {
		t.Fatalf("в detail должен быть отсутствующий метрик: %q", r.Detail)
	}
}

func TestCheckMetricsUnreachable(t *testing.T) {
	r := checkMetrics(&Context{Cfg: serverCfg(t, "http://127.0.0.1:1/metrics", "http://127.0.0.1:1", "m", false)})
	if r.Level != WARN {
		t.Fatalf("%s", r)
	}
	if !strings.Contains(r.Detail, "metrics_missing") {
		t.Fatalf("в detail должен быть флаг metrics_missing: %q", r.Detail)
	}
}

func TestCheckMetricsNoServers(t *testing.T) {
	r := checkMetrics(&Context{Cfg: config.Defaults()})
	if r.Level != PASS {
		t.Fatalf("%s", r)
	}
}

func TestCheckRequireDirectOK(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"m1"}]}`))
	}))
	defer up.Close()
	r := checkRequireDirect(&Context{Cfg: serverCfg(t, "", up.URL, "m1", true)})
	if r.Level != PASS {
		t.Fatalf("%s", r)
	}
}

func TestCheckRequireDirectRedirect(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer target.Close()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer up.Close()
	r := checkRequireDirect(&Context{Cfg: serverCfg(t, "", up.URL, "m1", true)})
	if r.Level != WARN {
		t.Fatalf("%s", r)
	}
	if !strings.Contains(r.Detail, "редирект") {
		t.Fatalf("в detail должен быть редирект: %q", r.Detail)
	}
}

func TestCheckRequireDirectTwoModels(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"a"},{"id":"b"}]}`))
	}))
	defer up.Close()
	r := checkRequireDirect(&Context{Cfg: serverCfg(t, "", up.URL, "m1", true)})
	if r.Level != WARN {
		t.Fatalf("%s", r)
	}
	if !strings.Contains(r.Detail, "2 моделей") {
		t.Fatalf("в detail должно быть 2 моделей: %q", r.Detail)
	}
}

func TestCheckRequireDirectWrongModel(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"other"}]}`))
	}))
	defer up.Close()
	r := checkRequireDirect(&Context{Cfg: serverCfg(t, "", up.URL, "m1", true)})
	if r.Level != WARN {
		t.Fatalf("%s", r)
	}
	if !strings.Contains(r.Detail, "other") {
		t.Fatalf("в detail должна быть модель: %q", r.Detail)
	}
}

func TestCheckRequireDirectSkipped(t *testing.T) {
	r := checkRequireDirect(&Context{Cfg: serverCfg(t, "", "http://127.0.0.1:1", "m1", false)})
	if r.Level != PASS {
		t.Fatalf("%s", r)
	}
}
