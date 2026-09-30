package gateway

import (
	"encoding/json"
	"fmt"
)

// transformBody — обработка тела генерирующего запроса (раздел 6 ТЗ):
// подмена model, ограничение max_tokens/max_completion_tokens сверху
// server.max_output_tokens. Тело прочитано целиком; всё остальное
// (включая stream_options от клиента) проходит без изменений.
func transformBody(raw []byte, model string, maxOutput int) ([]byte, error) {
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("gateway: тело запроса: %w", err)
	}
	doc["model"] = model
	for _, key := range []string{"max_tokens", "max_completion_tokens"} {
		v, ok := doc[key]
		if !ok {
			continue
		}
		n, ok := numberAsInt(v)
		if !ok {
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
			doc[key] = maxOutput
		}
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("gateway: сборка тела: %w", err)
	}
	return out, nil
}

// numberAsInt — JSON-число (в map[string]any — float64) → int.
func numberAsInt(v any) (int, bool) {
	f, ok := v.(float64)
	if !ok {
		return 0, false
	}
	n := int(f)
	if float64(n) != f {
		return 0, false
	}
	return n, true
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

