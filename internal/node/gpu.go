package node

import (
	"context"
	"fmt"
	"time"

	"runpilot/internal/monitor"
	"runpilot/internal/proto"
)

// gpuTimeout — таймаут на одну команду GPU (не порог ТЗ).
const gpuTimeout = gpuTimeoutSec * time.Second

// gpuCommand — фиксированная команда без оболочки (раздел 10 ТЗ):
// узел запускает только её, аргументы не собираются из внешних данных.
func gpuCommand(gpu string) (name string, args []string, ok bool) {
	switch gpu {
	case "nvidia":
		return "nvidia-smi", []string{
			"--query-gpu=index,utilization.gpu,memory.used,memory.total,temperature.gpu,power.draw",
			"--format=csv,noheader,nounits",
		}, true
	case "amd":
		return "rocm-smi", []string{
			"--showuse", "--showmeminfo", "vram", "--showtemp", "--showpower", "--json",
		}, true
	default:
		return "", nil, false
	}
}

// GPUEnabled — телеметрия включена: node.gpu задан и указано имя сервера,
// к строке которого привязываются карты (node.gpu_server).
func (n *Node) GPUEnabled() bool {
	_, _, ok := gpuCommand(n.cfg.Node.GPU)
	return ok && n.cfg.Node.GPUServer != ""
}

// RunGPU — телеметрия GPU (раздел 10 ТЗ): каждые
// monitor.metrics_interval_sec фиксированная команда → KindGPU{server,
// cards}. Ошибка команды/разбора — WARN и пропуск цикла (последнее
// значение у координатора остаётся).
func (n *Node) RunGPU(ctx context.Context, send func(proto.Msg) error) error {
	name, args, ok := gpuCommand(n.cfg.Node.GPU)
	if !ok {
		return fmt.Errorf("node: gpu: неизвестный node.gpu %q", n.cfg.Node.GPU)
	}
	if n.cfg.Node.GPUServer == "" {
		return fmt.Errorf("node: gpu: не задан node.gpu_server")
	}
	interval := time.Duration(n.cfg.Monitor.MetricsIntervalSec) * time.Second
	t := time.NewTicker(interval)
	defer t.Stop()
	run := func() {
		cctx, cancel := context.WithTimeout(ctx, gpuTimeout)
		defer cancel()
		out, err := n.ex.Output(cctx, name, args...)
		if err != nil {
			n.log.Warn("node: gpu: " + err.Error())
			return
		}
		var cards []proto.GPUCard
		if n.cfg.Node.GPU == "nvidia" {
			cards, err = monitor.ParseNvidia(out)
		} else {
			cards, err = monitor.ParseROCm(out)
		}
		if err != nil {
			n.log.Warn("node: gpu: разбор: " + err.Error())
			return
		}
		m := proto.New(proto.KindGPU)
		m.Server = n.cfg.Node.GPUServer
		m.Cards = cards
		if err := send(m); err != nil {
			n.log.Warn("node: gpu: отправка: " + err.Error())
		}
	}
	run() // первый цикл сразу
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("node: gpu: остановлен: %w", ctx.Err())
		case <-t.C:
			run()
		}
	}
}
