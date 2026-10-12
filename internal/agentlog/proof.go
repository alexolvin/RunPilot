package agentlog

import (
	"encoding/json"
	"os"
	"strings"

	"runpilot/internal/textnorm"
)

// Виды доставки (ТЗ Relay §12).
const (
	KindReport   = "report"
	KindQuestion = "question"
)

// DeliveryProof — доказательство доставки в записи Qwen Code (ТЗ Relay §12).
// kind=report: последнее настоящее пользовательское сообщение имеет хеш hash.
// kind=question: результат ask_user_question содержит текст ответа (по хешу)
// или подписи выбранных вариантов. Возвращает (найдено, ошибка).
func DeliveryProof(path, kind, hash string, optionLabels []string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	var records []record
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var r record
		if json.Unmarshal([]byte(line), &r) != nil {
			continue
		}
		records = append(records, r)
	}
	switch kind {
	case KindReport:
		return lastUserMessageHash(records) == hash, nil
	case KindQuestion:
		return questionAnswerProof(records, hash, optionLabels), nil
	default:
		return false, nil
	}
}

// lastUserMessageHash — хеш последнего настоящего пользовательского сообщения.
func lastUserMessageHash(records []record) string {
	for i := len(records) - 1; i >= 0; i-- {
		r := records[i]
		if r.Type != "user" || r.Subtype != "" || r.Message == nil {
			continue
		}
		var texts []string
		for _, p := range r.Message.Parts {
			if p.FunctionCall != nil || p.FunctionResponse != nil {
				continue
			}
			if p.Text != "" {
				texts = append(texts, p.Text)
			}
		}
		if len(texts) > 0 {
			return textnorm.NormHash(strings.Join(texts, "\n"))
		}
	}
	return ""
}

// questionAnswerProof — результат ask_user_question содержит ответ.
func questionAnswerProof(records []record, hash string, optionLabels []string) bool {
	for i := len(records) - 1; i >= 0; i-- {
		r := records[i]
		if r.Type != "user" || r.Message == nil {
			continue
		}
		for _, p := range r.Message.Parts {
			fr := p.FunctionResponse
			if fr == nil || fr.Name != "ask_user_question" || fr.Response == nil {
				continue
			}
			s := responseToString(fr.Response)
			if hash != "" && textnorm.NormHash(s) == hash {
				return true
			}
			for _, l := range optionLabels {
				if l != "" && strings.Contains(s, l) {
					return true
				}
			}
			return false
		}
	}
	return false
}

// responseToString — ответ functionResponse в строку.
func responseToString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case map[string]any:
		if s, ok := t["answer"].(string); ok {
			return s
		}
		if s, ok := t["text"].(string); ok {
			return s
		}
	}
	return ""
}
