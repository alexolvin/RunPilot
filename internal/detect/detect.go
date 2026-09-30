// Package detect — детектор состояния панелей (раздел 7 ТЗ).
//
// Снимок панели — результат `tmux capture-pane -p -J` (видимый экран,
// перенесённые строки склеены). Детектор нормализует снимок, классифицирует
// его (WAIT_UI → BUSY → PROMPT → IDLE → UNKNOWN) и извлекает текст ввода.
// Все регулярные выражения и пороги (число строк окна, ширина табуляции)
// приходят из профиля — числовых литералов в этом пакете нет
// (scripts/check-literals).
package detect

import (
	"hash/fnv"
	"regexp"
	"strings"
)

// State — состояние управляемой панели.
type State int

// Состояния панели; порядок не значим — порядок классификации задан ТЗ.
const (
	Unknown State = iota
	Idle
	Busy
	Prompt
	WaitUI
)

// String — человекочитаемое имя (логирование, отладка, TUI).
func (s State) String() string {
	switch s {
	case Idle:
		return "IDLE"
	case Busy:
		return "BUSY"
	case Prompt:
		return "PROMPT"
	case WaitUI:
		return "WAIT_UI"
	default:
		return "UNKNOWN"
	}
}

// Regexps — скомпилированные правила одного профиля.
type Regexps struct {
	Wait        *regexp.Regexp
	Busy        *regexp.Regexp
	Approval    *regexp.Regexp
	Idle        *regexp.Regexp
	Placeholder *regexp.Regexp
	InputTop    *regexp.Regexp
	InputBottom *regexp.Regexp
	InputStrip  *regexp.Regexp
	Volatile    []*regexp.Regexp
	// ClassifyLines — размер окна последних непустых строк для классификации.
	ClassifyLines int
	// TabWidth — число пробелов, на которые раскладывается таб.
	TabWidth int
}

// Normalize — нормализация снимка по разделу 7 ТЗ:
// хвостовые пробелы удаляются, затем удаляются строки, совпавшие с
// Volatile, затем табы раскладываются в TabWidth пробелов. NFC не применяется.
func Normalize(raw string, r Regexps) string {
	lines := strings.Split(strings.TrimRight(raw, "\n"), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimRight(line, " \t\r")
		if isVolatile(line, r.Volatile) {
			continue
		}
		if strings.ContainsRune(line, '\t') {
			line = expandTabs(line, r.TabWidth)
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// isVolatile — строка совпала с одним из правил Volatile (пустой список —
// ничего не удаляется).
func isVolatile(line string, volatile []*regexp.Regexp) bool {
	for _, re := range volatile {
		if re.MatchString(line) {
			return true
		}
	}
	return false
}

// expandTabs раскладывает табы в TabWidth пробелов.
func expandTabs(s string, width int) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, ch := range s {
		if ch == '\t' {
			for i := 0; i < width; i++ {
				b.WriteByte(' ')
			}
			continue
		}
		b.WriteRune(ch)
	}
	return b.String()
}

// nonEmpty — непустые строки (после нормализации строка из одних пробелов
// уже пуста).
func nonEmpty(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

// Classify — классификация нормализованного снимка: по последним
// ClassifyLines непустым строкам, первое совпадение в порядке
// wait → busy → approval → idle, иначе Unknown (раздел 7 ТЗ).
func Classify(normalized string, r Regexps) State {
	lines := nonEmpty(strings.Split(normalized, "\n"))
	if len(lines) > r.ClassifyLines {
		lines = lines[len(lines)-r.ClassifyLines:]
	}
	for _, l := range lines {
		if r.Wait != nil && r.Wait.MatchString(l) {
			return WaitUI
		}
	}
	for _, l := range lines {
		if r.Busy != nil && r.Busy.MatchString(l) {
			return Busy
		}
	}
	for _, l := range lines {
		if r.Approval != nil && r.Approval.MatchString(l) {
			return Prompt
		}
	}
	for _, l := range lines {
		if r.Idle != nil && r.Idle.MatchString(l) {
			return Idle
		}
	}
	return Unknown
}

// ExtractInput — текст ввода из нормализованного снимка: строки между
// последними совпадениями InputTop (включая её) и InputBottom (не включая);
// из каждой строки удаляется InputStrip, склейка через \n. Если результат
// совпал с Placeholder — ввод пуст.
func ExtractInput(normalized string, r Regexps) string {
	if r.InputTop == nil || r.InputBottom == nil {
		return ""
	}
	lines := strings.Split(normalized, "\n")
	top, bottom := -1, -1
	for i, l := range lines {
		if r.InputTop.MatchString(l) {
			top = i
		}
		if r.InputBottom.MatchString(l) {
			bottom = i
		}
	}
	if top < 0 || bottom <= top {
		return ""
	}
	var parts []string
	for _, l := range lines[top:bottom] {
		if r.InputStrip != nil {
			l = r.InputStrip.ReplaceAllString(l, "")
		}
		parts = append(parts, l)
	}
	text := strings.Join(parts, "\n")
	if r.Placeholder != nil && r.Placeholder.MatchString(text) {
		return ""
	}
	return text
}

// Hash — FNV-1a 64 нормализованного текста (раздел 7 ТЗ).
func Hash(normalized string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(normalized))
	return h.Sum64()
}
