package config

// Проверки раздела 4.5 (config.Validate): имя сервера, URL, slots, accept,
// перекрёстные ограничения turn/dispatch/gateway.

import (
	"strings"
	"testing"
)

func srv45(name string, slots int, accept []string, upstreamURL string) Server {
	return Server{
		Name: name, Slots: slots, Accept: accept,
		HealthURL: "http://192.0.2.1:8004/health", MetricsURL: "http://192.0.2.1:8004/metrics",
		MaxOutputTokens: 16384, FirstByteTimeoutSec: 900,
		Upstreams: Upstreams{OpenAI: UpstreamOpenAI{
			URL: upstreamURL, Model: "m", KeyEnv: "K"}},
	}
}

func validAccept() []string { return []string{"resume", "high", "normal", "low"} }

// 4.5: имя сервера соответствует шаблону [a-z0-9-]{1,32}.
func TestValidate45ServerNameTemplate(t *testing.T) {
	cfg := Defaults()
	cfg.Servers = []Server{srv45("Bad_Name", 1, validAccept(), "http://192.0.2.1:8004")}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "шаблон") {
		t.Fatalf("имя с подчёркиванием/регистром должно отклоняться: %v", err)
	}
	// Слишком длинное имя (>32).
	long := make([]byte, 33)
	for i := range long {
		long[i] = 'a'
	}
	cfg.Servers = []Server{srv45(string(long), 1, validAccept(), "http://192.0.2.1:8004")}
	if err := cfg.Validate(); err == nil {
		t.Fatal("имен >32 символов должно отклоняться")
	}
	// Корректное имя проходит.
	cfg.Servers = []Server{srv45("good-name-1", 1, validAccept(), "http://192.0.2.1:8004")}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("корректное имя: %v", err)
	}
}

// 4.5: имя сервера уникально.
func TestValidate45ServerNameUnique(t *testing.T) {
	cfg := Defaults()
	cfg.Servers = []Server{
		srv45("dup", 1, validAccept(), "http://192.0.2.1:8004"),
		srv45("dup", 1, validAccept(), "http://192.0.2.2:8004"),
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "дублирующ") {
		t.Fatalf("дублирующееся имя должно отклоняться: %v", err)
	}
}

// 4.5: URL — абсолютный http/https.
func TestValidate45URLAbsolute(t *testing.T) {
	cfg := Defaults()
	cfg.Servers = []Server{srv45("s", 1, validAccept(), "192.0.2.1:8004")} // без схемы
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "http") {
		t.Fatalf("URL без схемы должен отклоняться: %v", err)
	}
	cfg.Servers = []Server{srv45("s", 1, validAccept(), "ftp://192.0.2.1:8004")} // не http
	if err := cfg.Validate(); err == nil {
		t.Fatal("URL со схемой ftp должен отклоняться")
	}
	cfg.Servers = []Server{srv45("s", 1, validAccept(), "https://192.0.2.1:8004")}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("https URL: %v", err)
	}
}

// 4.5: slots ≥ 1.
func TestValidate45Slots(t *testing.T) {
	cfg := Defaults()
	cfg.Servers = []Server{srv45("s", 0, validAccept(), "http://192.0.2.1:8004")}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "slots") {
		t.Fatalf("slots=0 должно отклоняться: %v", err)
	}
}

// 4.5: accept не пуст.
func TestValidate45AcceptNonEmpty(t *testing.T) {
	cfg := Defaults()
	cfg.Servers = []Server{srv45("s", 1, nil, "http://192.0.2.1:8004")}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "accept") {
		t.Fatalf("пустой accept должен отклоняться: %v", err)
	}
}

// 4.5: turn.done_quiet_sec и turn.done_stable_sec < dispatch.start_confirm_sec.
func TestValidate45DoneLessThanStartConfirm(t *testing.T) {
	cfg := Defaults()
	cfg.Turn.DoneQuietSec = 20 // == start_confirm (20)
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "done_quiet") {
		t.Fatalf("done_quiet_sec >= start_confirm должно отклоняться: %v", err)
	}
	cfg = Defaults()
	cfg.Turn.DoneStableSec = 21 // > start_confirm
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "done_stable") {
		t.Fatalf("done_stable_sec >= start_confirm должно отклоняться: %v", err)
	}
	// Корректное: done (5) < start_confirm (20).
	cfg = Defaults()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("корректные значения: %v", err)
	}
}

// В.7: инвариант client_timeout_sec > hold_max_sec + max(servers[].first_byte_timeout_sec).
// Нарушение — отрицание (<=), включая равенство. first_byte сервера srv45 = 900.
func TestValidateV7ClientTimeoutFloor(t *testing.T) {
	const fb = 900 // srv45
	srv := func() []Server {
		return []Server{srv45("s", 1, validAccept(), "http://192.0.2.1:8004")}
	}

	// дефолты (client_timeout 5400 / hold_max 3600) + first_byte=900: 5400 > 4500 → OK.
	cfg := Defaults()
	cfg.Servers = srv()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("дефолты (5400/3600) + first_byte=900: %v", err)
	}
	// равенство (hold+max_fb) → нарушение.
	cfg = Defaults()
	cfg.Servers = srv()
	cfg.Profiles.Qwen.ClientTimeoutSec = 3600 + fb
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "client_timeout_sec") {
		t.Fatalf("равенство hold+max_fb должно отклоняться: %v", err)
	}
	// hold+max_fb − 1 → нарушение.
	cfg = Defaults()
	cfg.Servers = srv()
	cfg.Profiles.Qwen.ClientTimeoutSec = 3600 + fb - 1
	if err := cfg.Validate(); err == nil {
		t.Fatal("hold+max_fb−1 должно отклоняться")
	}
	// hold+max_fb + 1 → OK.
	cfg = Defaults()
	cfg.Servers = srv()
	cfg.Profiles.Qwen.ClientTimeoutSec = 3600 + fb + 1
	if err := cfg.Validate(); err != nil {
		t.Fatalf("hold+max_fb+1: %v", err)
	}
	// стенд: hold_max=600, first_byte=900, client_timeout по умолчанию 5400 → OK.
	cfg = Defaults()
	cfg.Servers = srv()
	cfg.Gateway.HoldMaxSec = 600
	if err := cfg.Validate(); err != nil {
		t.Fatalf("конфиг стенда (600/900/5400): %v", err)
	}
}
