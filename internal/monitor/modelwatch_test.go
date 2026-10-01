package monitor

// Наблюдение моделей (v2 S7/S8): checkModels → GET /v1/models → список
// моделей в наблюдателе; не-200/не-JSON → без вызова.

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"runpilot/internal/clock"
	"runpilot/internal/config"
)

type obsFunc func(name string, models []string)

func (f obsFunc) OnModels(name string, models []string) { f(name, models) }

type ctxFunc func(name string, maxModelLen int)

func (f ctxFunc) OnModelContext(name string, maxModelLen int) { f(name, maxModelLen) }

type extFunc func(name string, ext int)

func (f extFunc) SetExternal(name string, ext int) { f(name, ext) }

func TestCheckModelsCallsObserver(t *testing.T) {
	h := newMHarn(t, 2)
	h.fetch.modelsStatus = 200
	h.fetch.modelsBody = `{"data":[{"id":"m1"},{"id":"m2"}]}`
	var gotName string
	var gotModels []string
	h.mon.SetModelObserver(obsFunc(func(name string, models []string) {
		gotName, gotModels = name, models
	}))

	h.mon.checkModels("s")
	if gotName != "s" || len(gotModels) != 2 || gotModels[0] != "m1" || gotModels[1] != "m2" {
		t.Fatalf("observer: name=%s models=%v, хочу s [m1 m2]", gotName, gotModels)
	}

	// не-200 → наблюдатель не вызывается.
	h.fetch.modelsStatus = 500
	gotModels = nil
	h.mon.checkModels("s")
	if gotModels != nil {
		t.Fatalf("не-200: наблюдатель вызван (models=%v)", gotModels)
	}

	// не-JSON → наблюдатель не вызывается.
	h.fetch.modelsStatus = 200
	h.fetch.modelsBody = "not json"
	h.mon.checkModels("s")
	if gotModels != nil {
		t.Fatalf("не-JSON: наблюдатель вызван (models=%v)", gotModels)
	}
}

// TestCheckModelsSendsBearer — item 3: /v1/models шлётся с bearer из KeyEnv
// (vLLM отвечает 401 без ключа), и max_model_len настроенной модели →
// ContextObserver.
func TestCheckModelsSendsBearer(t *testing.T) {
	t.Setenv("MONITOR_TEST_KEY", "secret")
	cfg := config.Defaults()
	cfg.Servers = []config.Server{{
		Name: "s", HealthURL: "http://127.0.0.1:1/health",
		MetricsURL: "http://127.0.0.1:1/metrics",
		Upstreams: config.Upstreams{OpenAI: config.UpstreamOpenAI{
			URL: "http://127.0.0.1:1", Model: "m", KeyEnv: "MONITOR_TEST_KEY"}},
	}}
	fetch := &fakeFetcher{modelsStatus: 200,
		modelsBody: `{"data":[{"id":"m","max_model_len":262144}]}`}
	var gotCtx int
	mon := New(cfg, clock.NewVirtual(time.Now()), fetch.get,
		extFunc(func(string, int) {}), &fakeSink{}, fakeInfl{0},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	mon.SetModelObserver(obsFunc(func(string, []string) {}))
	mon.SetContextObserver(ctxFunc(func(name string, maxModelLen int) { gotCtx = maxModelLen }))
	mon.checkModels("s")
	if fetch.lastBearer != "Bearer secret" {
		t.Fatalf("bearer=%q, хочу \"Bearer secret\" (иначе 401)", fetch.lastBearer)
	}
	if gotCtx != 262144 {
		t.Fatalf("context=%d, хочу 262144", gotCtx)
	}
}

// TestCheckModelsNoKey — KeyEnv пуст/не задан → bearer пустой (fetch без аутха).
func TestCheckModelsNoKey(t *testing.T) {
	cfg := config.Defaults()
	cfg.Servers = []config.Server{{
		Name: "s", HealthURL: "http://127.0.0.1:1/health",
		MetricsURL: "http://127.0.0.1:1/metrics",
		Upstreams: config.Upstreams{OpenAI: config.UpstreamOpenAI{
			URL: "http://127.0.0.1:1", Model: "m", KeyEnv: "MONITOR_UNSET_KEY"}},
	}}
	fetch := &fakeFetcher{modelsStatus: 200, modelsBody: `{"data":[{"id":"m"}]}`}
	mon := New(cfg, clock.NewVirtual(time.Now()), fetch.get,
		extFunc(func(string, int) {}), &fakeSink{}, fakeInfl{0},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	mon.SetModelObserver(obsFunc(func(string, []string) {}))
	mon.checkModels("s")
	if fetch.lastBearer != "" {
		t.Fatalf("bearer=%q, хочу пустой (KeyEnv не задан)", fetch.lastBearer)
	}
}
