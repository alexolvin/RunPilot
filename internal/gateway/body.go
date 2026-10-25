package gateway

import (
	"encoding/json"
	"fmt"
)

// transformBodyDoc — разбор тела генерирующего запроса (раздел 6 ТЗ):
// подмена model, ограничение max_tokens/max_completion_tokens сверху
// server.max_output_tokens. Декодирование только в map[string]json.RawMessage:
// всё остальное (массивы content, image_url, video_url, неизвестные поля)
// проходит побайто без изменений. Типизированные структуры сообщений
// в шлюзе запрещены.
func transformBodyDoc(raw []byte, model string, maxOutput int) (map[string]json.RawMessage, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("gateway: тело запроса: %w", err)
	}
	modelJSON, err := json.Marshal(model)
	if err != nil {
		return nil, fmt.Errorf("gateway: model: %w", err)
	}
	doc["model"] = modelJSON
	for _, key := range []string{"max_tokens", "max_completion_tokens"} {
		v, ok := doc[key]
		if !ok {
			continue
		}
		var n int
		if err := json.Unmarshal(v, &n); err != nil {
			continue
		}
		if n <= 0 {
			// Неуправляемое значение: апстрим отклонит (vLLM: 400 «max_tokens
			// must be at least 1»; W9 — реальный клиент qwen шлёт 0) → поле
			// убираем, апстрим применит свой дефолт.
			delete(doc, key)
			continue
		}
		// 0 в max_output_tokens = без лимита (W9: сервер, созданный без
		// явного max_output_tokens, не должен обнулять max_* клиента).
		if maxOutput > 0 && n > maxOutput {
			b, err := json.Marshal(maxOutput)
			if err != nil {
				return nil, fmt.Errorf("gateway: %s: %w", key, err)
			}
			doc[key] = b
		}
	}
	return doc, nil
}

// transformBody — обработка тела генерирующего запроса (раздел 6 ТЗ).
func transformBody(raw []byte, model string, maxOutput int) ([]byte, error) {
	doc, err := transformBodyDoc(raw, model, maxOutput)
	if err != nil {
		return nil, err
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("gateway: сборка тела: %w", err)
	}
	return out, nil
}

// isGenerativePath — генерирующие пути (раздел 6 ТЗ): только они
// участвуют в арендах.
func isGenerativePath(path string) bool {
	switch path {
	case "v1/chat/completions", "v1/completions", "v1/responses":
		return true
	}
	return false
}

