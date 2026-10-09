package sid

import (
	"strings"
	"testing"
)

func TestSIDLengthAndAlphabet(t *testing.T) {
	for i := 0; i < 100; i++ {
		s, err := New()
		if err != nil {
			t.Fatal(err)
		}
		if len(s) != Length {
			t.Fatalf("длина %q = %d, хочу %d", s, len(s), Length)
		}
		for _, r := range s {
			if !strings.ContainsRune(Alphabet, r) {
				t.Fatalf("символ %q вне алфавита в %q", r, s)
			}
		}
	}
}

func TestSIDUniqueness(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 10000; i++ {
		s, err := New()
		if err != nil {
			t.Fatal(err)
		}
		if seen[s] {
			t.Fatalf("дубликат %q за 10000 генераций", s)
		}
		seen[s] = true
	}
}

func TestValid(t *testing.T) {
	// Валидные sids.
	s, _ := New()
	if !Valid(s) {
		t.Errorf("Valid(%q) = false, хочу true", s)
	}
	if !Valid("0123456789") {
		t.Error("Valid(0123456789) = false")
	}
	if !Valid("ABCDEFGHJK") {
		t.Error("Valid(ABCDEFGHJK) = false")
	}

	// Невалидные: неправильная длина.
	if Valid("short") {
		t.Error("Valid(short) = true")
	}
	if Valid("01234567890") { // 11 символов
		t.Error("Valid(11 chars) = true")
	}

	// Невалидные: символы вне алфавита.
	if Valid("W_EZPODRYS") { // подчёркивание
		t.Error("Valid(W_EZPODRYS) = true")
	}
	if Valid("012345678I") { // I — гласная
		t.Error("Valid(с I) = true")
	}
	if Valid("012345678L") { // L — гласная
		t.Error("Valid(с L) = true")
	}
	if Valid("012345678O") { // O — гласная
		t.Error("Valid(с O) = true")
	}
	if Valid("012345678U") { // U — гласная
		t.Error("Valid(с U) = true")
	}
}
