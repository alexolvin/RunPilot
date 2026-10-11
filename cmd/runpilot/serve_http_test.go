package main

import (
	"testing"
	"time"
)

// п.2: у созданного сервера шлюза ReadTimeout = 0 и WriteTimeout = 0.
// ReadTimeout = 0 обязателен: шлюз молча удерживает генерирующий запрос до
// gateway.hold_max_sec (без байта к клиенту) — клиент на это время не шлёт
// байты, таймаут чтения оборвал бы удержание.
func TestGatewayServerTimeouts(t *testing.T) {
	s := newHTTPServer(nil, 30*time.Second)
	if s.ReadTimeout != 0 {
		t.Fatalf("ReadTimeout = %v, хочу 0 (удержание запроса молча)", s.ReadTimeout)
	}
	if s.WriteTimeout != 0 {
		t.Fatalf("WriteTimeout = %v, хочу 0 (длинные SSE-потоки)", s.WriteTimeout)
	}
	if s.ReadHeaderTimeout != 30*time.Second {
		t.Fatalf("ReadHeaderTimeout = %v, хочу 30s (gateway.read_header_timeout_sec)", s.ReadHeaderTimeout)
	}
}
