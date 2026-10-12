package relay

import (
	"context"
	"testing"
	"time"

	"runpilot/internal/clock"
	"runpilot/internal/config"
	"runpilot/internal/proto"
	"runpilot/internal/textnorm"
)

// Параметры advanceDeliver (виртуальные часы, строгое правило 2).
const (
	advanceStep       = 200 * time.Millisecond
	advanceRealTimeout = 5 * time.Second // страховка от зависания (реальное время).
)

// deliverTest — автомат с виртуальными часами и хуками доставки.
func deliverTest(idle bool) (*Relay, *deliverHooks, *clock.Virtual) {
	cfg := config.Defaults().Relay
	cfg.InputWaitSec = 120
	clk := clock.NewVirtual(time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC))
	r := &Relay{cfg: cfg, clk: clk}
	h := &deliverHooks{idle: idle}
	r.SetDeliverHooks(h.hooks())
	return r, h, clk
}

// deliverHooks — мок-хуки доставки.
type deliverHooks struct {
	idle      bool
	pasted    []string
	enqueued  bool
	steps     []proto.DialogStep
	proofOK   bool
	proofKind string
}

func (h *deliverHooks) hooks() DeliverHooks {
	return DeliverHooks{
		CheckIdle: func() (bool, error) { return h.idle, nil },
		Paste:     func(_ context.Context, text string) error { h.pasted = append(h.pasted, text); return nil },
		Enqueue:   func(_ context.Context) error { h.enqueued = true; return nil },
		Dialog:    func(_ context.Context, steps []proto.DialogStep) error { h.steps = steps; return nil },
		Proof:     func(kind, _ string, _ []string) (bool, error) { h.proofKind = kind; return h.proofOK, nil },
	}
}

// advanceDeliver — Deliver в горутине + advance виртуальных часов в цикле
// (сон через clk.AfterFunc, строгое правило 2). Реальный таймаут — страховка.
func advanceDeliver(t *testing.T, r *Relay, clk *clock.Virtual, kind, text string,
	steps []proto.DialogStep, labels []string) DeliverResult {
	t.Helper()
	done := make(chan DeliverResult, 1)
	go func() {
		res, err := r.Deliver(t.Context(), kind, text, steps, labels)
		if err != nil {
			t.Errorf("Deliver: %v", err)
		}
		done <- res
	}()
	realTimeout := time.After(advanceRealTimeout)
	for {
		select {
		case res := <-done:
			return res
		case <-realTimeout:
			t.Fatal("advanceDeliver: реальный таймаут")
		default:
		}
		clk.Advance(advanceStep)
	}
}

// TestDeliverReport — kind=report: IDLE → вставка → очередь → доказательство.
func TestDeliverReport(t *testing.T) {
	r, h, _ := deliverTest(true)
	h.proofOK = true
	res, err := r.Deliver(t.Context(), DeliverReport, "Сделай шаг 2.", nil, nil)
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if !res.Delivered || res.Proof != "recording" {
		t.Fatalf("res = %+v, хочу Delivered+recording", res)
	}
	if len(h.pasted) != 1 || h.pasted[0] != "Сделай шаг 2." {
		t.Fatalf("pasted = %v, хочу [Сделай шаг 2.]", h.pasted)
	}
	if !h.enqueued {
		t.Fatal("enqueued = false, хочу true")
	}
	if h.proofKind != DeliverReport {
		t.Fatalf("proofKind = %s, хочу report", h.proofKind)
	}
}

// TestDeliverReportMismatch — доказательство не найдено → DELIVERY_MISMATCH.
func TestDeliverReportMismatch(t *testing.T) {
	r, h, _ := deliverTest(true)
	h.proofOK = false
	res, err := r.Deliver(t.Context(), DeliverReport, "текст", nil, nil)
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if res.Hold != HoldDeliveryMismatch {
		t.Fatalf("hold = %s, хочу DELIVERY_MISMATCH", res.Hold)
	}
}

// TestDeliverQuestion — kind=question: dialog_answer → доказательство.
func TestDeliverQuestion(t *testing.T) {
	r, h, _ := deliverTest(true)
	h.proofOK = true
	steps := []proto.DialogStep{{Key: "Down"}, {Key: "Enter"}}
	res, err := r.Deliver(t.Context(), DeliverQuestion, "", steps, []string{"Опция"})
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if !res.Delivered {
		t.Fatalf("res = %+v, хочу Delivered", res)
	}
	if len(h.steps) != 2 {
		t.Fatalf("steps = %d, хочу 2", len(h.steps))
	}
	if h.proofKind != DeliverQuestion {
		t.Fatalf("proofKind = %s, хочу question", h.proofKind)
	}
}

// TestDeliverQuestionMismatch — доказательство не найдено → DIALOG_MISMATCH.
func TestDeliverQuestionMismatch(t *testing.T) {
	r, h, _ := deliverTest(true)
	h.proofOK = false
	res, err := r.Deliver(t.Context(), DeliverQuestion, "", []proto.DialogStep{{Key: "Enter"}}, nil)
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if res.Hold != HoldDialogMismatch {
		t.Fatalf("hold = %s, хочу DIALOG_MISMATCH", res.Hold)
	}
}

// TestCheckIdleOperatorInput — оператор печатает: HOLD(OPERATOR_INPUT) через input_wait_sec.
func TestCheckIdleOperatorInput(t *testing.T) {
	r, _, clk := deliverTest(false) // не IDLE
	res := advanceDeliver(t, r, clk, DeliverReport, "текст", nil, nil)
	if res.Hold != HoldOperatorInput {
		t.Fatalf("hold = %s, хочу OPERATOR_INPUT", res.Hold)
	}
}

// TestCheckIdleBecomesIdle — панель становится IDLE в пределах input_wait_sec.
func TestCheckIdleBecomesIdle(t *testing.T) {
	r, _, clk := deliverTest(false)
	calls := 0
	r.SetDeliverHooks(DeliverHooks{
		CheckIdle: func() (bool, error) { calls++; return calls > 1, nil },
		Paste:     func(_ context.Context, _ string) error { return nil },
		Enqueue:   func(_ context.Context) error { return nil },
		Proof:     func(_ string, _ string, _ []string) (bool, error) { return true, nil },
	})
	res := advanceDeliver(t, r, clk, DeliverReport, "текст", nil, nil)
	if !res.Delivered {
		t.Fatalf("res = %+v, хочу Delivered (панель стала IDLE)", res)
	}
}

// TestNormHashStable — хеш доставки детерминирован (нормализация).
func TestNormHashStable(t *testing.T) {
	if textnorm.NormHash("a  b\n c") != textnorm.NormHash("a b c") {
		t.Fatal("NormHash: нормализация не схлопывает пробелы")
	}
}
