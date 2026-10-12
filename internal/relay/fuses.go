package relay

import (
	"runpilot/internal/model"
)

// hold-коды предохранителей (ТЗ Relay §14).
const (
	HoldFuseCycles = "FUSE_CYCLES"
)

// Fused — сработал ли предохранитель циклов (auto_cycles ≥ max_auto_cycles).
func (r *Relay) Fused(autoCycles int) bool {
	return r.cfg.MaxAutoCycles > 0 && autoCycles >= r.cfg.MaxAutoCycles
}

// NextState — следующее состояние автомата после цикла (ТЗ Relay §5).
// ok — цикл завершил доставку; stop — стоп-слово; hold — код предохранителя.
func NextState(ok, stop bool, hold string) model.RelayState {
	if stop {
		return model.RelayDone
	}
	if hold != "" {
		return model.RelayHold
	}
	if ok {
		return model.RelayArmed
	}
	return model.RelayHold
}
