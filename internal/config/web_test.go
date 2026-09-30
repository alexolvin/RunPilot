package config

import "testing"

// W1 CONTROL: не петлевой web.bind, пустой или короткий токен → отказ старта.

func TestValidateWebRefusesNonLoopback(t *testing.T) {
	cases := map[string]struct {
		bind  string
		wantOK bool
	}{
		"loopback v4":  {bind: "127.0.0.1", wantOK: true},
		"loopback v6":  {bind: "::1", wantOK: true},
		"any v4":       {bind: "0.0.0.0", wantOK: false},
		"private ip":   {bind: "192.168.1.10", wantOK: false},
		"empty":        {bind: "", wantOK: false},
		"hostname-ish": {bind: "localhost", wantOK: false}, // не IP → не петлевой
	}
	for name, tc := range cases {
		cfg := Defaults()
		cfg.Web.Bind = tc.bind
		err := cfg.ValidateWeb()
		if tc.wantOK && err != nil {
			t.Errorf("%s: web.bind=%q ожидалось OK, а %v", name, tc.bind, err)
		}
		if !tc.wantOK && err == nil {
			t.Errorf("%s: web.bind=%q ожидался отказ старта, а OK", name, tc.bind)
		}
	}
}

func TestValidateToken(t *testing.T) {
	cfg := Defaults()
	// Пустой токен — отказ.
	if err := cfg.ValidateToken(""); err == nil {
		t.Errorf("пустой токен: ожидался отказ старта")
	}
	// Короткий токен (< limits.token_min_len) — отказ.
	_ = cfg.ValidateToken("short")
	min := cfg.Limits.TokenMinLen
	if min <= 0 {
		t.Fatalf("limits.token_min_len = %d, должен быть > 0", min)
	}
	short := make([]byte, min-1)
	for i := range short {
		short[i] = 'a'
	}
	if err := cfg.ValidateToken(string(short)); err == nil {
		t.Errorf("короткий токен (длина %d < %d): ожидался отказ", len(short), min)
	}
	// Достаточный токен — OK.
	ok := make([]byte, min)
	for i := range ok {
		ok[i] = 'a'
	}
	if err := cfg.ValidateToken(string(ok)); err != nil {
		t.Errorf("токен длины %d: ожидалось OK, а %v", len(ok), err)
	}
}
