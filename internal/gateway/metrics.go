package gateway

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
)

// Metrics — метрики шлюза (раздел 14 ТЗ):
//   - aqm_gateway_requests_total{server,code} — counter
//   - aqm_gateway_hold_seconds — histogram
//   - aqm_gateway_overhead_seconds — histogram
type Metrics struct {
	mu       sync.Mutex
	requests map[string]int64 // "server|code" → count
	hold     []float64        // hold durations in seconds
	overhead []float64        // overhead durations in seconds
}

func NewMetrics() *Metrics {
	return &Metrics{
		requests: map[string]int64{},
	}
}

// RecordRequest — aqm_gateway_requests_total{server,code}.
func (m *Metrics) RecordRequest(server, code string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests[server+"|"+code]++
}

// RecordHold — aqm_gateway_hold_seconds.
func (m *Metrics) RecordHold(seconds float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hold = append(m.hold, seconds)
}

// RecordOverhead — aqm_gateway_overhead_seconds.
func (m *Metrics) RecordOverhead(seconds float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.overhead = append(m.overhead, seconds)
}

// Handler — HTTP-обработчик /metrics (Prometheus text format).
func (m *Metrics) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		m.mu.Lock()
		defer m.mu.Unlock()

		fmt.Fprintln(w, "# TYPE aqm_gateway_requests_total counter")
		keys := make([]string, 0, len(m.requests))
		for k := range m.requests {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			parts := strings.SplitN(k, "|", metricLabelParts)
			if len(parts) == metricLabelParts {
				fmt.Fprintf(w, "aqm_gateway_requests_total{server=%q,code=%q} %d\n", parts[0], parts[1], m.requests[k])
			}
		}

		m.writeHistogram(w, "aqm_gateway_hold_seconds", m.hold)
		m.writeHistogram(w, "aqm_gateway_overhead_seconds", m.overhead)
	})
}

var metricBuckets = []float64{bucket001, bucket005, bucket01, bucket05, bucket1, bucket5, bucket10, bucket30, bucket60, bucket300, bucket600, bucket3600}

func (m *Metrics) writeHistogram(w http.ResponseWriter, name string, values []float64) {
	fmt.Fprintf(w, "# TYPE %s histogram\n", name)
	if len(values) == 0 {
		return
	}
	counts := make([]int, len(metricBuckets))
	sum := 0.0
	for _, v := range values {
		sum += v
		for i, b := range metricBuckets {
			if v <= b {
				counts[i]++
			}
		}
	}
	cumulative := 0
	for i, b := range metricBuckets {
		cumulative += counts[i]
		fmt.Fprintf(w, "%s_bucket{le=%q} %d\n", name, fmt.Sprintf("%.2f", b), cumulative)
	}
	fmt.Fprintf(w, "%s_bucket{le=\"+Inf\"} %d\n", name, len(values))
	fmt.Fprintf(w, "%s_sum %f\n", name, sum)
	fmt.Fprintf(w, "%s_count %d\n", name, len(values))
}
