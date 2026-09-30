package gateway

// Классификация отказов сервера (раздел 7.1 С4/С5/С6): класс по коду HTTP
// и телу ответа. Чистая функция — без сети и времени.

import (
	"net/http"
	"strings"

	"runpilot/internal/model"
)

// Шаблоны классов отказов. Соответствие — регистронезависимое по подстроке
// в начале тела ответа (vLLM пишет причину в начале JSON-ошибки).
var (
	oomPatterns = []string{
		"cuda out of memory",
		"outofmemoryerror",
		"out of memory",
		"oomkilled",
	}
	engineDeadPatterns = []string{
		"enginedeaderror",
		"engine core",
	}
	contextLengthPatterns = []string{
		"context_length_exceeded",
		"maximum context length",
		"context length exceeded",
		"too many tokens",
		"prompt is too long",
	}
)

// containsFold — хотя бы один шаблон в строке (строка уже в нижнем регистре).
func containsFold(s string, pats []string) bool {
	for _, p := range pats {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}

// ClassifyFault — класс отказа по коду HTTP и телу ответа (раздел 7.1):
// 5xx → OOM / ENGINE_DEAD; 4xx → CONTEXT_LENGTH. Чистая функция (R5:
// без сети и времени).
func ClassifyFault(status int, body []byte) model.FaultClass {
	s := strings.ToLower(string(body))
	switch {
	case status >= http.StatusInternalServerError:
		if containsFold(s, oomPatterns) {
			return model.FaultOOM
		}
		if containsFold(s, engineDeadPatterns) {
			return model.FaultEngineDead
		}
		return model.FaultNone
	case status >= http.StatusBadRequest:
		if containsFold(s, contextLengthPatterns) {
			return model.FaultContextLength
		}
		return model.FaultNone
	}
	return model.FaultNone
}
