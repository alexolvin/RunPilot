package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"runpilot/internal/config"
	"runpilot/internal/model"
	"runpilot/internal/testutil/fakellm"
)

// TestMetricsEndpoint — /metrics отдаёт Prometheus text format с тремя метриками.
func TestMetricsEndpoint(t *testing.T) {
	m := NewMetrics()
	m.RecordRequest("srv-a", "200")
	m.RecordRequest("srv-a", "503")
	m.RecordRequest("", "0")
	m.RecordHold(29.3)
	m.RecordHold(1.5)
	m.RecordOverhead(0.05)

	ts := httptest.NewServer(m.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	text := string(body)

	for _, want := range []string{
		"aqm_gateway_requests_total{server=\"srv-a\",code=\"200\"} 1",
		"aqm_gateway_requests_total{server=\"srv-a\",code=\"503\"} 1",
		"aqm_gateway_requests_total{server=\"\",code=\"0\"} 1",
		"aqm_gateway_hold_seconds_count 2",
		"aqm_gateway_overhead_seconds_count 1",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("нет %q в /metrics:\n%s", want, text)
		}
	}
}

// TestHeldToResumeRequestLine — heldToResume создаёт request-строку
// с error=HOLD_TIMEOUT и held_ms.
func TestHeldToResumeRequestLine(t *testing.T) {
	a := fakellm.Start("srv-model")
	defer a.Close()
	t.Setenv("RUNPILOT_TEST_KEY", "k")
	st, gw, _ := testEnv(t, []config.Server{srvCfg("a", 10, 1, a.URL, "RUNPILOT_TEST_KEY")})
	s := mkSession(t, st, model.SessionIdle)

	gw.heldToResume(s, 3600000)

	reqFound := false
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		reqs, _ := st.RequestList(s, 10)
		for _, r := range reqs {
			if r.Error == "HOLD_TIMEOUT" {
				reqFound = true
				if r.HeldMS != 3600000 {
					t.Fatalf("held_ms: %d, хочу 3600000", r.HeldMS)
				}
				break
			}
		}
		if reqFound {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !reqFound {
		t.Fatal("нет request.error = HOLD_TIMEOUT после heldToResume")
	}
}
