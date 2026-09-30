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
