package fakellm

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"runpilot/internal/monitor"
)

// TestSetDelaysPerRequest — P.1 (раздел 15 ТЗ): задержки фиксируются на запрос
// в момент приёма; смена настроек на уже идущие запросы не влияет. Запрос 1
// (firstByte=500мс) уходит в полёт, затем настройки меняются на 0: запрос 2
// идёт мгновенно, а запрос 1 всё равно ~500мс — каждый получил свои задержки.
func TestSetDelaysPerRequest(t *testing.T) {
	s := Start("fake-model")
	defer s.Close()

	body := `{"model":"fake-model","stream":true,"messages":[{"role":"user","content":"hi"}]}`
	firstByte := func() (time.Duration, error) {
		req, err := http.NewRequest(http.MethodPost, s.URL+"/v1/chat/completions", strings.NewReader(body))
		if err != nil {
			return 0, err
		}
		req.Header.Set("Content-Type", "application/json")
		t0 := time.Now()
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return 0, err
		}
		defer resp.Body.Close()
		buf := make([]byte, 1)
		if _, err := resp.Body.Read(buf); err != nil {
			return 0, err
		}
		return time.Since(t0), nil
	}

	// Запрос 1 при firstByte=500мс: 500 фиксится при его приёме.
	s.SetDelays(500*time.Millisecond, 0, 1)
	ch1, e1 := make(chan time.Duration, 1), make(chan error, 1)
	go func() { d, err := firstByte(); ch1 <- d; e1 <- err }()
	// Запрос 1 в полёте (спит ~500мс); смена настроек его не касается.
	time.Sleep(60 * time.Millisecond)
	s.SetDelays(0, 0, 1)
	ch2, e2 := make(chan time.Duration, 1), make(chan error, 1)
	go func() { d, err := firstByte(); ch2 <- d; e2 <- err }()

	d1 := <-ch1
	if err := <-e1; err != nil {
		t.Fatalf("запрос 1: %v", err)
	}
	d2 := <-ch2
	if err := <-e2; err != nil {
		t.Fatalf("запрос 2: %v", err)
	}
	if d1 < 300*time.Millisecond {
		t.Fatalf("запрос 1: firstByte=%v, хочу ~500мс (зафиксировано при приёме)", d1)
	}
	if d2 >= 300*time.Millisecond {
		t.Fatalf("запрос 2: firstByte=%v, хочу <300мс (новые настройки 0)", d2)
	}
}

// TestMetricsVLLMFormat — /metrics в формате vLLM: разбор монитором,
// управляемые running/waiting/kv и рост счётчиков на запросах (Э6).
func TestMetricsVLLMFormat(t *testing.T) {
	s := Start("fake-model")
	defer s.Close()

	get := func(t *testing.T) *monitor.VLLMMetrics {
		t.Helper()
		resp, err := http.Get(s.URL + "/metrics")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("/metrics → %d", resp.StatusCode)
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		m, err := monitor.ParseVLLMMetrics(body)
		if err != nil {
			t.Fatalf("ParseVLLMMetrics: %v", err)
		}
		if miss := m.Missing(); len(miss) > 0 {
			t.Fatalf("required отсутствуют: %v", miss)
		}
		return m
	}

	s.SetMetrics(3, 2, 0.5)
	m := get(t)
	if *m.Running != 3 || *m.Waiting != 2 || *m.KV != 0.5 {
		t.Fatalf("running/waiting/kv = %d/%d/%.1f, хочу 3/2/0.5", *m.Running, *m.Waiting, *m.KV)
	}
	if len(m.Models) != 1 || m.Models[0] != "fake-model" {
		t.Fatalf("models = %v, хочу [fake-model]", m.Models)
	}
	gen0 := *m.GenTotal

	resp, err := http.Post(s.URL+"/v1/chat/completions", "application/json",
		strings.NewReader(`{"model":"fake-model","messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	m2 := get(t)
	if *m2.GenTotal <= gen0 {
		t.Fatalf("generation_tokens_total не вырос: %g → %g", gen0, *m2.GenTotal)
	}
}
