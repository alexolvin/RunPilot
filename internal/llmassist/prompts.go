// Package llmassist — помощник LLM связи (ТЗ Relay §13). Вызов — через шлюз
// под JOB-арендой relay-assist, temperature 0; строгий JSON, невалидный →
// один повтор → HOLD(LLM_INVALID). Промпты — embed, вход обрезается с конца.
package llmassist

import (
	_ "embed"
)

//go:embed prompts/report.md
var reportPrompt string

//go:embed prompts/answer.md
var answerPrompt string

//go:embed prompts/dialog.md
var dialogPrompt string
