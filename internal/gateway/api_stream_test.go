package gateway

// Стриминг API-клиентов (allow_stream): SSE-поток насквозь, учёт токенов
// из usage-чанка, usage-чанк с пустым choices клиенту не передаётся.
//
// Запуск: go test -run TestAPIStream ./internal/gateway/

import (
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

	"runpilot/internal/clock"
	"runpilot/internal/config"
	"runpilot/internal/store"
)

// --- фейковый vLLM со стримингом ---

type apiStreamFakeLLM struct {
	*httptest.Server
	mu       sync.Mutex
	model    string
	split    bool // первый чанк в двух записях (тест pending-буфера)
	down     bool
	body     []byte
	count    int
}

func newAPIStreamFakeLLM(model string) *apiStreamFakeLLM {
	f := &apiStreamFakeLLM{model: model}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", f.handleHealth)
	mux.HandleFunc("/v1/models", f.handleModels)
	mux.HandleFunc("/v1/chat/completions", f.handleChat)
	f.Server = httptest.NewServer(mux)
	return f
}

func (f *apiStreamFakeLLM) handleHealth(w http.ResponseWriter, r *http.Request) {
	if f.isDown() {
		http.Error(w, "down", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{"status":"ok"}`)
}

func (f *apiStreamFakeLLM) handleModels(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"object":"list","data":[{"id":%q,"object":"model"}]}`, f.model)
}

func (f *apiStreamFakeLLM) chunkJSON(model string, choices, usage string) string {
	return `{"id":"gen-1","object":"chat.completion.chunk","model":` +
		fmt.Sprintf("%q", model) + `,"choices":` + choices + usage + `}`
}

func (f *apiStreamFakeLLM) handleChat(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.body = raw
	f.count++
	split, down, model := f.split, f.down, f.model
	f.mu.Unlock()
	if down {
		http.Error(w, "down", http.StatusServiceUnavailable)
		return
	}
	var req struct {
		Stream bool `json:"stream"`
	}
	_ = json.Unmarshal(raw, &req)
	if !req.Stream {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id":"gen-1","object":"chat.completion","model":%q,"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`, model)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	fl := w.(http.Flusher)
	ev1 := f.chunkJSON(model, `[{"index":0,"delta":{"content":"He"}}]`, ``)
	ev2 := f.chunkJSON(model, `[{"index":0,"delta":{"content":"llo"},"finish_reason":"stop"}]`, ``)
	evUsage := f.chunkJSON(model, `[]`, `,"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18}`)
	if split {
		// Первый чанк в двух записях: тест pending-буфера streamingWriter.
		line := "data: " + ev1 + "\n\n"
		half := len(line) / 2
		_, _ = io.WriteString(w, line[:half])
		fl.Flush()
		_, _ = io.WriteString(w, line[half:])
		fl.Flush()
	} else {
		fmt.Fprintf(w, "data: %s\n\n", ev1)
		fl.Flush()
	}
	fmt.Fprintf(w, "data: %s\n\n", ev2)
	fl.Flush()
	fmt.Fprintf(w, "data: %s\n\n", evUsage)
	fl.Flush()
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
	fl.Flush()
}

func (f *apiStreamFakeLLM) setSplit(v bool) { f.mu.Lock(); f.split = v; f.mu.Unlock() }
func (f *apiStreamFakeLLM) setDown(d bool)  { f.mu.Lock(); f.down = d; f.mu.Unlock() }
func (f *apiStreamFakeLLM) isDown() bool    { f.mu.Lock(); defer f.mu.Unlock(); return f.down }
func (f *apiStreamFakeLLM) lastBody() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]byte, len(f.body))
	copy(out, f.body)
	return out
}

