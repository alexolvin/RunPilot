// Package fakellm — тестовый OpenAI-совместимый сервер (раздел 15 ТЗ):
// SSE chat/completions, управляемые задержки, отказы до и после первого
// байта, /metrics с управляемыми running/waiting. Anthropic-режим не
// реализовывается.
package fakellm

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"
)

// Server — fakellm: httptest-сервер + управляемое поведение.
type Server struct {
	*httptest.Server
	mux *http.ServeMux

	model string

	mu             sync.Mutex
	failBeforeN    int
	failBeforeMode string // "" | "502" | "503" | "504" | "conn" | "post"
	firstByteDelay time.Duration
	chunkPause     time.Duration
	chunks         int
	healthDown     bool

	// /metrics в формате vLLM: управляемые running/waiting/kv + счётчики,
	// растущие на генерирующих запросах (для монитора и doctor, Э6).
	metricsRunning    int
	metricsWaiting    int
	metricsKV         float64
	genTokensTotal    float64
	promptTokensTotal float64

	requests    int
	sseRequests int
	// lastModel — модель из тела последнего генерирующего запроса
	// (проверка подмены шлюзом).
	lastModel string
	// lastAuth — Authorization последнего запроса (проверка ключа шлюза).
	lastAuth string
	// lastMaxTokens / lastMaxCompletionTokens — из последнего тела
	// (проверка ограничения max_output_tokens).
	lastMaxTokens         int
	lastMaxCompletionTok  int
	sawStreamOptions      bool
}

// New — fakellm без listener (встраивание в чужой mux: runpilot-fakellm).
func New(model string) *Server {
	s := &Server{model: model, chunks: defaultChunks}
	s.mux = http.NewServeMux()
	s.mux.HandleFunc("/", s.handle)
	return s
}

// Handler — http.Handler fakellm.
func (s *Server) Handler() http.Handler { return s.mux }

// Start — fakellm с моделью model на случайном порту.
func Start(model string) *Server {
	s := New(model)
	s.Server = httptest.NewServer(s.mux)
	return s
}

// SetModel — модель для /v1/models.
func (s *Server) SetModel(m string) {
	s.mu.Lock()
	s.model = m
	s.mu.Unlock()
}

// SetFailBefore — следующие n генерирующих запросов отказывают ДО
// первого байта: "502"/"503"/"504" — HTTP-статус, "conn" — обрыв
// соединения. n = 0 — выключить.
func (s *Server) SetFailBefore(n int, mode string) {
	s.mu.Lock()
	s.failBeforeN = n
	s.failBeforeMode = mode
	s.mu.Unlock()
}

// SetFailAfter — следующие n запросов отказывают ПОСЛЕ первого байта
// (обрыв посреди SSE). n = 0 — выключить.
func (s *Server) SetFailAfter(n int) {
	s.mu.Lock()
	s.failBeforeN = n
	s.failBeforeMode = "post"
	s.mu.Unlock()
}

// SetDelays — задержка до первого байта и между SSE-чанками.
func (s *Server) SetDelays(firstByte, chunkPause time.Duration, chunks int) {
	s.mu.Lock()
	s.firstByteDelay = firstByte
	s.chunkPause = chunkPause
	if chunks > 0 {
		s.chunks = chunks
	}
	s.mu.Unlock()
}

// SetHealthDown — /health отвечает 503 (для проверки миграции: сервер
// «мёртвый»).
func (s *Server) SetHealthDown(down bool) {
	s.mu.Lock()
	s.healthDown = down
	s.mu.Unlock()
}

// SetMetrics — /metrics: управляемые num_requests_running /
// num_requests_waiting и kv_cache_usage_perc (монитор и doctor, Э6).
func (s *Server) SetMetrics(running, waiting int, kv float64) {
	s.mu.Lock()
	s.metricsRunning = running
	s.metricsWaiting = waiting
	s.metricsKV = kv
	s.mu.Unlock()
}

// Requests — число принятых генерирующих запросов.
func (s *Server) Requests() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests
}

// SSEChunks — число SSE-запросов (stream: true).
func (s *Server) SSERequests() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sseRequests
}

// LastModel — модель из тела последнего генерирующего запроса.
func (s *Server) LastModel() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastModel
}

// LastAuth — Authorization последнего запроса.
func (s *Server) LastAuth() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastAuth
}

// LastMaxTokens — max_tokens/max_completion_tokens последнего тела.
func (s *Server) LastMaxTokens() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastMaxTokens, s.lastMaxCompletionTok
}

// SawStreamOptions — встречался ли stream_options в теле запросов.
func (s *Server) SawStreamOptions() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sawStreamOptions
}

