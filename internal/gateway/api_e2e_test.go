package gateway

// Этап 5 ТЗ: сценарии API-клиентов шлюза (a–g), сырой вывод с меткой Z.
// В одном процессе: два фейковых vLLM, координатор (store + планировщик +
// шлюз), API-клиент. Реальный MoxHome не используется.
//
// Запуск: go test -run TestAPIE2E ./internal/gateway/ -v

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"runpilot/internal/clock"
	"runpilot/internal/config"
	"runpilot/internal/model"
	"runpilot/internal/proto"
	"runpilot/internal/scheduler"
	"runpilot/internal/store"
)

func Z(t *testing.T, label string, a ...any) {
	t.Logf("Z %s: %v", label, a)
	fmt.Printf("Z %s: %v\n", label, a)
}

// --- фейковый vLLM ---

type apiE2EFakeLLM struct {
	*httptest.Server
	mu       sync.Mutex
	model    string
	body     []byte
	down     bool
	delay    time.Duration
	requests int
}

func newAPIE2EFakeLLM(model string) *apiE2EFakeLLM {
	f := &apiE2EFakeLLM{model: model}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", f.handleHealth)
	mux.HandleFunc("/v1/models", f.handleModels)
	mux.HandleFunc("/v1/chat/completions", f.handleChat)
	f.Server = httptest.NewServer(mux)
	return f
}

func (f *apiE2EFakeLLM) handleHealth(w http.ResponseWriter, r *http.Request) {
	if f.isDown() {
		http.Error(w, "down", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{"status":"ok"}`)
}

func (f *apiE2EFakeLLM) handleModels(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"object":"list","data":[{"id":%q,"object":"model"}]}`, f.model)
}

func (f *apiE2EFakeLLM) handleChat(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.body = raw
	f.requests++
	down, delay, mdl := f.down, f.delay, f.model
	f.mu.Unlock()
	if down {
		if hj, ok := w.(http.Hijacker); ok {
			if conn, _, err := hj.Hijack(); err == nil {
				_ = conn.Close()
				return
			}
		}
		http.Error(w, "down", http.StatusBadGateway)
		return
	}
	if delay > 0 {
		time.Sleep(delay)
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"id":"gen-1","object":"chat.completion","model":%q,"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`, mdl)
}

func (f *apiE2EFakeLLM) setDown(d bool)             { f.mu.Lock(); f.down = d; f.mu.Unlock() }
func (f *apiE2EFakeLLM) isDown() bool               { f.mu.Lock(); defer f.mu.Unlock(); return f.down }
func (f *apiE2EFakeLLM) setDelay(d time.Duration)   { f.mu.Lock(); f.delay = d; f.mu.Unlock() }
func (f *apiE2EFakeLLM) reqCount() int              { f.mu.Lock(); defer f.mu.Unlock(); return f.requests }
func (f *apiE2EFakeLLM) lastBody() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]byte, len(f.body))
	copy(out, f.body)
	return out
}

// --- адаптеры планировщика ---

type apiE2EGwViews struct{ gw *Servers }

func (a apiE2EGwViews) List() []scheduler.ServerView {
	out := []scheduler.ServerView{}
	for _, s := range a.gw.All() {
		out = append(out, scheduler.ServerView{
			Name: s.Cfg.Name, Priority: s.Cfg.Priority, Slots: s.Cfg.Slots,
			Accept: s.Cfg.Accept, State: s.State(),
		})
	}
	return out
}

type apiE2ENoopPanes struct{}

func (apiE2ENoopPanes) PaneBySID(string) (scheduler.PaneSnap, bool)   { return scheduler.PaneSnap{}, false }
func (apiE2ENoopPanes) PaneByPaneID(string) (scheduler.PaneSnap, bool) { return scheduler.PaneSnap{}, false }

// --- запрос к шлюзу ---

