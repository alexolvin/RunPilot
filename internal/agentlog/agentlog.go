// Package agentlog — разбор записи Qwen Code и истории терминала (ТЗ Relay,
// раздел 6). Чистый парсер: не обращается к tmux, только читает файл записи и
// проверяет существование .md-файлов каталога сессии.
package agentlog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Source — источник отчёта.
const (
	SourceRecording = "recording"
	SourceTerminal  = "terminal"
)

// Config — параметры разбора (только из конфигурации, строгое правило 1).
type Config struct {
	ReportKeywords []string
	AttachMaxFiles int
}

// TermConfig — параметры терминального источника (раздел 6.3).
type TermConfig struct {
	ReportKeywords      []string
	TerminalMarkerRegex *regexp.Regexp
	InputTopRegex       *regexp.Regexp
}

// Report — разобранный последний ход.
type Report struct {
	Source   string     // "recording" | "terminal"
	Found    bool       // найден ли маркер отчёта
	Report   string     // текст отчёта (от маркера до конца хода)
	Files    []FileRef  // .md-файлы хода (путь + размер)
	Question *Question  // неотвеченный ask_user_question
}

// FileRef — .md-файл, созданный в ходе.
type FileRef struct {
	Path string
	Size int64
}

// Question — аргументы ask_user_question.
type Question struct {
	Questions []QuestionItem
}

// QuestionItem — один вопрос.
type QuestionItem struct {
	Question    string
	Options     []Option
	MultiSelect bool
}

// Option — вариант ответа.
type Option struct {
	Label       string
	Description string
}

// --- структура записи Qwen Code (jsonl) ---

type record struct {
	Type    string   `json:"type"`
	Subtype string   `json:"subtype"`
	Message *message `json:"message"`
}

type message struct {
	Role  string `json:"role"`
	Parts []part `json:"parts"`
}

type part struct {
	Text             string            `json:"text"`
	Thought          bool              `json:"thought"`
	FunctionCall     *functionCall     `json:"functionCall"`
	FunctionResponse *functionResponse `json:"functionResponse"`
}

type functionCall struct {
	ID   string         `json:"id"`
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

type functionResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ParseRecording — разбор последнего хода из записи Qwen Code (раздел 6.1).
// path — путь к <uuid>.jsonl; sessionDir — каталог сессии (для .md-файлов).
func ParseRecording(path string, sessionDir string, cfg Config) (*Report, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var records []record
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var r record
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			continue
		}
		records = append(records, r)
	}

	// Ход — записи после последней type: user без subtype (6.1.2).
	lastUser := -1
	for i, r := range records {
		if r.Type == "user" && r.Subtype == "" {
			lastUser = i
		}
	}
	rep := &Report{Source: SourceRecording}
	if lastUser < 0 || lastUser+1 >= len(records) {
		return rep, nil
	}
	turn := records[lastUser+1:]

	// Сообщения агента (6.1.3): текстовые части assistant, без thought.
	var messages []string
	for _, r := range turn {
		if r.Type != "assistant" || r.Message == nil {
			continue
		}
		var texts []string
		for _, p := range r.Message.Parts {
			if p.FunctionCall != nil || p.FunctionResponse != nil || p.Thought {
				continue
			}
			if p.Text != "" {
				texts = append(texts, p.Text)
			}
		}
		if len(texts) > 0 {
			messages = append(messages, strings.Join(texts, "\n"))
		}
	}

	// Маркер (6.1.4): снизу вверх, первая непустая строка содержит ключевое слово.
	marker := -1
	for i := len(messages) - 1; i >= 0; i-- {
		if hasKeyword(firstNonEmptyLine(messages[i]), cfg.ReportKeywords) {
			marker = i
			break
		}
	}
	if marker >= 0 {
		rep.Found = true
		rep.Report = strings.Join(messages[marker:], "\n\n")
	}

	// Файлы (6.1.7) и вопрос (6.1.8).
	rep.Files = collectFiles(turn, sessionDir, cfg.AttachMaxFiles)
	rep.Question = findQuestion(turn)
	return rep, nil
}

