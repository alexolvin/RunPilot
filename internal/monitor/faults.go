package monitor

// Обработка отказов сервера (раздел 7.1 С4/С5): окно отказов OOM/
// ENGINE_DEAD, карантин по порогу, снятие оператором и истечение.

import (
	"time"

	"runpilot/internal/model"
)

// faultWindow — окно учёта отказов (faults.window_sec).
func (m *Monitor) faultWindow() time.Duration {
	return time.Duration(m.cfg.Faults.WindowSec) * time.Second
}

// faultQuarantine — длительность карантина (faults.quarantine_sec).
func (m *Monitor) faultQuarantine() time.Duration {
	return time.Duration(m.cfg.Faults.QuarantineSec) * time.Second
}

// ReportFault — отказ класса OOM/ENGINE_DEAD (S4/S5): учёт в окне отказов;
// ≥ faults.quarantine_count за faults.window_sec → карантин на
// faults.quarantine_sec (SetState + SERVER_FAULT). CONTEXT_LENGTH не в
// карантин (это 4xx на запрос, С6 — отдельно).
func (m *Monitor) ReportFault(name string, fc model.FaultClass) {
	if fc == model.FaultNone || fc == model.FaultContextLength {
		return
	}
	ss, ok := m.srvs[name]
	if !ok {
		return
	}
	now := m.clk.Now()
	window := m.faultWindow()

	ss.mu.Lock()
	var kept []time.Time
	for _, ts := range ss.faults {
		if now.Sub(ts) <= window {
			kept = append(kept, ts)
		}
	}
	kept = append(kept, now)
	ss.faults = kept
	ss.lastFC = fc
	count := len(kept)
	enter := count >= m.cfg.Faults.QuarantineCount && ss.quarantUntil.IsZero()
	if enter {
		ss.quarantUntil = now.Add(m.faultQuarantine())
	}
	ss.mu.Unlock()

	if enter {
		m.sink.SetState(name, model.ServerQuarantined)
		if m.eventSink != nil {
			m.eventSink.RecordServerFault(name, fc, count)
		}
		m.log.Warn("monitor: сервер в карантине",
			"server", name, "class", string(fc), "count", count)
	}
}

// Unquarantine — оператор: снять карантин (S4/S5): сервер возвращается в
// состояние здоровья, окно отказов сбрасывается.
func (m *Monitor) Unquarantine(name string) {
	ss, ok := m.srvs[name]
	if !ok {
		return
	}
	ss.mu.Lock()
	ss.faults = nil
	ss.flaps = nil
	ss.lastFC = model.FaultNone
	ss.quarantUntil = time.Time{}
	health := ss.health.State
	ss.mu.Unlock()
	m.sink.SetState(name, health)
	m.log.Info("monitor: карантин снят", "server", name)
}

// FaultInfo — сводка для карточки сервера (S4/S5): класс, число отказов в
// окне, конец карантина. nil — отказов не было и карантин не действует.
type FaultInfo struct {
	Class        model.FaultClass
	Count        int
	QuarantUntil time.Time
}

// FaultInfoOf — сводка по имени (nil, если отказов не было).
func (m *Monitor) FaultInfoOf(name string) *FaultInfo {
	ss, ok := m.srvs[name]
	if !ok {
		return nil
	}
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if len(ss.faults) == 0 && ss.quarantUntil.IsZero() {
		return nil
	}
	now := m.clk.Now()
	window := m.faultWindow()
	count := 0
	for _, ts := range ss.faults {
		if now.Sub(ts) <= window {
			count++
		}
	}
	return &FaultInfo{Class: ss.lastFC, Count: count, QuarantUntil: ss.quarantUntil}
}

// tickQuarantine — истечение карантина: возврат в состояние здоровья
// (обычно UP). Вызывается из Tick по каждому серверу.
func (m *Monitor) tickQuarantine(name string) {
	ss := m.srvs[name]
	ss.mu.Lock()
	until := ss.quarantUntil
	ss.mu.Unlock()
	if until.IsZero() || m.clk.Now().Before(until) {
		return
	}
	ss.mu.Lock()
	if ss.quarantUntil.IsZero() {
		ss.mu.Unlock()
		return
	}
	ss.quarantUntil = time.Time{}
	ss.faults = nil
	ss.flaps = nil
	health := ss.health.State
	ss.mu.Unlock()
	m.sink.SetState(name, health)
	m.log.Info("monitor: карантин истёк", "server", name)
}