type apiE2EChatResp struct {
	Model   string `json:"model"`
	Choices []struct {
		Message      struct{ Content string `json:"content"` } `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

func apiE2EPost(gwURL, key, body string) (int, string, apiE2EChatResp) {
	req, _ := http.NewRequest(http.MethodPost, gwURL+"/v1/chat/completions",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := http.DefaultClient.Do(req)
	var ra string
	var cr apiE2EChatResp
	if err == nil {
		ra = resp.Header.Get("Retry-After")
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		_ = json.Unmarshal(b, &cr)
		return resp.StatusCode, ra, cr
	}
	return 0, ra, cr
}

func apiE2ESrvCfg(name string, priority, slots int, url string) config.Server {
	return config.Server{
		Name: name, Priority: priority, Slots: slots,
		Accept:    []string{"resume", "high", "normal", "low"},
		HealthURL: url + "/health",
		Upstreams: config.Upstreams{OpenAI: config.UpstreamOpenAI{
			URL: url, Model: "srv-model",
		}},
	}
}

func apiE2ECountGrants(st *store.Store) int {
	evs, _ := st.EventList(0, 100000)
	n := 0
	for _, e := range evs {
		if e.Kind == model.KindLeaseGrant {
			n++
		}
	}
	return n
}

func apiE2EExtractField(body []byte, field string) []byte {
	k := []byte(`"` + field + `":"`)
	i := bytes.Index(body, k)
	if i < 0 {
		return nil
	}
	rest := body[i+len(k):]
	if j := bytes.IndexByte(rest, '"'); j >= 0 {
		return rest[:j]
	}
	return rest
}

func apiE2EOccupy(st *store.Store, srvs ...string) {
	now := time.Now().UTC()
	for _, srv := range srvs {
		_ = st.CreateSession(store.SessionRecord{
			SID: "occ-" + srv, Name: "occ-" + srv, Host: "t", HostIP: "127.0.0.1",
			TmuxSession: "occ", PaneID: "%1", Profile: "qwen",
			State: model.SessionRunning, StateChangedAt: now,
			Class: model.ClassNormal, CreatedAt: now,
		})
		_, _ = st.LeaseCreateIfAbsent(model.Lease{
			SID: "occ-" + srv, Server: srv, Slot: 1,
			State: model.LeaseActive, Origin: model.LeaseOriginImplicit, GrantedAt: now,
		}, 1, false)
	}
}

func apiE2EFree(st *store.Store, srvs ...string) {
	now := time.Now().UTC()
	for _, srv := range srvs {
		_ = st.LeaseRelease("occ-"+srv, "TURN_DONE", now)
	}
}

func TestAPIE2E(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := store.Open(filepath.Join(t.TempDir(), "runpilot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	a := newAPIE2EFakeLLM("srv-model")
	b := newAPIE2EFakeLLM("srv-model")
	defer a.Close()
	defer b.Close()

	cfg := config.Defaults()
	cfg.Servers = []config.Server{
		apiE2ESrvCfg("a", 10, 1, a.URL),
		apiE2ESrvCfg("b", 5, 1, b.URL),
	}
	cfg.Gateway.MaxInflight = 256
	cfg.Scheduler.AgingSec = 2

	clk := clock.NewReal()
	gw, err := NewGateway(cfg, st, clk, log)
	if err != nil {
		t.Fatal(err)
	}
	sch := scheduler.New(scheduler.Options{
		Cfg: cfg, Store: st, Clk: clk,
		Servers: apiE2EGwViews{gw: gw.Servers()},
		Panes:   apiE2ENoopPanes{},
		Dispatch: func(context.Context, string, proto.Msg) (proto.Msg, error) {
			return proto.Msg{}, nil
		},
		Wake: gw.Wake, Log: log,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sch.Start(ctx)

	gwSrv := httptest.NewServer(gw.Handler())
	defer gwSrv.Close()

	key, err := st.CreateApiClient(store.ApiClient{
		Name: "moxhome", Enabled: true, MaxHoldSec: 3, StickyIdleSec: 20, AgingSec: 2,
	})
	if err != nil {
		t.Fatal(err)
	}

	// --- a: оба свободны ---
	Z(t, "=== a: оба сервера свободны ===")
	code, _, cr := apiE2EPost(gwSrv.URL, key,
		`{"model":"any","max_tokens":10,"chat_template_kwargs":{"enable_thinking":false}}`)
	Z(t, "a.status", code)
	Z(t, "a.model", cr.Model)
	if len(cr.Choices) > 0 {
		Z(t, "a.content", cr.Choices[0].Message.Content)
		Z(t, "a.finish_reason", cr.Choices[0].FinishReason)
	}
	Z(t, "a.usage", cr.Usage.PromptTokens, cr.Usage.CompletionTokens)
	up := a.lastBody()
	Z(t, "a.upstream_has_chat_template_kwargs", bytes.Contains(up, []byte("chat_template_kwargs")))
	Z(t, "a.upstream_model", string(apiE2EExtractField(up, "model")))

	// --- f: ошибки доступа ---
	Z(t, "=== f: ошибки доступа ===")
	code, _, _ = apiE2EPost(gwSrv.URL, "rp_wrong", `{"model":"any"}`)
	Z(t, "f.wrong_key", code)
	code, _, _ = apiE2EPost(gwSrv.URL, key, `{"model":"any","stream":true}`)
	Z(t, "f.stream", code)
	_ = st.SetApiClientEnabled("moxhome", false)
	code, _, _ = apiE2EPost(gwSrv.URL, key, `{"model":"any"}`)
	Z(t, "f.disabled", code)
	_ = st.SetApiClientEnabled("moxhome", true)

	// --- b: оба заняты → 503 + Retry-After, без записи в очереди ---
	// Снимаем липкую аренду сценария a (иначе запрос возьмёт свой слот).
	Z(t, "=== b: оба сервера заняты ===")
	_ = st.LeaseRelease(store.APISessionSID("moxhome"), "TURN_DONE", time.Now().UTC())
	apiE2EOccupy(st, "a", "b")
	t0 := time.Now()
	code, ra, _ := apiE2EPost(gwSrv.URL, key, `{"model":"any"}`)
	Z(t, "b.status", code)
	Z(t, "b.retry_after", ra)
	Z(t, "b.waited_sec", time.Since(t0).Seconds())
	ql, _ := st.QueueList()
	Z(t, "b.queue_len", len(ql))
	apiE2EFree(st, "a", "b")

	// --- c: 200 запросов, липкая аренда → выдачи ≪ 200 ---
	Z(t, "=== c: 200 запросов, липкая аренда ===")
	gBefore := apiE2ECountGrants(st)
	ok := 0
	for i := 0; i < 200; i++ {
		c, _, _ := apiE2EPost(gwSrv.URL, key, `{"model":"any","max_tokens":10}`)
		if c == http.StatusOK {
			ok++
		}
	}
	gAfter := apiE2ECountGrants(st)
	Z(t, "c.ok", ok)
	Z(t, "c.lease_grants", gAfter-gBefore)
	Z(t, "c.upstream_requests_a", a.reqCount())

	// --- g: сервер упал в запросе → 502 + аренда снята ---
	Z(t, "=== g: сервер упал в запросе ===")
	a.setDown(true)
	code, ra, _ = apiE2EPost(gwSrv.URL, key, `{"model":"any"}`)
	Z(t, "g.status", code)
	Z(t, "g.retry_after", ra)
	a.setDown(false)
	lease, _ := st.LeaseGet(store.APISessionSID("moxhome"))
	Z(t, "g.lease_after", lease != nil)

	// --- d: кодер в середине серии получает сервер ---
	Z(t, "=== d: кодер в середине серии ===")
	apiE2EOccupy(st, "b")
	a.setDelay(2 * time.Second)
	done := make(chan int, 1)
	go func() { c, _, _ := apiE2EPost(gwSrv.URL, key, `{"model":"any"}`); done <- c }()
	time.Sleep(500 * time.Millisecond)
	now := time.Now().UTC()
	_ = st.CreateSession(store.SessionRecord{
		SID: "coder-d", Name: "coder-d", Host: "t", HostIP: "127.0.0.1",
		TmuxSession: "coder-d", PaneID: "%1", Profile: "qwen", Kind: model.KindJob,
		State: model.SessionQueued, StateChangedAt: now,
		Class: model.ClassNormal, CreatedAt: now,
	})
	_ = st.QueueUpsert(model.QueueEntry{
		SID: "coder-d", Class: model.ClassNormal, EnqueuedAt: now, NotBefore: now,
		Constraint: model.Constraint{Kind: model.ConstraintNone}, Mode: model.QueueModeSubmit,
	})
	<-done
	got := "NO_LEASE"
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if l, err := st.LeaseGet("coder-d"); err == nil && l != nil {
			got = l.Server
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	Z(t, "d.coder_server", got)
	a.setDelay(0)
	_ = st.LeaseRelease("coder-d", model.ReleaseTurnDone, time.Now().UTC())
	apiE2EFree(st, "b")

	// --- e: aging — BATCH после aging_sec получает кодерный ход ---
	Z(t, "=== e: aging BATCH → кодерный ход ===")
	_ = st.LeaseRelease(store.APISessionSID("moxhome"), "TURN_DONE", time.Now().UTC())
	apiE2EOccupy(st, "a", "b")
	done2 := make(chan int, 1)
	go func() { c, _, _ := apiE2EPost(gwSrv.URL, key, `{"model":"any"}`); done2 <- c }()
	time.Sleep(2000 * time.Millisecond)
	apiE2EFree(st, "a")
	c := <-done2
	Z(t, "e.status", c)
	apiE2EFree(st, "b")

	Z(t, "=== готово ===")
}
