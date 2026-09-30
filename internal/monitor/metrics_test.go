package monitor

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// fixturePath — testdata/fixtures/… от корня репозитория.
func fixturePath(t *testing.T, parts ...string) string {
	t.Helper()
	return filepath.Join("..", "..", "testdata", "fixtures", filepath.Join(parts...))
}

// TestParseVLLMMetricsReal — записанный /metrics реального vLLM
// (2026-09-25, 127.0.0.1:8004): 100% строк разобрано, все 5 required
// метрик, один model_name; Found совпадает с набором vllm:-семейств
// в файле (независимая сверка по сырому тексту).
func TestParseVLLMMetricsReal(t *testing.T) {
	body, err := os.ReadFile(fixturePath(t, "metrics", "vllm_real.txt"))
	if err != nil {
		t.Fatal(err)
	}
	m, err := ParseVLLMMetrics(body)
	if err != nil {
		t.Fatalf("реальная фикстура не разобрана: %v", err)
	}
	if miss := m.Missing(); len(miss) > 0 {
		t.Errorf("Missing = %v, хочу пусто", miss)
	}
	// Значения фикстуры (engine=0, qwen3.8-27b).
	if *m.Running != 0 || *m.Waiting != 0 {
		t.Errorf("running/waiting = %d/%d, хочу 0/0", *m.Running, *m.Waiting)
	}
	if *m.KV != 0 {
		t.Errorf("kv = %v, хочу 0", *m.KV)
	}
	if *m.GenTotal != 1018197 {
		t.Errorf("gen = %v, хочу 1018197", *m.GenTotal)
	}
	if *m.PromptTotal != 167468330 {
		t.Errorf("prompt = %v, хочу 167468330", *m.PromptTotal)
	}
	if len(m.Models) != 1 || m.Models[0] != "qwen3.8-27b" {
		t.Errorf("models = %v, хочу [qwen3.8-27b]", m.Models)
	}
	want := familyNames(t, body)
	if len(m.Found) != len(want) {
		t.Fatalf("Found = %d имён, в файле %d vllm:-семейств", len(m.Found), len(want))
	}
	for i := range want {
		if m.Found[i] != want[i] {
			t.Errorf("Found[%d] = %q, хочу %q", i, m.Found[i], want[i])
		}
	}
}

// familyNames — множество vllm:-семейств из сырого текста (проверка
// независимо от expfmt): семейство — имя из «# TYPE name …»; строки
// выборки (_bucket/_sum/_count) относятся к ближайшему TYPE.
func familyNames(t *testing.T, body []byte) []string {
	t.Helper()
	seen := map[string]bool{}
	current := ""
	for _, line := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(line, "# TYPE ") {
			f := strings.Fields(line)
			if len(f) >= 3 {
				current = f[2]
			}
			continue
		}
		if !strings.HasPrefix(line, "vllm:") {
			continue
		}
		name := current
		if !strings.HasPrefix(name, "vllm:") {
			name = line
			if i := strings.IndexByte(name, '{'); i >= 0 {
				name = name[:i]
			}
			if i := strings.IndexByte(name, ' '); i >= 0 {
				name = name[:i]
			}
		}
		seen[name] = true
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// TestParseVLLMSumLabels — значения суммируются по всем наборам меток
// (раздел 10 ТЗ).
func TestParseVLLMSumLabels(t *testing.T) {
	body := []byte(`
# HELP vllm:num_requests_running Running requests
# TYPE vllm:num_requests_running gauge
vllm:num_requests_running{engine="0",model_name="m"} 1.0
vllm:num_requests_running{engine="1",model_name="m"} 2.0
vllm:num_requests_waiting{engine="0",model_name="m"} 0.0
vllm:kv_cache_usage_perc{engine="0",model_name="m"} 0.5
vllm:kv_cache_usage_perc{engine="1",model_name="m"} 0.25
vllm:generation_tokens_total{engine="0",model_name="m"} 10.0
vllm:generation_tokens_total{engine="1",model_name="m"} 5.0
vllm:prompt_tokens_total{engine="0",model_name="m"} 100.0
`)
	m, err := ParseVLLMMetrics(body)
	if err != nil {
		t.Fatal(err)
	}
	if *m.Running != 3 {
		t.Errorf("running = %d, хочу 3 (1+2 по engine)", *m.Running)
	}
	if *m.Waiting != 0 {
		t.Errorf("waiting = %d, хочу 0", *m.Waiting)
	}
	if *m.KV != 0.75 {
		t.Errorf("kv = %v, хочу 0.75", *m.KV)
	}
	if *m.GenTotal != 15 {
		t.Errorf("gen = %v, хочу 15", *m.GenTotal)
	}
	if *m.PromptTotal != 100 {
		t.Errorf("prompt = %v, хочу 100", *m.PromptTotal)
	}
}

// TestParseVLLMWaitingNotByReason — num_requests_waiting не путается с
// num_requests_waiting_by_reason.
func TestParseVLLMWaitingNotByReason(t *testing.T) {
	body := []byte("vllm:num_requests_waiting_by_reason{engine=\"0\",model_name=\"m\",reason=\"capacity\"} 7.0\n")
	m, err := ParseVLLMMetrics(body)
	if err != nil {
		t.Fatal(err)
	}
	if m.Waiting != nil {
		t.Fatalf("waiting = %d, хочу absence", *m.Waiting)
	}
	if m.Running != nil {
		t.Fatalf("running = %d, хочу absence", *m.Running)
	}
}

// TestParseVLLMMissing — отсутствующие required-метрики → Missing;
// чужие (не vllm:) семейства в Found не попадают.
func TestParseVLLMMissing(t *testing.T) {
	body := []byte("vllm:num_requests_running{engine=\"0\",model_name=\"m\"} 1.0\ngo_gc_duration_seconds_sum 0.5\n")
	m, err := ParseVLLMMetrics(body)
	if err != nil {
		t.Fatal(err)
	}
	miss := m.Missing()
	if len(miss) != 4 {
		t.Fatalf("Missing = %v, хочу 4 имени", miss)
	}
	if len(m.Found) != 1 || m.Found[0] != "vllm:num_requests_running" {
		t.Errorf("Found = %v", m.Found)
	}
}

// TestParseVLLMEmpty — пустой ответ: без ошибки, все метрики отсутствуют.
func TestParseVLLMEmpty(t *testing.T) {
	m, err := ParseVLLMMetrics([]byte(""))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Missing()) != len(Required) {
		t.Errorf("Missing = %v, хочу все %d", m.Missing(), len(Required))
	}
}

// TestParseVLLMRejectsGarbage — не-метрики → ошибка.
func TestParseVLLMRejectsGarbage(t *testing.T) {
	if _, err := ParseVLLMMetrics([]byte("not a metric\n{{{")); err == nil {
		t.Fatal("хочу ошибку на мусоре")
	}
}
