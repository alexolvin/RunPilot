package gateway

// v2 (S4/S5/S6): классификация отказа по коду HTTP и телу ответа.

import (
	"testing"

	"runpilot/internal/model"
)

// TE-S4/S5/S6 — классификация по шаблону: 5xx → OOM/ENGINE_DEAD,
// 4xx → CONTEXT_LENGTH, иное → None.
func TestClassifyFault(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   model.FaultClass
	}{
		{"OOM cuda", 500, `{"error":"CUDA out of memory. Tried to allocate 2.00 GiB"}`, model.FaultOOM},
		{"OOM torch", 503, `torch.OutOfMemoryError: CUDA out of memory`, model.FaultOOM},
		{"ENGINE_DEAD", 500, `vllm.engine.EngineDeadError: Engine core process died`, model.FaultEngineDead},
		{"ENGINE_DEAD core", 502, `{"detail":"Engine core initialization failed"}`, model.FaultEngineDead},
		{"5xx без шаблона", 500, `{"detail":"internal error"}`, model.FaultNone},
		{"CTX exceeded", 400, `{"error":{"code":"context_length_exceeded","message":"prompt is too long"}`, model.FaultContextLength},
		{"CTX max len", 400, `This model's maximum context length is 32768`, model.FaultContextLength},
		{"4xx без шаблона", 400, `{"detail":"bad request"}`, model.FaultNone},
		{"2xx не отказ", 200, `{"data":"ok"}`, model.FaultNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyFault(tc.status, []byte(tc.body)); got != tc.want {
				t.Fatalf("ClassifyFault(%d, %q) = %q, хочу %q", tc.status, tc.body, got, tc.want)
			}
		})
	}
}
