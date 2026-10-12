package relay

import (
	"context"
	"time"

	"runpilot/internal/proto"
)

// hold-коды доставки (ТЗ Relay §12).
const (
	HoldOperatorInput    = "OPERATOR_INPUT"
	HoldDeliveryMismatch = "DELIVERY_MISMATCH"
	HoldDialogMismatch   = "DIALOG_MISMATCH"
)

// Виды доставки (ТЗ Relay §12).
const (
	DeliverReport   = "report"
	DeliverQuestion = "question"
)

// DeliverResult — результат доставки (раздел 12).
type DeliverResult struct {
	Delivered bool
	Hold      string
	Proof     string // "recording" | "terminal"
}

// DeliverHooks — хуки доставки, заполняются координатором (узел + agentlog +
// планировщик). Все проверки — только через хуки, чтобы автомат тестировался
// на виртуальных часах и мокках (строгое правило 2).
type DeliverHooks struct {
	// CheckIdle — сессия+панель в IDLE, ввод агента пуст, хеш стабилен.
	CheckIdle func() (idle bool, err error)
	// Paste — вставка текста (bracketed paste) в композер (12.1.2).
	Paste func(ctx context.Context, text string) error
	// Enqueue — постановка в очередь с умолчаниями сессии (12.1.3).
	Enqueue func(ctx context.Context) error
	// Dialog — dialog_answer: шаги диалога вопроса (12.2.3).
	Dialog func(ctx context.Context, steps []proto.DialogStep) error
	// Proof — доказательство в записи (12.1.4/12.2.4): kind, хеш, подписи.
	Proof func(kind, hash string, labels []string) (bool, error)
}

// SetDeliverHooks — установка хуков доставки (координатор).
func (r *Relay) SetDeliverHooks(h DeliverHooks) {
	r.deliver = h
}

// Deliver — доставка ответа оркестратора агенту (ТЗ Relay §12).
// kind=report: вставка + очередь + доказательство (12.1).
// kind=question: dialog_answer + доказательство (12.2).
func (r *Relay) Deliver(ctx context.Context, kind, answer string,
	steps []proto.DialogStep, optionLabels []string) (DeliverResult, error) {

	hash := normHash(answer)
	switch kind {
	case DeliverReport:
		return r.deliverReport(ctx, answer, hash)
	case DeliverQuestion:
		return r.deliverQuestion(ctx, steps, hash, optionLabels)
	default:
		return DeliverResult{Hold: HoldDeliveryMismatch}, nil
	}
}

// deliverReport — kind=report (12.1): IDLE → вставка → очередь → доказательство.
func (r *Relay) deliverReport(ctx context.Context, text, hash string) (DeliverResult, error) {
	// 12.1.1: IDLE + пустой ввод + стабильный хеш, иначе ждать input_wait_sec.
	if hold, err := r.checkIdle(ctx); hold != "" || err != nil {
		if hold != "" {
			return DeliverResult{Hold: hold}, nil
		}
		return DeliverResult{}, err
	}
	// 12.1.2: вставка (bracketed paste).
	if err := r.deliver.Paste(ctx, text); err != nil {
		return DeliverResult{}, err
	}
	// 12.1.3: постановка в очередь.
	if err := r.deliver.Enqueue(ctx); err != nil {
		return DeliverResult{}, err
	}
	// 12.1.4: доказательство — хеш следующего пользовательского сообщения.
	found, err := r.deliver.Proof(DeliverReport, hash, nil)
	if err != nil {
		return DeliverResult{}, err
	}
	if !found {
		return DeliverResult{Hold: HoldDeliveryMismatch}, nil
	}
	return DeliverResult{Delivered: true, Proof: "recording"}, nil
}

// deliverQuestion — kind=question (12.2): dialog_answer → доказательство.
func (r *Relay) deliverQuestion(ctx context.Context, steps []proto.DialogStep,
	hash string, labels []string) (DeliverResult, error) {

	// 12.2.3: узел выполняет dialog_answer по шагам.
	if err := r.deliver.Dialog(ctx, steps); err != nil {
		return DeliverResult{}, err
	}
	// 12.2.4: доказательство — результат ask_user_question содержит ответ.
	found, err := r.deliver.Proof(DeliverQuestion, hash, labels)
	if err != nil {
		return DeliverResult{}, err
	}
	if !found {
		return DeliverResult{Hold: HoldDialogMismatch}, nil
	}
	return DeliverResult{Delivered: true, Proof: "recording"}, nil
}

// checkIdle — 12.1.1: IDLE + пустой ввод + стабильный хеш. Иначе ждать до
// input_wait_sec (виртуальные часы), затем HOLD(OPERATOR_INPUT).
func (r *Relay) checkIdle(ctx context.Context) (string, error) {
	idle, err := r.deliver.CheckIdle()
	if err != nil {
		return "", err
	}
	if idle {
		return "", nil
	}
	deadline := r.clk.Now().Add(time.Duration(r.cfg.InputWaitSec) * time.Second)
	for {
		if r.clk.Now().After(deadline) {
			return HoldOperatorInput, nil
		}
		if err := r.sleepClock(ctx, idlePollInterval); err != nil {
			return "", err
		}
		idle, err = r.deliver.CheckIdle()
		if err != nil {
			return "", err
		}
		if idle {
			return "", nil
		}
	}
}

// sleepClock — сон на d через часы (виртуальные в тестах, строгое правило 2).
func (r *Relay) sleepClock(ctx context.Context, d time.Duration) error {
	ch := make(chan struct{})
	r.clk.AfterFunc(d, func() { close(ch) })
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-ch:
		return nil
	}
}