// apiStreamPost — POST /v1/chat/completions, тело ответа целиком (SSE).
func apiStreamPost(gwURL, key, body string) (int, string, string) {
	req, _ := http.NewRequest(http.MethodPost, gwURL+"/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, "", ""
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode, string(b), resp.Header.Get("Retry-After")
}

func TestAPIStream(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := store.Open(filepath.Join(t.TempDir(), "runpilot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	fake := newAPIStreamFakeLLM("srv-model")
	defer fake.Close()

	cfg := config.Defaults()
	cfg.Servers = []config.Server{apiE2ESrvCfg("a", 10, 1, fake.URL)}
	cfg.Gateway.MaxInflight = 256

	clk := clock.NewReal()
	gw, err := NewGateway(cfg, st, clk, log)
	if err != nil {
		t.Fatal(err)
	}
	gwSrv := httptest.NewServer(gw.Handler())
	defer gwSrv.Close()

	key, err := st.CreateApiClient(store.ApiClient{
		Name: "streamer", Enabled: true, MaxHoldSec: 3, StickyIdleSec: 0, AgingSec: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	enableStream := func() {
		c, _ := st.GetApiClient("streamer")
		c.AllowStream = true
		if _, err := st.SetApiClient(c); err != nil {
			t.Fatal(err)
		}
	}

	// --- a: stream=true, allow_stream=false → 400 stream_not_enabled ---
	t.Log("=== a: stream без allow_stream → 400 ===")
	code, body, _ := apiStreamPost(gwSrv.URL, key, `{"model":"any","stream":true}`)
	t.Logf("a.status=%d", code)
	if code != http.StatusBadRequest || !strings.Contains(body, "stream_not_enabled") {
		t.Fatalf("a: хочу 400 stream_not_enabled, получил %d %q", code, body)
	}

	// --- b: stream=true, allow_stream=true → 200, поток, usage скрыт, учёт ---
	t.Log("=== b: stream с allow_stream → 200, учёт из usage-чанка ===")
	enableStream()
	code, body, _ = apiStreamPost(gwSrv.URL, key, `{"model":"any","stream":true,"max_tokens":50}`)
	t.Logf("b.status=%d", code)
	if code != http.StatusOK {
		t.Fatalf("b: хочу 200, получил %d %q", code, body)
	}
	if !strings.Contains(body, `"content":"He"`) || !strings.Contains(body, `"content":"llo"`) {
		t.Fatalf("b: чанки не дошли: %q", body)
	}
	if !strings.Contains(body, "[DONE]") {
		t.Fatalf("b: нет [DONE]: %q", body)
	}
	if strings.Contains(body, `"usage"`) {
		t.Fatalf("b: usage-чанк ушёл клиенту (не просил include_usage): %q", body)
	}
	up := fake.lastBody()
	if !strings.Contains(string(up), `"include_usage":true`) {
		t.Fatalf("b: апстрим не получил stream_options.include_usage: %q", up)
	}
	if !strings.Contains(string(up), `"max_tokens":50`) {
		t.Fatalf("b: max_tokens клиента потерян: %q", up)
	}
	// Аренда снята (StickyIdleSec=0) — слот не сиротский.
	if _, err := st.LeaseGet(store.APISessionSID("streamer")); err == nil {
		t.Fatal("b: аренда не снята после стримингового запроса")
	}
	daily, _ := st.ApiRequestsDaily(clk.Now())
	if d := daily["streamer"]; d.PromptTokens != 11 || d.CompletionTokens != 7 {
		t.Fatalf("b: учёт токенов: хочу 11/7, получил %d/%d", d.PromptTokens, d.CompletionTokens)
	}

	// --- b2: чанк, разрезанный между записями (pending-буфер) ---
	t.Log("=== b2: чанк в двух записях апстрима ===")
	fake.setSplit(true)
	code, body, _ = apiStreamPost(gwSrv.URL, key, `{"model":"any","stream":true}`)
	fake.setSplit(false)
	t.Logf("b2.status=%d", code)
	if code != http.StatusOK || !strings.Contains(body, `"content":"He"`) {
		t.Fatalf("b2: разрезанный чанк не собрался: %d %q", code, body)
	}
	if strings.Contains(body, `"usage"`) {
		t.Fatalf("b2: usage-чанк ушёл клиенту: %q", body)
	}

	// --- c: клиент сам просит include_usage → usage-чанк передаётся ---
	t.Log("=== c: include_usage от клиента → usage-чанк насквозь ===")
	code, body, _ = apiStreamPost(gwSrv.URL, key,
		`{"model":"any","stream":true,"stream_options":{"include_usage":true}}`)
	t.Logf("c.status=%d", code)
	if code != http.StatusOK {
		t.Fatalf("c: хочу 200, получил %d %q", code, body)
	}
	if !strings.Contains(body, `"usage":{"prompt_tokens":11`) {
		t.Fatalf("c: usage-чанк не передан клиенту: %q", body)
	}

	// --- d: non-streaming без изменений (регресс) ---
	t.Log("=== d: non-streaming — регресс ===")
	code, body, _ = apiStreamPost(gwSrv.URL, key, `{"model":"any","max_tokens":10}`)
	t.Logf("d.status=%d", code)
	if code != http.StatusOK || !strings.Contains(body, `"prompt_tokens":10`) {
		t.Fatalf("d: non-streaming сломан: %d %q", code, body)
	}
	up = fake.lastBody()
	if strings.Contains(string(up), `stream_options`) {
		t.Fatalf("d: stream_options не должен ставиться в non-streaming: %q", up)
	}

	// --- e: апстрим 503 до первого байта → 502 + Retry-After ---
	t.Log("=== e: апстрим упал → 502 + Retry-After ===")
	fake.setDown(true)
	code, _, ra := apiStreamPost(gwSrv.URL, key, `{"model":"any","stream":true}`)
	fake.setDown(false)
	t.Logf("e.status=%d e.retry_after=%s", code, ra)
	if code != http.StatusBadGateway || ra == "" {
		t.Fatalf("e: хочу 502 + Retry-After, получил %d %q", code, ra)
	}
	// Аренда снята после prefail.
	if _, err := st.LeaseGet(store.APISessionSID("streamer")); err == nil {
		t.Fatal("e: аренда не снята после prefail")
	}

	// --- итог: учёт за 24ч (b + b2 + c + d) ---
	daily, _ = st.ApiRequestsDaily(clk.Now())
	d := daily["streamer"]
	t.Logf("итог: запросов=%d prompt=%d completion=%d", d.Requests, d.PromptTokens, d.CompletionTokens)
	if d.PromptTokens != 43 || d.CompletionTokens != 26 {
		t.Fatalf("итог: хочу 43/26, получил %d/%d", d.PromptTokens, d.CompletionTokens)
	}
	t.Log("=== готово ===")
}
