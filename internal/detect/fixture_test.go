package detect_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"runpilot/internal/detect"
	"runpilot/profiles"
)

// requiredStates — обязательный набор раздела 7 ТЗ (минимум 2 экрана на состояние).
var requiredStates = []string{
	"idle_empty", "idle_input_single", "idle_input_multiline",
	"busy_thinking", "busy_tool", "busy_tool_looks_like_idle",
	"approval_prompt", "approval_after_scroll",
	"wait_compact", "wait_connecting", "wait_rate_limit",
	"after_api_error", "after_success",
}

// expectedState — ожидаемое состояние для каждого состояния фикстур.
//
// wait_rate_limit — задокументированное отклонение: на реальных экранах
// Qwen Code 0.24.4 строки ретрая («Retrying in Ns… (attempt X/10)») видны
// только в истории терминала, а видимый экран byte-в-byte равен обычному
// busy. Зафиксировано в docs/evidence/Э1/CONTROL.md; для диспетчеризации
// безопасно: BUSY, как и WAIT_UI, не даёт dispatch submit.
var expectedState = map[string]detect.State{
	"idle_empty":                detect.Idle,
	"idle_input_single":         detect.Idle,
	"idle_input_multiline":      detect.Idle,
	"busy_thinking":             detect.Busy,
	"busy_tool":                 detect.Busy,
	"busy_tool_looks_like_idle": detect.Busy,
	"approval_prompt":           detect.Prompt,
	"approval_after_scroll":     detect.Prompt,
	// W5 (раздел 13.1 ТЗ): вставка задания и литералы compress_text/quit_text
	// 0.24.5 — панель остаётся IDLE, текст виден в вводе (подтверждает значения).
	"idle_input_pasted_short":   detect.Idle,
	"idle_input_pasted_long":    detect.Idle,
	"compress":                  detect.Idle,
	"quit":                      detect.Idle,
	"wait_compact":              detect.WaitUI,
	"wait_connecting":           detect.WaitUI,
	"wait_rate_limit":           detect.Busy,
	"after_api_error":           detect.Idle,
	"after_success":             detect.Idle,
	// plan_mode — бонус-состояние: план-режим в таблице состояний раздела 7
	// ТЗ относится к WAIT_UI (видимый маркер — строка режима).
	"plan_mode":                 detect.WaitUI,
	"mockcoder_idle":            detect.Idle,
	"mockcoder_busy":            detect.Busy,
	"mockcoder_approval":        detect.Prompt,
	"mockcoder_wait_connecting": detect.WaitUI,
	"mockcoder_wait_compact":    detect.WaitUI,
	"mockcoder_wait_rate_limit": detect.WaitUI,
	"mockcoder_after_error":     detect.Idle,
}

// expectedInput — побайтовый текст ввода (раздел 7 ТЗ: «текст ввода
// совпадает побайто на всех idle_input_*»).
var expectedInput = map[string]string{
	"idle_input_single-01":    "Однострочный тестовый промпт",
	"idle_input_single-02":    "Однострочный тестовый промпт и ещё кусок",
	"idle_input_multiline-01": "Первая строка\nВторая строка",
	"idle_input_multiline-02": "Line A\nLine B",
	"idle_empty-01":           "",
	"idle_empty-02":           "",
	// W5 (0.24.5): вставленный текст побайто + литералы compress_text/quit_text.
	"idle_input_pasted_short-01": "Короткая тестовая вставка",
	"idle_input_pasted_long-01":  "[Pasted Content 8000 chars]",
	"compress-01":                "/compact",
	"quit-01":                    "/quit",
}

const fixturesRoot = "../../testdata/fixtures"

type fixture struct {
	state string
	path  string
	raw   string
}

// loadFixtures — все фикстуры testdata/fixtures/qwen/<version>/ и
// testdata/fixtures/mockcoder/ в формате <state>-NN.txt.
func loadFixtures(t *testing.T) []fixture {
	t.Helper()
	var out []fixture

	qwenDir := filepath.Join(fixturesRoot, "qwen")
	versions, err := os.ReadDir(qwenDir)
	if err != nil {
		t.Fatalf("каталог фикстур qwen: %v", err)
	}
	for _, v := range versions {
		if !v.IsDir() {
			continue
		}
		out = append(out, readFixtureDir(t, filepath.Join(qwenDir, v.Name()))...)
	}
	out = append(out, readFixtureDir(t, filepath.Join(fixturesRoot, "mockcoder"))...)
	if len(out) == 0 {
		t.Fatalf("фикстуры не найдены в %s", fixturesRoot)
	}
	return out
}

