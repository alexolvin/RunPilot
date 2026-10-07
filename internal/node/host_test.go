package node

// TestHostnameOrDefault — единая функция имени узла (node.host из конфига,
// иначе os.Hostname): exec, embedded-узел и WS hello берут host из неё.
import (
	"testing"
)

func TestHostnameOrDefault(t *testing.T) {
	// node.host задан → используется (тест оператора: host="custom" → "custom").
	if got := HostnameOrDefault("custom"); got != "custom" {
		t.Fatalf("HostnameOrDefault(\"custom\") = %q, want \"custom\"", got)
	}
	// node.host пуст → os.Hostname (те же значения, что даёт Hostname()).
	if got := HostnameOrDefault(""); got != Hostname() {
		t.Fatalf("HostnameOrDefault(\"\") = %q, want Hostname()=%q", got, Hostname())
	}
	// Беспробельное имя с точками тоже проходит как есть.
	if got := HostnameOrDefault("3p14-aaa"); got != "3p14-aaa" {
		t.Fatalf("HostnameOrDefault(\"3p14-aaa\") = %q, want \"3p14-aaa\"", got)
	}
}
