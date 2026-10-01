package texts

import (
	"testing"

	"runpilot/internal/model"
)

// allIneligible — ВСЕ коды ineligible_reason: приложение А базового ТЗ
// (pause…held_ok) + раздел 3.3 v2 (emergency…key_missing). CONTROL W4:
// каждый код обязан иметь why.text (подпись), а не сырой код.
var allIneligible = []string{
	model.ReasonPause, model.ReasonNotBefore, model.ReasonAfterWait,
	model.ReasonStaleSnapshot, model.ReasonNotIdle, model.ReasonOperatorTyping,
	model.ReasonWaitUI, model.ReasonPinMismatch, model.ReasonPinDown,
	model.ReasonPreferWait, model.ReasonAccept, model.ReasonCooldown,
	model.ReasonExternal, model.ReasonNoUpServer, model.ReasonNodeDrain,
	model.ReasonHeldOK,
	// раздел 3.3 v2.
	model.ReasonEmergency, model.ReasonSafeMode, model.ReasonServerDisabled,
	model.ReasonServerQuarantine, model.ReasonModelProblem, model.ReasonKeyMissing,
}

// allHold — ВСЕ коды hold_reason: базовые (EMPTY_INPUT…PIN_UNAVAILABLE) +
// раздел 3.3 v2 (AGENT_EXITED, EMERGENCY).
var allHold = []model.HoldReason{
	model.HoldEmptyInput, model.HoldStartNotConfirmed, model.HoldRequeueLimit,
	model.HoldPaneUnknown, model.HoldNodeLost, model.HoldNodeDrain,
	model.HoldOperator, model.HoldUpstream4XX, model.HoldDependencyFailed,
	model.HoldPinUnavailable,
	// раздел 3.3 v2.
	model.HoldAgentExited, model.HoldEmergency,
}

// TestWhyTextAllCodes — why.text для каждого кода ineligible_reason
// (приложение А базового ТЗ и раздел 3.3) и hold_reason: подпись
// непустая и не совпадает с кодом (то есть реально отображается текст).
func TestWhyTextAllCodes(t *testing.T) {
	for _, code := range allIneligible {
		got := IneligibleLabel(code)
		if got == "" {
			t.Errorf("ineligible %q: why.text пуст", code)
			continue
		}
		if got == code {
			t.Errorf("ineligible %q: why.text = коду (нет подписи)", code)
		}
	}
	for _, hr := range allHold {
		got := HoldLabel(hr)
		if got == "" {
			t.Errorf("hold %q: why.text пуст", hr)
			continue
		}
		if got == string(hr) {
			t.Errorf("hold %q: why.text = коду (нет подписи)", hr)
		}
	}
}

// TestMetaWhyMapsComplete — meta (GET /api/v1/meta) несёт подписи для
// ВСЕХ кодов: ineligible и hold_reasons (клиент рисует why.text из meta,
// не из кода).
func TestMetaWhyMapsComplete(t *testing.T) {
	d := Meta("test", "NORMAL")
	for _, code := range allIneligible {
		if _, ok := d.Ineligible[code]; !ok {
			t.Errorf("meta.ineligible нет кода %q", code)
		}
	}
	for _, hr := range allHold {
		if _, ok := d.HoldReasons[string(hr)]; !ok {
			t.Errorf("meta.hold_reasons нет кода %q", hr)
		}
	}
}

// TestServerStateLabels — подписи отображаемых состояний сервера (3.2).
func TestServerStateLabels(t *testing.T) {
	d := Meta("test", "NORMAL")
	for _, code := range []string{"UP", "DRAINING", "PAUSED", "QUARANTINED", "DOWN",
		"MODEL_PROBLEM", "KEY_MISSING", "DISABLED", "REMOVING"} {
		found := false
		for _, e := range d.Servers {
			if e.Code == code && e.Label != "" {
				found = true
			}
		}
		if !found {
			t.Errorf("meta.servers нет подписи состояния %q", code)
		}
	}
}
