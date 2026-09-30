// Package profiles — профили агентов (раздел 7 ТЗ).
//
// profiles/qwen.yaml встроен в бинарник (go:embed) и переопределяется
// файлом ~/.config/runpilot/profiles/qwen.yaml (оператор может подогнать
// regex под новую версию UI без пересборки). Разбор строгий: неизвестный
// ключ — ошибка. Профилей claude и openclaw в этой редакции нет.
package profiles

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"runpilot/internal/detect"

	"gopkg.in/yaml.v3"
)

// ErrUnknownField — в YAML есть ключ, которого нет в схеме.
var ErrUnknownField = errors.New("profiles: unknown field")

// Qwen — профиль агента qwen (раздел 7 ТЗ).
type Qwen struct {
	Name          string            `yaml:"name"`
	Command       string            `yaml:"command"`
	VersionCmd    string            `yaml:"version_cmd"`
	SubmitKeys    string            `yaml:"submit_keys"`
	CancelKeys    string            `yaml:"cancel_keys"`
	ResumeText    string            `yaml:"resume_text"`
	CmdlineRegex  string            `yaml:"cmdline_regex"`
	ClassifyLines int               `yaml:"classify_lines"`
	TabWidth      int               `yaml:"tab_width"`
	BusyRegex     string            `yaml:"busy_regex"`
	ApprovalRegex string            `yaml:"approval_regex"`
	WaitRegex     string            `yaml:"wait_regex"`
	IdleRegex     string            `yaml:"idle_regex"`
	PlaceholderRe string            `yaml:"placeholder_regex"`
	InputTopRe    string            `yaml:"input_top_regex"`
	InputBottomRe string            `yaml:"input_bottom_regex"`
	InputStripRe  string            `yaml:"input_strip_regex"`
	VolatileRe    string            `yaml:"volatile_regex"`
	Env           map[string]string `yaml:"env"`

	// Ввод и разрешения (разделы 13.1/13.3 ТЗ, v2): фиксируются
	// фикстурами 0.24.5 (этап W5).
	ApprovalOptions       []ApprovalOption `yaml:"approval_options"`
	CompressText          string           `yaml:"compress_text"`
	QuitText              string           `yaml:"quit_text"`
	PastePlaceholderRegex string           `yaml:"paste_placeholder_regex"`

	cmdline *regexp.Regexp
}

// ApprovalOption — вариант ответа на запрос подтверждения (13.3): id — имя
// в /approve, label — подпись кнопки, keys — клавиши (allowlist узла).
type ApprovalOption struct {
	ID    string   `yaml:"id" json:"id"`
	Label string   `yaml:"label" json:"label"`
	Keys  []string `yaml:"keys" json:"keys"`
}

//go:embed qwen.yaml
var embeddedQwenYAML []byte

// OverridePath — путь к пользовательскому переопределению профиля
// (~/.config/runpilot/profiles/qwen.yaml).
func OverridePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "runpilot", "profiles", "qwen.yaml"), nil
}

// LoadQwen — профиль qwen: пользовательское переопределение, если оно
// есть, иначе встроенный. Строгий разбор, валидные regex, sanity-проверки.
func LoadQwen() (*Qwen, error) {
	p, err := OverridePath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("profiles: чтение переопределения: %w", err)
		}
		data = embeddedQwenYAML
	}
	return parseQwen(data)
}

// LoadEmbeddedQwen — только встроенный профиль (детерминированные тесты).
func LoadEmbeddedQwen() (*Qwen, error) {
	return parseQwen(embeddedQwenYAML)
}

func parseQwen(data []byte) (*Qwen, error) {
	var p Qwen
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&p); err != nil {
		var terr *yaml.TypeError
		if errors.As(err, &terr) {
			for _, msg := range terr.Errors {
				if strings.Contains(msg, "not found in type") {
					return nil, fmt.Errorf("%w: %s", ErrUnknownField, strings.Join(terr.Errors, "; "))
				}
			}
			return nil, fmt.Errorf("profiles: некорректный YAML: %s", strings.Join(terr.Errors, "; "))
		}
		return nil, fmt.Errorf("profiles: некорректный YAML: %v", err)
	}
	if err := p.compile(); err != nil {
		return nil, err
	}
	return &p, nil
}

// compile проверяет обязательные поля и компилирует все regex.
func (p *Qwen) compile() error {
	var problems []string
	if p.Name != "qwen" {
		problems = append(problems, "name должен быть qwen")
	}
	if p.ClassifyLines <= 0 {
		problems = append(problems, "classify_lines должен быть > 0")
	}
	if p.TabWidth <= 0 {
		problems = append(problems, "tab_width должен быть > 0")
	}
	fields := map[string]string{
		"cmdline_regex":      p.CmdlineRegex,
		"busy_regex":         p.BusyRegex,
		"approval_regex":     p.ApprovalRegex,
		"wait_regex":         p.WaitRegex,
		"idle_regex":         p.IdleRegex,
		"placeholder_regex":  p.PlaceholderRe,
		"input_top_regex":    p.InputTopRe,
		"input_bottom_regex": p.InputBottomRe,
		"input_strip_regex":  p.InputStripRe,
		"volatile_regex":     p.VolatileRe,
		"paste_placeholder_regex": p.PastePlaceholderRegex,
	}
	for name, pattern := range fields {
		if pattern == "" {
			continue
		}
		if _, err := regexp.Compile(pattern); err != nil {
			problems = append(problems, name+": "+err.Error())
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("profiles: %s", strings.Join(problems, "; "))
	}
	var err error
	if p.CmdlineRegex != "" {
		if p.cmdline, err = regexp.Compile(p.CmdlineRegex); err != nil {
			return fmt.Errorf("profiles: cmdline_regex: %w", err)
		}
	}
	return nil
}

// Cmdline — скомпилированный cmdline_regex (сопоставление с процессом панели).
func (p *Qwen) Cmdline() *regexp.Regexp {
	return p.cmdline
}

// Regexps — правила детектора из профиля (пустые поля — nil;
// пустой volatile_regex — пустой список).
func (p *Qwen) Regexps() (detect.Regexps, error) {
	var r detect.Regexps
	set := func(name, pattern string, target **regexp.Regexp) error {
		if pattern == "" {
			return nil
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return fmt.Errorf("profiles: %s: %w", name, err)
		}
		*target = re
		return nil
	}
	for _, s := range []struct {
		name    string
		pattern string
		target  **regexp.Regexp
	}{
		{"wait_regex", p.WaitRegex, &r.Wait},
		{"busy_regex", p.BusyRegex, &r.Busy},
		{"approval_regex", p.ApprovalRegex, &r.Approval},
		{"idle_regex", p.IdleRegex, &r.Idle},
		{"placeholder_regex", p.PlaceholderRe, &r.Placeholder},
		{"input_top_regex", p.InputTopRe, &r.InputTop},
		{"input_bottom_regex", p.InputBottomRe, &r.InputBottom},
		{"input_strip_regex", p.InputStripRe, &r.InputStrip},
	} {
		if err := set(s.name, s.pattern, s.target); err != nil {
			return r, err
		}
	}
	if p.VolatileRe != "" {
		re, err := regexp.Compile(p.VolatileRe)
		if err != nil {
			return r, fmt.Errorf("profiles: volatile_regex: %w", err)
		}
		r.Volatile = append(r.Volatile, re)
	}
	r.ClassifyLines = p.ClassifyLines
	r.TabWidth = p.TabWidth
	return r, nil
}