func readFixtureDir(t *testing.T, dir string) []fixture {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("чтение %s: %v", dir, err)
	}
	var out []fixture
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".txt") {
			continue
		}
		base := strings.TrimSuffix(e.Name(), ".txt")
		i := strings.LastIndex(base, "-")
		if i < 0 {
			t.Fatalf("%s: имя не в формате <state>-NN.txt", e.Name())
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("чтение %s: %v", e.Name(), err)
		}
		out = append(out, fixture{
			state: base[:i],
			path:  filepath.Join(dir, e.Name()),
			raw:   string(raw),
		})
	}
	return out
}

// detector — встроенный профиль qwen + скомпилированные правила.
func detector(t *testing.T) detect.Regexps {
	t.Helper()
	p, err := profiles.LoadEmbeddedQwen()
	if err != nil {
		t.Fatalf("встроенный профиль: %v", err)
	}
	r, err := p.Regexps()
	if err != nil {
		t.Fatalf("regexps профиля: %v", err)
	}
	return r
}

// TestFixturesClassify100 — классификатор обязан давать ожидаемое
// состояние на 100% фикстур (раздел 7 ТЗ).
func TestFixturesClassify100(t *testing.T) {
	r := detector(t)
	for _, f := range loadFixtures(t) {
		want, ok := expectedState[f.state]
		if !ok {
			t.Errorf("%s: состояние %q не описано в expectedState", f.path, f.state)
			continue
		}
		norm := detect.Normalize(f.raw, r)
		if got := detect.Classify(norm, r); got != want {
			t.Errorf("%s: Classify = %v, want %v\n--- нормализованный снимок:\n%s", f.path, got, want, norm)
		}
	}
}

// TestFixtureInputByteExact — текст ввода совпадает побайто на всех
// idle_input_*; плейсхолдер даёт пустой ввод (раздел 7 ТЗ).
func TestFixtureInputByteExact(t *testing.T) {
	r := detector(t)
	seen := map[string]bool{}
	for _, f := range loadFixtures(t) {
		name := strings.TrimSuffix(filepath.Base(f.path), ".txt")
		want, ok := expectedInput[name]
		if !ok {
			continue
		}
		seen[name] = true
		norm := detect.Normalize(f.raw, r)
		if got := detect.ExtractInput(norm, r); got != want {
			t.Errorf("%s: ExtractInput = %q, want %q (побайтно)", f.path, got, want)
		}
	}
	for name := range expectedInput {
		if !seen[name] {
			t.Errorf("фикстура %s не найдена", name)
		}
	}
}

// TestNegativeFixturesNotIdle — отрицательные фикстуры не имеют права
// стать IDLE (раздел 7 ТЗ).
func TestNegativeFixturesNotIdle(t *testing.T) {
	r := detector(t)
	found := 0
	for _, f := range loadFixtures(t) {
		if f.state != "busy_tool_looks_like_idle" && f.state != "approval_after_scroll" {
			continue
		}
		found++
		if got := detect.Classify(detect.Normalize(f.raw, r), r); got == detect.Idle {
			t.Errorf("%s: отрицательная фикстура классифицирована как IDLE", f.path)
		}
	}
	if found < 2 {
		t.Fatalf("отрицательные фикстуры не найдены (найдено %d)", found)
	}
}

// TestRequiredSetComplete — обязательный набор раздела 7: минимум 2 экрана
// на каждое из 13 состояний.
func TestRequiredSetComplete(t *testing.T) {
	count := map[string]int{}
	for _, f := range loadFixtures(t) {
		count[f.state]++
	}
	for _, s := range requiredStates {
		if count[s] < 2 {
			t.Errorf("состояние %s: экранов %d, нужно минимум 2", s, count[s])
		}
	}
}

// TestNoClaudeFixtures — каталога testdata/fixtures/claude быть не должно
// (разделы 7 и 15 ТЗ).
func TestNoClaudeFixtures(t *testing.T) {
	if _, err := os.Stat(filepath.Join(fixturesRoot, "claude")); !os.IsNotExist(err) {
		t.Fatalf("каталог testdata/fixtures/claude существует — он запрещён ТЗ")
	}
}
