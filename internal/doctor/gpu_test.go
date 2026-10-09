package doctor

// W13 этап 5: checkGPU — GPU на узле, но телеметрия не настроена
// (node.gpu=none) или некорректно (нет node.gpu_server, вендор не совпадает)
// → WARN; GPU нет → PASS.

import (
	"testing"

	"runpilot/internal/config"
)

// installFakeGPU — подменяет gpuDetect (вендор, обнаружен ли).
func installFakeGPU(vendor string, ok bool) func() {
	old := gpuDetect
	gpuDetect = func() (string, bool) { return vendor, ok }
	return func() { gpuDetect = old }
}

func gpuCtx(gpu, server string) *Context {
	cfg := &config.Config{}
	cfg.Node.GPU = gpu
	cfg.Node.GPUServer = server
	return &Context{Cfg: cfg, CfgPath: "/tmp/cfg.yaml"}
}

// GPU нет → PASS (проверка не применима).
func TestGPUNoGPU(t *testing.T) {
	defer installFakeGPU("", false)()
	if r := checkGPU(gpuCtx("none", "")); r.Level != PASS {
		t.Fatalf("уровень=%s, хочу PASS; detail=%s", r.Level, r.Detail)
	}
}

// GPU nvidia, но node.gpu=none → WARN.
func TestGPUPresentGpuNone(t *testing.T) {
	defer installFakeGPU("nvidia", true)()
	if r := checkGPU(gpuCtx("none", "")); r.Level != WARN {
		t.Fatalf("уровень=%s, хочу WARN; detail=%s", r.Level, r.Detail)
	}
}

// GPU nvidia, node.gpu=nvidia, но не задан node.gpu_server → WARN.
func TestGPUPresentNoServer(t *testing.T) {
	defer installFakeGPU("nvidia", true)()
	if r := checkGPU(gpuCtx("nvidia", "")); r.Level != WARN {
		t.Fatalf("уровень=%s, хочу WARN; detail=%s", r.Level, r.Detail)
	}
}

// GPU nvidia, но node.gpu=amd (вендор не совпадает) → WARN.
func TestGPUPresentVendorMismatch(t *testing.T) {
	defer installFakeGPU("nvidia", true)()
	if r := checkGPU(gpuCtx("amd", "vzu5")); r.Level != WARN {
		t.Fatalf("уровень=%s, хочу WARN; detail=%s", r.Level, r.Detail)
	}
}

// GPU nvidia, node.gpu=nvidia, node.gpu_server задан → PASS.
func TestGPUPresentConfigured(t *testing.T) {
	defer installFakeGPU("nvidia", true)()
	r := checkGPU(gpuCtx("nvidia", "vzu5"))
	if r.Level != PASS {
		t.Fatalf("уровень=%s, хочу PASS; detail=%s", r.Level, r.Detail)
	}
}

// GPU amd, node.gpu=amd, node.gpu_server задан → PASS.
func TestGPUPresentAMD(t *testing.T) {
	defer installFakeGPU("amd", true)()
	if r := checkGPU(gpuCtx("amd", "vzu5")); r.Level != PASS {
		t.Fatalf("уровень=%s, хочу PASS; detail=%s", r.Level, r.Detail)
	}
}
