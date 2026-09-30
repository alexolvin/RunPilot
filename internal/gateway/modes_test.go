package gateway

// Режимы координатора (v2 разделы 6.2/6.4): 503 по режиму и отмена
// идущих потоков (CONTROL 3).

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"runpilot/internal/config"
	"runpilot/internal/model"
	"runpilot/internal/testutil/fakellm"
)

// CONTROL 3 (6.2): EMERGENCY — любой генерирующий запрос 503 runpilot_emergency
// + Retry-After.
func TestEmergency503(t *testing.T) {
	t.Setenv("RUNPILOT_TEST_KEY", "k")
	a := fakellm.Start("srv-model")
	defer a.Close()
	st, _, ts := testEnv(t, []config.Server{srvCfg("a", 10, 1, a.URL, "RUNPILOT_TEST_KEY")})
	s := mkSession(t, st, model.SessionIdle)

	if err := st.SetMode("EMERGENCY"); err != nil {
		t.Fatal(err)
	}
	resp := postOpen(t, ts.URL+"/s/"+s+"/v1/chat/completions", chatBody("runpilot", `"stream":true`))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("EMERGENCY: статус=%d, хочу 503", resp.StatusCode)
	}
	if ra := resp.Header.Get("Retry-After"); ra != "7" {
		t.Fatalf("EMERGENCY: Retry-After=%q, хочу 7", ra)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "runpilot_emergency") {
		t.Fatalf("EMERGENCY: тело %q без runpilot_emergency", body)
	}
}

// CONTROL 4 (6.4): SAFE_MODE — новая неявная аренда (без активной) 503
// runpilot_safe_mode.
func TestSafeMode503(t *testing.T) {
	t.Setenv("RUNPILOT_TEST_KEY", "k")
	a := fakellm.Start("srv-model")
	defer a.Close()
	st, _, ts := testEnv(t, []config.Server{srvCfg("a", 10, 1, a.URL, "RUNPILOT_TEST_KEY")})
	s := mkSession(t, st, model.SessionIdle)

	if err := st.SetMode("SAFE_MODE"); err != nil {
		t.Fatal(err)
	}
	resp := postOpen(t, ts.URL+"/s/"+s+"/v1/chat/completions", chatBody("runpilot", `"stream":true`))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("SAFE_MODE: статус=%d, хочу 503", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "runpilot_safe_mode") {
		t.Fatalf("SAFE_MODE: тело %q без runpilot_safe_mode", body)
	}
}

// CONTROL 3 (6.2): аварийная остановка отменяет идущие потоки —
// CancelAll обрывает SSE раньше, чем апстрим достримит все чанки.
func TestEmergencyCancelAll(t *testing.T) {
	t.Setenv("RUNPILOT_TEST_KEY", "k")
	a := fakellm.Start("srv-model")
	defer a.Close()
	// 100 чанков × 30 мс ≈ 3 с: без отмены достримится целиком.
	a.SetDelays(50*time.Millisecond, 30*time.Millisecond, 100)
	st, gw, ts := testEnv(t, []config.Server{srvCfg("a", 10, 1, a.URL, "RUNPILOT_TEST_KEY")})
	s := mkSession(t, st, model.SessionIdle)

	resp, err := http.Post(ts.URL+"/s/"+s+"/v1/chat/completions", "application/json",
		strings.NewReader(chatBody("runpilot", `"stream":true`)))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("старт потока: %d, хочу 200", resp.StatusCode)
	}
	// Поток идёт — аварийная остановка отменяет его.
	time.Sleep(100 * time.Millisecond)
	gw.CancelAll()

	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	n := 0
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "data: {") {
			n++
		}
	}
	if n >= 90 {
		t.Fatalf("после CancelAll прочитано %d чанков (хочу << 100): поток не отменён", n)
	}
	t.Logf("после CancelAll прочитано %d из 100 чанков", n)
}
