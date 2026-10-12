package llmassist

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// mockLLM — возвращает заранее размеченный ответ LLM из testdata.
type mockLLM struct {
	answer string
	calls  int
}

func (m *mockLLM) Call(_ context.Context, _ string) (string, error) {
	m.calls++
	return m.answer, nil
}

// flakyLLM — первые n вызовов невалидны, затем корректный ответ (проверка повтора).
type flakyLLM struct {
	bad    int
	good   string
	calls  int
}

func (m *flakyLLM) Call(_ context.Context, _ string) (string, error) {
	m.calls++
	if m.calls <= m.bad {
		return "not-json", nil
	}
	return m.good, nil
}

func loadCases(t *testing.T, name string, v any) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read testdata: %v", err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("unmarshal testdata: %v", err)
	}
}

type reportCase struct {
	Name      string   `json:"name"`
	Messages  []string `json:"messages"`
	LLM       string   `json:"llm"`
	WantStart *int     `json:"want_start"`
}

func TestReportReferenceSet(t *testing.T) {
	var file struct {
		Cases []reportCase `json:"cases"`
	}
	loadCases(t, "report.json", &file)
	if len(file.Cases) != 20 {
		t.Fatalf("want 20 cases, got %d", len(file.Cases))
	}
	a := New(&mockLLM{}, Config{MaxTokens: 512, TimeoutSec: 60, MaxInputKB: 64, MinQuoteChars: 8})
	for _, c := range file.Cases {
		a.caller = &mockLLM{answer: c.LLM}
		res, err := a.Report(context.Background(), c.Messages)
		if err != nil {
			t.Errorf("%s: unexpected error: %v", c.Name, err)
			continue
		}
		if res.Hold != "" {
			t.Errorf("%s: unexpected hold %q", c.Name, res.Hold)
			continue
		}
		if (res.Start == nil) != (c.WantStart == nil) || (res.Start != nil && *res.Start != *c.WantStart) {
			t.Errorf("%s: start = %v, want %v", c.Name, res.Start, c.WantStart)
		}
		if !res.LLMUsed {
			t.Errorf("%s: LLMUsed = false", c.Name)
		}
	}
}

type answerCase struct {
	Name      string `json:"name"`
	Answer    string `json:"answer"`
	LLM       string `json:"llm"`
	WantBlock *int   `json:"want_block"`
	WantQuote string `json:"want_quote"`
	WantNull  bool   `json:"want_null"`
}

func TestAnswerReferenceSet(t *testing.T) {
	var file struct {
		Cases []answerCase `json:"cases"`
	}
	loadCases(t, "answer.json", &file)
	if len(file.Cases) != 20 {
		t.Fatalf("want 20 cases, got %d", len(file.Cases))
	}
	a := New(&mockLLM{}, Config{MaxTokens: 512, TimeoutSec: 60, MaxInputKB: 64, MinQuoteChars: 8})
	for _, c := range file.Cases {
		a.caller = &mockLLM{answer: c.LLM}
		res, err := a.Answer(context.Background(), c.Answer)
		if err != nil {
			t.Errorf("%s: unexpected error: %v", c.Name, err)
			continue
		}
		if res.Hold != "" {
			t.Errorf("%s: unexpected hold %q", c.Name, res.Hold)
			continue
		}
		if c.WantNull {
			if res.Answer != nil {
				t.Errorf("%s: want null, got %+v", c.Name, res.Answer)
			}
			continue
		}
		if res.Answer == nil {
			t.Errorf("%s: Answer = nil, want %+v", c.Name, c.WantBlock)
			continue
		}
		if (res.Answer.Block == nil) != (c.WantBlock == nil) || (res.Answer.Block != nil && *res.Answer.Block != *c.WantBlock) {
			t.Errorf("%s: block = %v, want %v", c.Name, res.Answer.Block, c.WantBlock)
		}
		if res.Answer.Quote != c.WantQuote {
			t.Errorf("%s: quote = %q, want %q", c.Name, res.Answer.Quote, c.WantQuote)
		}
	}
}

