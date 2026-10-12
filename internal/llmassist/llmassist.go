package llmassist

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// errInvalid — невалидный ответ LLM (JSON/схема) после повтора.
var errInvalid = errors.New("llmassist: invalid response")

// Assistant — помощник LLM связи (ТЗ Relay §13).
type Assistant struct {
	caller LLMCaller
	cfg    Config
}

// New — сборка помощника.
func New(caller LLMCaller, cfg Config) *Assistant {
	return &Assistant{caller: caller, cfg: cfg}
}

// Report — T-REPORT: индекс начала отчёта в пронумерованных сообщениях хода.
func (a *Assistant) Report(ctx context.Context, messages []string) (Result, error) {
	msgs := keepTail(messages, a.cfg.MaxInputKB*kbBytes)
	prompt := strings.ReplaceAll(reportPrompt, "{messages}", numberItems(msgs))
	var resp struct {
		Start *int `json:"start"`
	}
	raw, err := a.callValidated(ctx, prompt, func(s string) error {
		if err := json.Unmarshal([]byte(s), &resp); err != nil {
			return err
		}
		if resp.Start != nil && (*resp.Start < 0 || *resp.Start >= len(msgs)) {
			return errInvalid
		}
		return nil
	})
	if err != nil {
		return resultErr(err)
	}
	_ = raw
	return Result{Start: resp.Start, LLMUsed: true}, nil
}

// Answer — T-ANSWER: блок кода для агента в ответе оркестратора.
func (a *Assistant) Answer(ctx context.Context, answer string) (Result, error) {
	prompt := strings.ReplaceAll(answerPrompt, "{answer}", answer)
	var resp AnswerResp
	_, err := a.callValidated(ctx, prompt, func(s string) error {
		var v AnswerResp
		if err := json.Unmarshal([]byte(s), &v); err != nil {
			return err
		}
		resp = v
		if v.Block == nil && v.Quote == "" {
			return nil // null — допустимо (блока нет)
		}
		if v.Quote != "" && !validQuote(v.Quote, answer, a.cfg.MinQuoteChars) {
			return errInvalid
		}
		return nil
	})
	if err != nil {
		return resultErr(err)
	}
	if resp.Block == nil && resp.Quote == "" {
		return Result{LLMUsed: true}, nil // null — блока нет
	}
	return Result{Answer: &resp, LLMUsed: true}, nil
}

// Dialog — T-DIALOG: ответ на каждый вопрос диалога.
func (a *Assistant) Dialog(ctx context.Context, questions, answer string) (Result, error) {
	prompt := strings.ReplaceAll(dialogPrompt, "{questions}", questions)
	prompt = strings.ReplaceAll(prompt, "{answer}", answer)
	var resp []DialogAnswer
	_, err := a.callValidated(ctx, prompt, func(s string) error {
		var v []DialogAnswer
		if err := json.Unmarshal([]byte(s), &v); err != nil {
			return err
		}
		resp = v
		for _, d := range v {
			if d.Quote != "" && !validQuote(d.Quote, answer, a.cfg.MinQuoteChars) {
				return errInvalid
			}
		}
		return nil
	})
	if err != nil {
		return resultErr(err)
	}
	return Result{Dialog: resp, LLMUsed: true}, nil
}

// callValidated — вызов LLM + валидация; невалидный → один повтор → errInvalid.
func (a *Assistant) callValidated(ctx context.Context, prompt string, validate func(string) error) (string, error) {
	raw, err := a.caller.Call(ctx, prompt)
	if err != nil {
		return "", err
	}
	if err := validate(raw); err != nil {
		raw, err = a.caller.Call(ctx, prompt) // один повтор (§13.2)
		if err != nil {
			return "", err
		}
		if err := validate(raw); err != nil {
			return "", errInvalid
		}
	}
	return raw, nil
}

// validQuote — quote: точная подстрока ответа, длина ≥ min_quote_chars.
func validQuote(quote, answer string, minChars int) bool {
	if len([]rune(quote)) < minChars {
		return false
	}
	return strings.Contains(answer, quote)
}

// resultErr — errInvalid → HOLD(LLM_INVALID); иное — ошибка.
func resultErr(err error) (Result, error) {
	if errors.Is(err, errInvalid) {
		return Result{Hold: HoldLLMInvalid, LLMUsed: true}, nil
	}
	return Result{}, err
}

// keepTail — последние целые элементы, укладывающиеся в maxBytes (обрезка с конца).
func keepTail(items []string, maxBytes int) []string {
	if maxBytes <= 0 {
		return items
	}
	total := 0
	start := len(items)
	for i := len(items) - 1; i >= 0; i-- {
		total += len(items[i])
		if total > maxBytes {
			break
		}
		start = i
	}
	return items[start:]
}

// numberItems — пронумеровать элементы (индексы с 0).
func numberItems(items []string) string {
	var b strings.Builder
	for i, it := range items {
		fmt.Fprintf(&b, "%d: %s\n", i, it)
	}
	return b.String()
}