// handle — маршрутизация fakellm.
func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/health":
		s.mu.Lock()
		down := s.healthDown
		s.mu.Unlock()
		if down {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"ok"}`)

	case r.URL.Path == "/v1/models":
		s.mu.Lock()
		model := s.model
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"object":"list","data":[{"id":%q,"object":"model"}]}`, model)

	case r.URL.Path == "/metrics":
		s.mu.Lock()
		model, running, waiting, kv := s.model, s.metricsRunning, s.metricsWaiting, s.metricsKV
		gen, prompt := s.genTokensTotal, s.promptTokensTotal
		s.mu.Unlock()
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		l := fmt.Sprintf(`engine="0",model_name=%q`, model)
		fmt.Fprintf(w,
			"# TYPE vllm:num_requests_running gauge\nvllm:num_requests_running{%s} %d\n"+
				"# TYPE vllm:num_requests_waiting gauge\nvllm:num_requests_waiting{%s} %d\n"+
				"# TYPE vllm:kv_cache_usage_perc gauge\nvllm:kv_cache_usage_perc{%s} %g\n"+
				"# TYPE vllm:generation_tokens_total counter\nvllm:generation_tokens_total{%s} %g\n"+
				"# TYPE vllm:prompt_tokens_total counter\nvllm:prompt_tokens_total{%s} %g\n",
			l, running, l, waiting, l, kv, l, gen, l, prompt)

	case (r.URL.Path == "/v1/chat/completions" || r.URL.Path == "/v1/completions" || r.URL.Path == "/v1/responses") && r.Method == http.MethodPost:
		s.serveCompletion(w, r)

	default:
		http.Error(w, "not found", http.StatusNotFound)
	}
}

// serveCompletion — генерирующий запрос fakellm.
func (s *Server) serveCompletion(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Model             string         `json:"model"`
		Stream            bool           `json:"stream"`
		MaxTokens         *int           `json:"max_tokens"`
		MaxCompletionTok  *int           `json:"max_completion_tokens"`
		StreamOptions     map[string]any `json:"stream_options"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	s.mu.Lock()
	s.requests++
	if body.Stream {
		s.sseRequests++
	}
	// Псевдо-токены для /metrics (монитор считает rate по счётчикам).
	s.genTokensTotal += genTokensPerReq
	s.promptTokensTotal += promptTokensPerReq
	s.lastModel = body.Model
	s.lastAuth = r.Header.Get("Authorization")
	if body.MaxTokens != nil {
		s.lastMaxTokens = *body.MaxTokens
	}
	if body.MaxCompletionTok != nil {
		s.lastMaxCompletionTok = *body.MaxCompletionTok
	}
	if body.StreamOptions != nil {
		s.sawStreamOptions = true
	}
	failN := s.failBeforeN
	failMode := s.failBeforeMode
	firstDelay := s.firstByteDelay
	chunkPause := s.chunkPause
	chunks := s.chunks
	s.mu.Unlock()

	if failN > 0 {
		s.mu.Lock()
		s.failBeforeN--
		s.mu.Unlock()
		if failMode != "post" {
			s.failBefore(w, failMode)
			return
		}
	}

	if firstDelay > 0 {
		time.Sleep(firstDelay)
	}

	if body.Stream {
		if failMode == "post" && failN > 0 {
			// Отказ после начала: часть чанков, затем обрыв.
			s.writeSSE(w, chunks/chunkHalfDiv+1, chunkPause)
			hj, ok := w.(http.Hijacker)
			if ok {
				if conn, _, err := hj.Hijack(); err == nil {
					_ = conn.Close()
				}
			}
			return
		}
		s.writeSSE(w, chunks, chunkPause)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"id":"gen-1","object":"chat.completion","model":%q,"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`, body.Model)
}

// writeSSE — chunks чанков SSE (формат OpenAI), с паузой между ними.
func (s *Server) writeSSE(w http.ResponseWriter, chunks int, pause time.Duration) {
	flusher, _ := w.(http.Flusher)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	for i := 0; i < chunks; i++ {
		if i > 0 && pause > 0 {
			time.Sleep(pause)
		}
		fmt.Fprintf(w, "data: {\"id\":\"gen-1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"c%d\"},\"finish_reason\":null}]}\n\n", i)
		if flusher != nil {
			flusher.Flush()
		}
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

// failBefore — отказ ДО первого байта.
func (s *Server) failBefore(w http.ResponseWriter, mode string) {
	switch mode {
	case "502", "503", "504":
		code, _ := parseCode(mode)
		http.Error(w, "upstream failure", code)
	case "conn":
		// Обрыв соединения до первого байта.
		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "conn failure", http.StatusBadGateway)
			return
		}
		if conn, _, err := hj.Hijack(); err == nil {
			_ = conn.Close()
		}
	default:
		http.Error(w, "failure", http.StatusBadGateway)
	}
}

// parseCode — "502"/"503"/"504" → HTTP-статус (константы net/http).
func parseCode(mode string) (int, error) {
	switch mode {
	case "502":
		return http.StatusBadGateway, nil
	case "503":
		return http.StatusServiceUnavailable, nil
	case "504":
		return http.StatusGatewayTimeout, nil
	}
	return 0, fmt.Errorf("fakellm: неизвестный режим %q", mode)
}

// BaseURL — "http://127.0.0.1:port" (для upstreams.openai.url).
func (s *Server) BaseURL() string {
	return s.URL
}
