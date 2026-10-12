package llmassist

import "context"

// Виды задач помощника (ТЗ Relay §13).
const (
	TaskReport = "T-REPORT"
	TaskAnswer = "T-ANSWER"
	TaskDialog = "T-DIALOG"
)

// HoldLLMInvalid — невалидный ответ LLM после повтора (ТЗ Relay §13.2).
const HoldLLMInvalid = "LLM_INVALID"

// LLMCaller — вызов LLM (в проде — шлюз под JOB-арендой relay-assist;
// в тестах — мок). temperature 0 задаёт вызывающий.
type LLMCaller interface {
	Call(ctx context.Context, prompt string) (string, error)
}

// Config — параметры помощника (только из конфигурации, строгое правило 1).
type Config struct {
	MaxTokens     int
	TimeoutSec    int
	MaxInputKB    int
	MinQuoteChars int
}

// AnswerResp — ответ T-ANSWER (строгий JSON, раздел 13.4).
type AnswerResp struct {
	Block *int   `json:"block"`
	Quote string `json:"quote"`
}

// DialogAnswer — ответ T-DIALOG по одному вопросу.
type DialogAnswer struct {
	Option  *int   `json:"option"`
	Options []int  `json:"options"`
	Quote   string `json:"quote"`
}

// Result — результат задачи: значение или Hold. LLMUsed — был ли вызов LLM.
type Result struct {
	Start     *int           // T-REPORT
	Answer    *AnswerResp    // T-ANSWER
	Dialog    []DialogAnswer // T-DIALOG
	Hold      string
	LLMUsed   bool
}
