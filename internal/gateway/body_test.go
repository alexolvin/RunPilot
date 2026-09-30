package gateway

import (
	"encoding/json"
	"testing"
)

// W9 (приёмка на железе): реальный клиент qwen шлёт max_tokens=0 → vLLM 400
// «max_tokens must be at least 1»; и cap max_output_tokens=0 (сервер создан
// без явного max_output_tokens) обнулял любые положительные max_* клиента.
func TestBodyTransformNonPositiveAndNoCap(t *testing.T) {
	out, err := transformBody([]byte(`{"model":"x","max_tokens":0,"max_completion_tokens":0}`), "srv-model", 1000)
	if err != nil {
		t.Fatalf("transformBody: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("json: %v", err)
	}
	if _, ok := doc["max_tokens"]; ok {
		t.Fatal("max_tokens=0 должен быть убран из тела")
	}
	if _, ok := doc["max_completion_tokens"]; ok {
		t.Fatal("max_completion_tokens=0 должно быть убрано из тела")
	}

	// 0 в max_output_tokens = без лимита: положительное значение клиента
	// проходит как есть.
	out, err = transformBody([]byte(`{"model":"x","max_tokens":5000}`), "srv-model", 0)
	if err != nil {
		t.Fatalf("transformBody: %v", err)
	}
	doc = map[string]any{}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("json: %v", err)
	}
	if f, _ := doc["max_tokens"].(float64); int(f) != 5000 {
		t.Fatalf("без лимита max_tokens = %v, хочу 5000", doc["max_tokens"])
	}

	// Явный лимит по-прежнему обрезает сверху (регрессия TestBodyTransform).
	out, err = transformBody([]byte(`{"model":"x","max_tokens":5000}`), "srv-model", 1000)
	if err != nil {
		t.Fatalf("transformBody: %v", err)
	}
	doc = map[string]any{}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("json: %v", err)
	}
	if f, _ := doc["max_tokens"].(float64); int(f) != 1000 {
		t.Fatalf("с лимитом max_tokens = %v, хочу 1000", doc["max_tokens"])
	}
}
