// Package monitor — мониторинг серверов (разделы 5/10 ТЗ):
// /health с сериями down/up, метрики vLLM /metrics (expfmt, сумма по
// наборам меток), внешняя нагрузка ext, окно истории для спарклайнов,
// парсеры GPU-телеметрии (nvidia-smi/rocm-smi).
package monitor

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
)

// Required — пять метрик vLLM (раздел 10 ТЗ).
var Required = []string{
	"vllm:num_requests_running",
	"vllm:num_requests_waiting",
	"vllm:kv_cache_usage_perc",
	"vllm:generation_tokens_total",
	"vllm:prompt_tokens_total",
}

// VLLMMetrics — разобранный ответ /metrics. Значения суммируются по
// всем наборам меток (раздел 10 ТЗ); nil — метрика в ответе отсутствует
// (показывается «—», doctor — WARN).
type VLLMMetrics struct {
	Running     *int     // vllm:num_requests_running
	Waiting     *int     // vllm:num_requests_waiting
	KV          *float64 // vllm:kv_cache_usage_perc, %
	GenTotal    *float64 // vllm:generation_tokens_total (счётчик)
	PromptTotal *float64 // vllm:prompt_tokens_total (счётчик)
	Found       []string // найденные имена метрик с префиксом vllm:
	Models      []string // разные model_name в метках vllm:*
}

// has — есть ли метрика (nil = отсутствует).
func (m *VLLMMetrics) has(name string) bool {
	switch name {
	case "vllm:num_requests_running":
		return m.Running != nil
	case "vllm:num_requests_waiting":
		return m.Waiting != nil
	case "vllm:kv_cache_usage_perc":
		return m.KV != nil
	case "vllm:generation_tokens_total":
		return m.GenTotal != nil
	case "vllm:prompt_tokens_total":
		return m.PromptTotal != nil
	}
	return false
}

// Missing — отсутствующие из Required (порядок Required).
func (m *VLLMMetrics) Missing() []string {
	var out []string
	for _, name := range Required {
		if !m.has(name) {
			out = append(out, name)
		}
	}
	return out
}

// sumValue — сумма значений по всем выборкам семейства.
func sumValue(mf *dto.MetricFamily) (float64, int) {
	var sum float64
	n := 0
	for _, m := range mf.GetMetric() {
		var v float64
		switch {
		case m.GetGauge() != nil:
			v = m.GetGauge().GetValue()
		case m.GetCounter() != nil:
			v = m.GetCounter().GetValue()
		case m.GetUntyped() != nil:
			v = m.GetUntyped().GetValue()
		case m.GetHistogram() != nil:
			v = m.GetHistogram().GetSampleSum()
		default:
			continue
		}
		sum += v
		n++
	}
	return sum, n
}

// ParseVLLMMetrics — Prometheus text (expfmt) → VLLMMetrics.
func ParseVLLMMetrics(body []byte) (*VLLMMetrics, error) {
	p := expfmt.NewTextParser(model.LegacyValidation)
	mfs, err := p.TextToMetricFamilies(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("monitor: /metrics: %w", err)
	}
	out := &VLLMMetrics{}
	models := map[string]bool{}
	for name, mf := range mfs {
		if !strings.HasPrefix(name, "vllm:") {
			continue
		}
		out.Found = append(out.Found, name)
		for _, m := range mf.GetMetric() {
			for _, l := range m.GetLabel() {
				if l.GetName() == "model_name" && l.GetValue() != "" {
					models[l.GetValue()] = true
				}
			}
		}
		v, n := sumValue(mf)
		if n == 0 {
			continue
		}
		switch name {
		case "vllm:num_requests_running":
			i := int(v)
			out.Running = &i
		case "vllm:num_requests_waiting":
			i := int(v)
			out.Waiting = &i
		case "vllm:kv_cache_usage_perc":
			f := v
			out.KV = &f
		case "vllm:generation_tokens_total":
			f := v
			out.GenTotal = &f
		case "vllm:prompt_tokens_total":
			f := v
			out.PromptTotal = &f
		}
	}
	sort.Strings(out.Found)
	out.Models = make([]string, 0, len(models))
	for m := range models {
		out.Models = append(out.Models, m)
	}
	sort.Strings(out.Models)
	return out, nil
}
