package gateway

// v2 (S4/S5): отказ 5xx с шаблоном — шлюз классифицирует и сообщает
// наблюдателю (reportFault). Интеграция через прокси.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"runpilot/internal/config"
	"runpilot/internal/model"
	"runpilot/internal/testutil/fakellm"
)

// recFault — Fake FaultReporter: записывает полученные классы.
type recFault struct {
	mu    sync.Mutex
	calls []model.FaultClass
}

func (r *recFault) ReportFault(name string, fc model.FaultClass) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, fc)
}

func (r *recFault) got() []model.FaultClass {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]model.FaultClass(nil), r.calls...)
}

// upOom — апстрим, который отвечает 500 с OOM-телом на генерацию и 200 на /health.
func upOom(t *testing.T, body string) *httptest.Server {
	t.Helper()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(up.Close)
	return up
}

// TE-S4 — OOM: 500 с шаблоном OOM → reportFault(FaultOOM).
func TestGatewayReportsOOMFault(t *testing.T) {
	up := upOom(t, `{"error":"CUDA out of memory. Tried to allocate 2.00 GiB"}`)
	t.Setenv("RUNPILOT_S4_KEY", "k")
	st, gw, ts := testEnv(t, []config.Server{srvCfg("a", 10, 1, up.URL, "RUNPILOT_S4_KEY")})
	rep := &recFault{}
	gw.SetFaultReporter(rep)

	ok := mkSession(t, st, model.SessionIdle)
	resp, body := postJSON(t, ts.URL+"/s/"+ok+"/v1/chat/completions", chatBody("srv-model", ""))
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("статус: %d тело: %s, хочу 500", resp.StatusCode, body)
	}
	if calls := rep.got(); len(calls) != 1 || calls[0] != model.FaultOOM {
		t.Fatalf("reportFault: %v, хочу [OOM]", calls)
	}
}

// TE-S5 — ENGINE_DEAD: 500 с шаблоном → reportFault(FaultEngineDead).
func TestGatewayReportsEngineDeadFault(t *testing.T) {
	up := upOom(t, `{"detail":"vllm.engine.EngineDeadError: Engine core process died"}`)
	t.Setenv("RUNPILOT_S5_KEY", "k")
	st, gw, ts := testEnv(t, []config.Server{srvCfg("a", 10, 1, up.URL, "RUNPILOT_S5_KEY")})
	rep := &recFault{}
	gw.SetFaultReporter(rep)

	ok := mkSession(t, st, model.SessionIdle)
	resp, _ := postJSON(t, ts.URL+"/s/"+ok+"/v1/chat/completions", chatBody("srv-model", ""))
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("статус: %d, хочу 500", resp.StatusCode)
	}
	if calls := rep.got(); len(calls) != 1 || calls[0] != model.FaultEngineDead {
		t.Fatalf("reportFault: %v, хочу [ENGINE_DEAD]", calls)
	}
}

// 200-ответ отказа не даёт (нет отказов) — контроль ложных срабатываний.
func TestGatewayNoFaultOnOK(t *testing.T) {
	a := fakellm.Start("srv-model")
	t.Cleanup(a.Close)
	t.Setenv("RUNPILOT_SOK_KEY", "k")
	st, gw, ts := testEnv(t, []config.Server{srvCfg("a", 10, 1, a.URL, "RUNPILOT_SOK_KEY")})
	rep := &recFault{}
	gw.SetFaultReporter(rep)
	ok := mkSession(t, st, model.SessionIdle)
	resp, _ := postJSON(t, ts.URL+"/s/"+ok+"/v1/chat/completions", chatBody("srv-model", ""))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("статус: %d, хочу 200", resp.StatusCode)
	}
	if calls := rep.got(); len(calls) != 0 {
		t.Fatalf("неожиданные отказы: %v", calls)
	}
}
