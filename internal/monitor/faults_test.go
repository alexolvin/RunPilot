package monitor

// v2 (S4/S5): окно отказов OOM/ENGINE_DEAD, карантин по порогу, снятие и
// истечение. Виртуальные часы (mHarn).

import (
	"testing"
	"time"

	"runpilot/internal/model"
)

// lastChange — последняя запись sink (переход состояния).
func lastChange(t *testing.T, h *mHarn) string {
	t.Helper()
	if len(h.sink.changes) == 0 {
		return ""
	}
	return h.sink.changes[len(h.sink.changes)-1]
}

// TE-S4 — серия отказов ≥ faults.quarantine_count за faults.window_sec:
// сервер в карантин (QUARANTINED); истечение карантина возвращает в UP.
func TestFaultQuarantineLifecycle(t *testing.T) {
	h := newMHarn(t, 2)
	// 2 отказа — порог (3) не достигнут.
	h.mon.ReportFault("s", model.FaultOOM)
	h.mon.ReportFault("s", model.FaultOOM)
	if fi := h.mon.FaultInfoOf("s"); fi == nil || fi.Count != 2 {
		t.Fatalf("после 2 отказов: %+v, хочу count=2", fi)
	}
	if got := lastChange(t, h); got == "s=QUARANTINED" {
		t.Fatalf("после 2 отказов уже карантин: %v", h.sink.changes)
	}
	// 3-й отказ — порог: переход в карантин.
	h.mon.ReportFault("s", model.FaultOOM)
	fi := h.mon.FaultInfoOf("s")
	if fi == nil || fi.Count != 3 || fi.QuarantUntil.IsZero() || fi.Class != model.FaultOOM {
		t.Fatalf("после 3 отказов: %+v, хочу count=3, class=OOM, карантин", fi)
	}
	if got := lastChange(t, h); got != "s=QUARANTINED" {
		t.Fatalf("переход: %v, хочу s=QUARANTINED", h.sink.changes)
	}
	// До истечения (faults.quarantine_sec) Tick не возвращает в UP.
	h.advance(time.Second)
	h.mon.Tick()
	if fi := h.mon.FaultInfoOf("s"); fi == nil || fi.QuarantUntil.IsZero() {
		t.Fatalf("карантин истёк раньше времени: %+v", fi)
	}
	// Истечение (300 с) → возврат в состояние здоровья (UP).
	h.advance(300 * time.Second)
	h.mon.Tick()
	if got := lastChange(t, h); got != "s=UP" {
		t.Fatalf("после истечения: %v, хочу s=UP", h.sink.changes)
	}
	if fi := h.mon.FaultInfoOf("s"); fi != nil && !fi.QuarantUntil.IsZero() {
		t.Fatalf("карантин не сброшен после истечения: %+v", fi)
	}
}

// TE-S5 — ENGINE_DEAD: те же правила карантина; снятие оператором
// (Unquarantine) возвращает сервер раньше истечения.
func TestFaultUnquarantine(t *testing.T) {
	h := newMHarn(t, 2)
	for i := 0; i < 3; i++ {
		h.mon.ReportFault("s", model.FaultEngineDead)
	}
	if got := lastChange(t, h); got != "s=QUARANTINED" {
		t.Fatalf("переход: %v, хочу s=QUARANTINED", h.sink.changes)
	}
	// Оператор снимает карантин раньше истечения.
	h.mon.Unquarantine("s")
	if got := lastChange(t, h); got != "s=UP" {
		t.Fatalf("после Unquarantine: %v, хочу s=UP", h.sink.changes)
	}
	if fi := h.mon.FaultInfoOf("s"); fi != nil && !fi.QuarantUntil.IsZero() {
		t.Fatalf("карантин не сброшен после Unquarantine: %+v", fi)
	}
}

// С6 (CONTEXT_LENGTH) не учитывается в карантине: 4xx на запрос.
func TestFaultContextLengthNoQuarantine(t *testing.T) {
	h := newMHarn(t, 2)
	for i := 0; i < 10; i++ {
		h.mon.ReportFault("s", model.FaultContextLength)
	}
	if fi := h.mon.FaultInfoOf("s"); fi != nil && !fi.QuarantUntil.IsZero() {
		t.Fatalf("CONTEXT_LENGTH не должен в карантин: %+v", fi)
	}
	if got := lastChange(t, h); got == "s=QUARANTINED" {
		t.Fatalf("неожиданный карантин: %v", h.sink.changes)
	}
}

// Отказы вне окна (faults.window_sec) не складываются в порог.
func TestFaultWindowSliding(t *testing.T) {
	h := newMHarn(t, 2)
	h.mon.ReportFault("s", model.FaultOOM)
	h.advance(299 * time.Second) // чуть меньше окна (300 с)
	h.mon.ReportFault("s", model.FaultOOM)
	h.advance(299 * time.Second) // первый отказ вышел из окна
	h.mon.ReportFault("s", model.FaultOOM)
	if fi := h.mon.FaultInfoOf("s"); fi == nil || fi.Count >= 3 {
		t.Fatalf("считаются отказы только в окне: %+v", fi)
	}
	if got := lastChange(t, h); got == "s=QUARANTINED" {
		t.Fatalf("не должен быть карантин (окно): %v", h.sink.changes)
	}
}