// collectFiles — вызовы write_file/edit в ходе: путь .md внутри sessionDir,
// существует. Дубликаты — по последнему изменению; лимит AttachMaxFiles.
func collectFiles(turn []record, sessionDir string, maxFiles int) []FileRef {
	root := filepath.Clean(sessionDir)
	lastIdx := map[string]int{}
	var order []string
	for i, r := range turn {
		if r.Type != "assistant" || r.Message == nil {
			continue
		}
		for _, p := range r.Message.Parts {
			fc := p.FunctionCall
			if fc == nil || (fc.Name != "write_file" && fc.Name != "edit") {
				continue
			}
			fp, _ := fc.Args["file_path"].(string)
			if fp == "" || !strings.EqualFold(filepath.Ext(fp), ".md") {
				continue
			}
			abs := fp
			if !filepath.IsAbs(abs) {
				abs = filepath.Join(root, abs)
			}
			abs = filepath.Clean(abs)
			rel, err := filepath.Rel(root, abs)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
				continue
			}
			if _, err := os.Stat(abs); err != nil {
				continue
			}
			if _, ok := lastIdx[abs]; !ok {
				order = append(order, abs)
			}
			lastIdx[abs] = i
		}
	}
	sort.SliceStable(order, func(i, j int) bool { return lastIdx[order[i]] < lastIdx[order[j]] })
	if maxFiles > 0 && len(order) > maxFiles {
		order = order[:maxFiles]
	}
	var files []FileRef
	for _, fp := range order {
		info, err := os.Stat(fp)
		if err != nil {
			continue
		}
		files = append(files, FileRef{Path: fp, Size: info.Size()})
	}
	return files
}

// findQuestion — неотвеченный ask_user_question в ходе (6.1.8).
func findQuestion(turn []record) *Question {
	answered := map[string]bool{}
	for _, r := range turn {
		if r.Message == nil {
			continue
		}
		for _, p := range r.Message.Parts {
			if fr := p.FunctionResponse; fr != nil && fr.Name == "ask_user_question" {
				answered[fr.ID] = true
			}
		}
	}
	for i := len(turn) - 1; i >= 0; i-- {
		r := turn[i]
		if r.Type != "assistant" || r.Message == nil {
			continue
		}
		for _, p := range r.Message.Parts {
			fc := p.FunctionCall
			if fc == nil || fc.Name != "ask_user_question" || answered[fc.ID] {
				continue
			}
			if q := parseQuestion(fc.Args); q != nil {
				return q
			}
		}
	}
	return nil
}

func parseQuestion(args map[string]any) *Question {
	qs, _ := args["questions"].([]any)
	if len(qs) == 0 {
		return nil
	}
	var q Question
	for _, qa := range qs {
		qm, ok := qa.(map[string]any)
		if !ok {
			continue
		}
		item := QuestionItem{}
		item.Question, _ = qm["question"].(string)
		item.MultiSelect, _ = qm["multiSelect"].(bool)
		if opts, ok := qm["options"].([]any); ok {
			for _, oa := range opts {
				om, ok := oa.(map[string]any)
				if !ok {
					continue
				}
				opt := Option{}
				opt.Label, _ = om["label"].(string)
				opt.Description, _ = om["description"].(string)
				item.Options = append(item.Options, opt)
			}
		}
		q.Questions = append(q.Questions, item)
	}
	if len(q.Questions) == 0 {
		return nil
	}
	return &q
}

// firstNonEmptyLine — первая непустая строка текста.
func firstNonEmptyLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			return l
		}
	}
	return ""
}

// hasKeyword — содержит ли строка слово из keywords (без учёта регистра, подстрока).
func hasKeyword(line string, keywords []string) bool {
	l := strings.ToLower(line)
	for _, k := range keywords {
		if k == "" {
			continue
		}
		if strings.Contains(l, strings.ToLower(k)) {
			return true
		}
	}
	return false
}
