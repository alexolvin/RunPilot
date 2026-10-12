package agentlog

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// writeJSONL — записать строки в tmp-файл.
func writeJSONL(t *testing.T, lines []string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "rec.jsonl")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func cfg() Config {
	return Config{ReportKeywords: []string{"готово", "отчёт", "отчет"}, AttachMaxFiles: 5}
}

// TestRecordingWithMarker — ход с маркером: отчёт = сообщение-маркер + поздние.
func TestRecordingWithMarker(t *testing.T) {
	lines := []string{
		`{"type":"user","uuid":"u1","message":{"role":"user","parts":[{"text":"Шаг 1"}]}}`,
		`{"type":"assistant","uuid":"a1","message":{"role":"model","parts":[{"text":"Думаю","thought":true},{"text":"Делаю файл."}]}}`,
		`{"type":"tool_result","uuid":"t1","message":{"role":"user","parts":[{"functionResponse":{"id":"c1","name":"write_file","response":{"output":"ok"}}}]}}`,
		`{"type":"assistant","uuid":"a2","message":{"role":"model","parts":[{"text":"Готово, отчёт в step1.md"}]}}`,
	}
	rep, err := ParseRecording(writeJSONL(t, lines), "/proj", cfg())
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Found {
		t.Fatal("маркер не найден")
	}
	want := "Готово, отчёт в step1.md"
	if rep.Report != want {
		t.Fatalf("отчёт = %q, хочу %q", rep.Report, want)
	}
	if rep.Source != SourceRecording {
		t.Fatalf("source = %q", rep.Source)
	}
}

// TestRecordingNoMarker — маркера нет: Found=false, отчёт пуст.
func TestRecordingNoMarker(t *testing.T) {
	lines := []string{
		`{"type":"user","uuid":"u1","message":{"role":"user","parts":[{"text":"Привет"}]}}`,
		`{"type":"assistant","uuid":"a1","message":{"role":"model","parts":[{"text":"Здравствуйте, чем помочь?"}]}}`,
	}
	rep, err := ParseRecording(writeJSONL(t, lines), "/proj", cfg())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Found {
		t.Fatal("маркер найден, а быть не должен")
	}
	if rep.Report != "" {
		t.Fatalf("отчёт = %q, хочу пусто", rep.Report)
	}
}

// TestRecordingThoughtExcluded — thought-части не попадают в отчёт.
func TestRecordingThoughtExcluded(t *testing.T) {
	lines := []string{
		`{"type":"user","uuid":"u1","message":{"role":"user","parts":[{"text":"задача"}]}}`,
		`{"type":"assistant","uuid":"a1","message":{"role":"model","parts":[{"text":"готово (в размышлении)","thought":true},{"text":"Готово."}]}}`,
	}
	rep, err := ParseRecording(writeJSONL(t, lines), "/proj", cfg())
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Found {
		t.Fatal("маркер не найден")
	}
	if strings.Contains(rep.Report, "в размышлении") {
		t.Fatalf("thought-часть попала в отчёт: %q", rep.Report)
	}
	if rep.Report != "Готово." {
		t.Fatalf("отчёт = %q", rep.Report)
	}
}

