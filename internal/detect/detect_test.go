package detect_test

import (
	"regexp"
	"strings"
	"testing"

	"runpilot/internal/detect"
)

func testRegexps(t *testing.T) detect.Regexps {
	t.Helper()
	must := func(p string) *regexp.Regexp {
		t.Helper()
		re, err := regexp.Compile(p)
		if err != nil {
			t.Fatalf("regexp %q: %v", p, err)
		}
		return re
	}
	return detect.Regexps{
		Wait:          must(`Initializing|Context (8[5-9]|9[0-9]|100)% used`),
		Busy:          must(`Enter to steer|esc to cancel`),
		Approval:      must(`Waiting for user confirmation`),
		Idle:          must(`^>`),
		Placeholder:   must(`^Type your message`),
		InputTop:      must(`^>`),
		InputBottom:   must(`^─+$`),
		InputStrip:    must(`^>\s*|^ {2}|\s+\x{200B}$|\x{200B}`),
		ClassifyLines: 20,
		TabWidth:      8,
	}
}

func TestNormalize(t *testing.T) {
	r := testRegexps(t)
	raw := "line one   \nline\ttwo\n"
	if got, want := detect.Normalize(raw, r), "line one\nline        two"; got != want {
		t.Fatalf("Normalize = %q, want %q", got, want)
	}

	// Volatile-строки удаляются.
	r.Volatile = []*regexp.Regexp{regexp.MustCompile(`^volatile`)}
	if got, want := detect.Normalize("keep\nvolatile line\nkeep2\n", r), "keep\nkeep2"; got != want {
		t.Fatalf("Normalize volatile = %q, want %q", got, want)
	}

	// Пустой volatile-список ничего не удаляет.
	if got, want := detect.Normalize("a\n\nb\n", testRegexps(t)), "a\n\nb"; got != want {
		t.Fatalf("Normalize empty = %q, want %q", got, want)
	}
}

func TestClassifyOrder(t *testing.T) {
	r := testRegexps(t)
	cases := []struct {
		name  string
		lines string
		want  detect.State
	}{
		{"wait wins over busy", "Enter to steer\nContext 100% used", detect.WaitUI},
		{"wait wins over idle", "> x\nInitializing", detect.WaitUI},
		{"busy wins over idle", "> x\nEnter to steer", detect.Busy},
		{"busy esc to cancel", "> x\nWorking... (1s · esc to cancel)", detect.Busy},
		{"approval wins over idle", "> 4. No, suggest changes (esc)\nWaiting for user confirmation", detect.Prompt},
		{"idle", "> x\n⏸ Ask permissions (shift + tab to cycle)", detect.Idle},
		{"unknown", "nothing matches here\nat all", detect.Unknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := detect.Classify(tc.lines, r); got != tc.want {
				t.Fatalf("Classify(%q) = %v, want %v", tc.lines, got, tc.want)
			}
		})
	}
}

func TestClassifyWindow(t *testing.T) {
	r := testRegexps(t)
	// Маркер в окне последних ClassifyLines непустых строк виден,
	// за окном — нет.
	lines := make([]string, 0, r.ClassifyLines*2+1)
	for i := 0; i < r.ClassifyLines; i++ {
		lines = append(lines, "filler line")
	}
	lines = append(lines, "Enter to steer")
	if got := detect.Classify(strings.Join(lines, "\n"), r); got != detect.Busy {
		t.Fatalf("маркер в окне: got %v, want Busy", got)
	}
	for i := 0; i < r.ClassifyLines; i++ {
		lines = append(lines, "filler line")
	}
	if got := detect.Classify(strings.Join(lines, "\n"), r); got != detect.Unknown {
		t.Fatalf("маркер за окном: got %v, want Unknown", got)
	}
}

func TestExtractInput(t *testing.T) {
	r := testRegexps(t)
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			"single line with ZWSP cursor",
			"───\n> Однострочный тестовый промпт \u200b\n───\n",
			"Однострочный тестовый промпт",
		},
		{
			"multiline",
			"> Первая строка\n  Вторая строка \u200b\n───\n",
			"Первая строка\nВторая строка",
		},
		{
			"placeholder is empty",
			"───\n>   Type your message or @path/to/file  \n───\n",
			"",
		},
		{
			"no input box",
			"  Waiting for user confirmation...\n",
			"",
		},
		{
			"border only above top",
			"───\n> x\n",
			"",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			norm := detect.Normalize(tc.in, r)
			if got := detect.ExtractInput(norm, r); got != tc.want {
				t.Fatalf("ExtractInput = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestHashFNV1a(t *testing.T) {
	// FNV-1a 64: известное значение для пустой строки (offset basis).
	const fnvOffsetBasis = uint64(0xcbf29ce484222325)
	if got := detect.Hash(""); got != fnvOffsetBasis {
		t.Fatalf("Hash(\"\") = %#x, want %#x (FNV-1a 64 offset basis)", got, fnvOffsetBasis)
	}
	if a, b := detect.Hash("абв\ngде"), detect.Hash("абв\ngде"); a != b {
		t.Fatalf("Hash недетерминирован: %#x != %#x", a, b)
	}
	if detect.Hash("абв\ngде") == detect.Hash("абв\ngде ") {
		t.Fatalf("Hash не различает входные данные")
	}
}
