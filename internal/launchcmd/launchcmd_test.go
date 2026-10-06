package launchcmd

import (
	"reflect"
	"testing"
)

func TestParsePromptAndEnter(t *testing.T) {
	got := Parse("запусти тесты[Enter]")
	want := []Token{{Kind: Text, Value: "запусти тесты"}, {Kind: Key, Value: "Enter"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse = %+v, хочу %+v", got, want)
	}
}

func TestParseCursorsPromptEnter(t *testing.T) {
	got := Parse("[Up][Down] исправь баг[Enter]")
	want := []Token{
		{Kind: Key, Value: "Up"},
		{Kind: Key, Value: "Down"},
		{Kind: Text, Value: " исправь баг"},
		{Kind: Key, Value: "Enter"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse = %+v, хочу %+v", got, want)
	}
}

func TestParseJustEnter(t *testing.T) {
	got := Parse("[Enter]")
	want := []Token{{Kind: Key, Value: "Enter"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse = %+v, хочу %+v", got, want)
	}
}

func TestParseLiteralBrackets(t *testing.T) {
	// «[TODO]» — не токен, остаётся текстом.
	got := Parse("поставь [TODO] в коде")
	want := []Token{{Kind: Text, Value: "поставь [TODO] в коде"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse = %+v, хочу %+v", got, want)
	}
}

func TestParseEscSpace(t *testing.T) {
	got := Parse("[Esc][Space]x")
	want := []Token{
		{Kind: Key, Value: "Escape"},
		{Kind: Key, Value: "Space"},
		{Kind: Text, Value: "x"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse = %+v, хочу %+v", got, want)
	}
}

func TestParseEmpty(t *testing.T) {
	if got := Parse(""); len(got) != 0 {
		t.Fatalf("Parse(\"\") = %+v, хочу пусто", got)
	}
}

func TestHasKey(t *testing.T) {
	if !HasKey("[Enter]") {
		t.Error("HasKey([Enter]) = false, хочу true")
	}
	if !HasKey("текст [Up] ещё") {
		t.Error("HasKey(текст [Up] ещё) = false, хочу true")
	}
	if HasKey("просто текст") {
		t.Error("HasKey(просто текст) = true, хочу false")
	}
	if HasKey("текст [TODO]") {
		t.Error("HasKey([TODO]) = true, хочу false (не токен)")
	}
}

func TestHasEnter(t *testing.T) {
	if !HasEnter("промпт[Enter]") {
		t.Error("HasEnter(промпт[Enter]) = false, хочу true")
	}
	if HasEnter("[Esc]") {
		t.Error("HasEnter([Esc]) = true, хочу false")
	}
}

func TestIsTextOnly(t *testing.T) {
	if !IsTextOnly("просто текст") {
		t.Error("IsTextOnly(просто текст) = false, хочу true")
	}
	if IsTextOnly("текст[Enter]") {
		t.Error("IsTextOnly(текст[Enter]) = true, хочу false")
	}
}

func TestEnsureTrailingEnter(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "[Enter]"},
		{"   ", "[Enter]"},
		{"промпт", "промпт[Enter]"},
		{"промпт[Enter]", "промпт[Enter]"},
		{"промпт[Enter]   ", "промпт[Enter]"},
		{"[Up][Down]тест", "[Up][Down]тест[Enter]"},
	}
	for _, c := range cases {
		if got := EnsureTrailingEnter(c.in); got != c.want {
			t.Errorf("EnsureTrailingEnter(%q) = %q, хочу %q", c.in, got, c.want)
		}
	}
}

func TestPlainText(t *testing.T) {
	if got := PlainText("запусти[Enter]тест[Up]"); got != "запуститест" {
		t.Fatalf("PlainText = %q, хочу %q", got, "запуститест")
	}
}