type dialogCase struct {
	Name      string         `json:"name"`
	Questions string         `json:"questions"`
	Answer    string         `json:"answer"`
	LLM       string         `json:"llm"`
	Want      []DialogAnswer `json:"want"`
}

func TestDialogReferenceSet(t *testing.T) {
	var file struct {
		Cases []dialogCase `json:"cases"`
	}
	loadCases(t, "dialog.json", &file)
	if len(file.Cases) != 20 {
		t.Fatalf("want 20 cases, got %d", len(file.Cases))
	}
	a := New(&mockLLM{}, Config{MaxTokens: 512, TimeoutSec: 60, MaxInputKB: 64, MinQuoteChars: 8})
	for _, c := range file.Cases {
		a.caller = &mockLLM{answer: c.LLM}
		res, err := a.Dialog(context.Background(), c.Questions, c.Answer)
		if err != nil {
			t.Errorf("%s: unexpected error: %v", c.Name, err)
			continue
		}
		if res.Hold != "" {
			t.Errorf("%s: unexpected hold %q", c.Name, res.Hold)
			continue
		}
		if len(res.Dialog) != len(c.Want) {
			t.Errorf("%s: len(dialog) = %d, want %d", c.Name, len(res.Dialog), len(c.Want))
			continue
		}
		for i, w := range c.Want {
			got := res.Dialog[i]
			if (got.Option == nil) != (w.Option == nil) || (got.Option != nil && *got.Option != *w.Option) {
				t.Errorf("%s[%d]: option = %v, want %v", c.Name, i, got.Option, w.Option)
			}
			if len(got.Options) != len(w.Options) {
				t.Errorf("%s[%d]: options = %v, want %v", c.Name, i, got.Options, w.Options)
			}
			if got.Quote != w.Quote {
				t.Errorf("%s[%d]: quote = %q, want %q", c.Name, i, got.Quote, w.Quote)
			}
		}
	}
}

func TestRetryThenValid(t *testing.T) {
	m := &flakyLLM{bad: 1, good: `{"start": 0}`}
	a := New(m, Config{MaxInputKB: 64})
	res, err := a.Report(context.Background(), []string{"Отчёт: готово."})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if res.Hold != "" || res.Start == nil || *res.Start != 0 {
		t.Fatalf("res = %+v", res)
	}
	if m.calls != 2 {
		t.Fatalf("calls = %d, want 2", m.calls)
	}
}

func TestInvalidAfterRetryHold(t *testing.T) {
	m := &mockLLM{answer: "not-json"}
	a := New(m, Config{MaxInputKB: 64})
	res, err := a.Report(context.Background(), []string{"Отчёт."})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if res.Hold != HoldLLMInvalid {
		t.Fatalf("hold = %q, want %q", res.Hold, HoldLLMInvalid)
	}
	if m.calls != 2 {
		t.Fatalf("calls = %d, want 2 (один повтор)", m.calls)
	}
}

func TestAnswerQuoteNotSubstringHold(t *testing.T) {
	m := &mockLLM{answer: `{"quote": "нет такой подстроки в ответе"}`}
	a := New(m, Config{MinQuoteChars: 8})
	res, err := a.Answer(context.Background(), "Просто подтверждение.")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if res.Hold != HoldLLMInvalid {
		t.Fatalf("hold = %q, want %q", res.Hold, HoldLLMInvalid)
	}
}

func TestReportStartOutOfRangeHold(t *testing.T) {
	m := &mockLLM{answer: `{"start": 5}`}
	a := New(m, Config{MaxInputKB: 64})
	res, err := a.Report(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if res.Hold != HoldLLMInvalid {
		t.Fatalf("hold = %q, want %q", res.Hold, HoldLLMInvalid)
	}
}

func TestKeepTailWholeItems(t *testing.T) {
	items := []string{"aaaa", "bbbb", "cccc"}
	got := keepTail(items, 9)
	if len(got) != 2 || got[0] != "bbbb" {
		t.Fatalf("keepTail = %v, want [bbbb cccc]", got)
	}
}