// TestRecordingFiles — write_file/edit .md внутри каталога сессии.
func TestRecordingFiles(t *testing.T) {
	dir := t.TempDir()
	// Файлы в каталоге сессии.
	inA := filepath.Join(dir, "a.md")
	inB := filepath.Join(dir, "sub", "b.md")
	outside := filepath.Join(t.TempDir(), "c.md")
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{inA, inB, outside} {
		if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// Не-.md внутри каталога — не отбирается.
	txt := filepath.Join(dir, "notes.txt")
	_ = os.WriteFile(txt, []byte("x"), 0o600)

	lines := []string{
		`{"type":"user","uuid":"u1","message":{"role":"user","parts":[{"text":"задача"}]}}`,
		`{"type":"assistant","uuid":"a1","message":{"role":"model","parts":[{"functionCall":{"id":"c1","name":"write_file","args":{"file_path":"`+inA+`","content":"a"}}},{"functionCall":{"id":"c2","name":"write_file","args":{"file_path":"`+inB+`","content":"b"}}},{"functionCall":{"id":"c3","name":"write_file","args":{"file_path":"`+outside+`","content":"c"}}},{"functionCall":{"id":"c4","name":"write_file","args":{"file_path":"`+txt+`","content":"t"}}}]}}`,
		`{"type":"assistant","uuid":"a2","message":{"role":"model","parts":[{"text":"Готово."}]}}`,
	}
	rep, err := ParseRecording(writeJSONL(t, lines), dir, cfg())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, f := range rep.Files {
		got[f.Path] = true
	}
	if !got[inA] || !got[inB] {
		t.Fatalf("файлы внутри каталога не найдены: %v", rep.Files)
	}
	if got[outside] {
		t.Fatal("файл вне каталога отобран")
	}
	if got[txt] {
		t.Fatal("не-.md отобран")
	}
	if len(rep.Files) != 2 {
		t.Fatalf("число файлов = %d, хочу 2", len(rep.Files))
	}
}

// TestRecordingQuestionSingle — неотвеченный ask_user_question (1 вопрос).
func TestRecordingQuestionSingle(t *testing.T) {
	lines := []string{
		`{"type":"user","uuid":"u1","message":{"role":"user","parts":[{"text":"задача"}]}}`,
		`{"type":"assistant","uuid":"a1","message":{"role":"model","parts":[{"functionCall":{"id":"q1","name":"ask_user_question","args":{"questions":[{"question":"Какой вариант?","options":[{"label":"A","description":"первый"},{"label":"B","description":"второй"}]}]}}}]}}`,
	}
	rep, err := ParseRecording(writeJSONL(t, lines), "/proj", cfg())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Question == nil {
		t.Fatal("вопрос не найден")
	}
	if len(rep.Question.Questions) != 1 {
		t.Fatalf("число вопросов = %d", len(rep.Question.Questions))
	}
	q := rep.Question.Questions[0]
	if q.Question != "Какой вариант?" {
		t.Fatalf("question = %q", q.Question)
	}
	if len(q.Options) != 2 || q.Options[0].Label != "A" || q.Options[1].Label != "B" {
		t.Fatalf("options = %+v", q.Options)
	}
	if q.MultiSelect {
		t.Fatal("multiSelect должен быть false")
	}
}

// TestRecordingQuestionAnswered — отвеченный ask_user_question → вопроса нет.
func TestRecordingQuestionAnswered(t *testing.T) {
	lines := []string{
		`{"type":"user","uuid":"u1","message":{"role":"user","parts":[{"text":"задача"}]}}`,
		`{"type":"assistant","uuid":"a1","message":{"role":"model","parts":[{"functionCall":{"id":"q1","name":"ask_user_question","args":{"questions":[{"question":"Вариант?","options":[{"label":"A"}]}]}}}]}}`,
		`{"type":"tool_result","uuid":"t1","message":{"role":"user","parts":[{"functionResponse":{"id":"q1","name":"ask_user_question","response":{"output":"A"}}}]}}`,
	}
	rep, err := ParseRecording(writeJSONL(t, lines), "/proj", cfg())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Question != nil {
		t.Fatalf("отвеченный вопрос не должен быть: %+v", rep.Question)
	}
}

// TestRecordingMultiselect — multiSelect=true.
func TestRecordingMultiselect(t *testing.T) {
	lines := []string{
		`{"type":"user","uuid":"u1","message":{"role":"user","parts":[{"text":"задача"}]}}`,
		`{"type":"assistant","uuid":"a1","message":{"role":"model","parts":[{"functionCall":{"id":"q1","name":"ask_user_question","args":{"questions":[{"question":"Выберите","options":[{"label":"X"},{"label":"Y"}],"multiSelect":true}]}}}]}}`,
	}
	rep, err := ParseRecording(writeJSONL(t, lines), "/proj", cfg())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Question == nil || !rep.Question.Questions[0].MultiSelect {
		t.Fatalf("multiSelect не распознан: %+v", rep.Question)
	}
}

// TestTerminalWithMarker — терминал: отчёт от ◆ до последнего input_top.
func TestTerminalWithMarker(t *testing.T) {
	text := "> шаг 1\n◆ Готово, отчёт в step1.md\nСоздал файл.\n>"
	rep, err := ParseTerminal(text, TermConfig{
		ReportKeywords:      []string{"готово", "отчёт"},
		TerminalMarkerRegex: regexp.MustCompile(`^\s*◆\x{FE0E}?\s`),
		InputTopRegex:       regexp.MustCompile(`^>`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Found {
		t.Fatal("маркер не найден")
	}
	want := "Готово, отчёт в step1.md\nСоздал файл."
	if rep.Report != want {
		t.Fatalf("отчёт = %q, хочу %q", rep.Report, want)
	}
	if rep.Source != SourceTerminal {
		t.Fatalf("source = %q", rep.Source)
	}
}

// TestTerminalNoMarker — терминал без маркера.
func TestTerminalNoMarker(t *testing.T) {
	text := "> шаг 1\nпросто текст\n>"
	rep, err := ParseTerminal(text, TermConfig{
		ReportKeywords:      []string{"готово"},
		TerminalMarkerRegex: regexp.MustCompile(`^\s*◆\x{FE0E}?\s`),
		InputTopRegex:       regexp.MustCompile(`^>`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Found {
		t.Fatal("маркер найден, а быть не должен")
	}
}

// TestRecordingAskMulti — ask_user_question с несколькими вопросами.
func TestRecordingAskMulti(t *testing.T) {
	lines := []string{
		`{"type":"user","uuid":"u1","message":{"role":"user","parts":[{"text":"задача"}]}}`,
		`{"type":"assistant","uuid":"a1","message":{"role":"model","parts":[{"functionCall":{"id":"q1","name":"ask_user_question","args":{"questions":[{"question":"Первый?","options":[{"label":"A"}]},{"question":"Второй?","options":[{"label":"B"},{"label":"C"}],"multiSelect":true}]}}}]}}`,
	}
	rep, err := ParseRecording(writeJSONL(t, lines), "/proj", cfg())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Question == nil || len(rep.Question.Questions) != 2 {
		t.Fatalf("вопросов = %d, хочу 2: %+v", len(rep.Question.Questions), rep.Question)
	}
	if !rep.Question.Questions[1].MultiSelect {
		t.Fatal("второй вопрос multiSelect")
	}
	if len(rep.Question.Questions[1].Options) != 2 {
		t.Fatalf("вариантов второго = %d", len(rep.Question.Questions[1].Options))
	}
}

// TestRecordingCompressionMidTurn — сжатие (system/chat_compression) посреди
// хода не ломает разбор: маркер и отчёт извлекаются корректно.
func TestRecordingCompressionMidTurn(t *testing.T) {
	lines := []string{
		`{"type":"user","uuid":"u1","message":{"role":"user","parts":[{"text":"шаг 1"}]}}`,
		`{"type":"assistant","uuid":"a1","message":{"role":"model","parts":[{"text":"Делаю."}]}}`,
		`{"type":"system","subtype":"chat_compression","uuid":"s1","systemPayload":{"compressed":true}}`,
		`{"type":"assistant","uuid":"a2","message":{"role":"model","parts":[{"text":"Готово, отчёт в step1.md"}]}}`,
	}
	rep, err := ParseRecording(writeJSONL(t, lines), "/proj", cfg())
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Found {
		t.Fatal("маркер не найден после сжатия")
	}
	if rep.Report != "Готово, отчёт в step1.md" {
		t.Fatalf("отчёт = %q", rep.Report)
	}
}
