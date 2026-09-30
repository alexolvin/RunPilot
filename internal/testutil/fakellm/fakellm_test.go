package fakellm

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"runpilot/internal/monitor"
)

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
