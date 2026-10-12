// Package orchsite — профили сайтов оркестратора (ТЗ Relay §9).
// Профиль задаёт URL-паттерн чата, селекторы DOM и JS-экстракторы.
package orchsite

import (
	"fmt"
	"regexp"

	"gopkg.in/yaml.v3"

	"runpilot/orchestrators"
)

// Profile — разобранный профиль сайта оркестратора.
type Profile struct {
	Name                 string `yaml:"name"`
	URLRegex             string `yaml:"url_regex"`
	LoginURLRegex        string `yaml:"login_url_regex"`
	LoggedOutSelector    string `yaml:"logged_out_selector"`
	LoggedInSelector     string `yaml:"logged_in_selector"`
	ComposerSelector     string `yaml:"composer_selector"`
	SendButtonSelector   string `yaml:"send_button_selector"`
	FileInputSelector    string `yaml:"file_input_selector"`
	AttachmentChipSel    string `yaml:"attachment_chip_selector"`
	UploadingSelector    string `yaml:"uploading_selector"`
	GeneratingSelector   string `yaml:"generating_selector"`
	UserMessageSelector  string `yaml:"user_message_selector"`
	AsstMessageSelector  string `yaml:"assistant_message_selector"`
	CodeBlockSelector    string `yaml:"code_block_selector"`
	TextBlockSelector    string `yaml:"text_block_selector"`
	RateLimitRegex       string `yaml:"rate_limit_regex"`
	ErrorRegex           string `yaml:"error_regex"`
	Normalize            string `yaml:"normalize"`

	// Скомпилированные паттерны (заполняются в Load).
	urlRE        *regexp.Regexp
	loginURLRE   *regexp.Regexp
	rateLimitRE  *regexp.Regexp
	errorRE      *regexp.Regexp

	// JS-экстракторы профиля.
	js []byte
}

// Block — один блок сообщения (ТЗ Relay §11): text или code.
type Block struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

// Names — имена доступных профилей (встроенные + переопределённые).
func Names() []string {
	return orchestrators.Names()
}

// Load — загружает профиль по имени (embed, затем override из ~/.config).
func Load(name string) (*Profile, error) {
	var yml, js []byte
	var err error
	if yml, err = orchestrators.Override(name, "yaml"); err != nil {
		yml, err = orchestrators.YAML(name)
		if err != nil {
			return nil, fmt.Errorf("orchsite: профиль %q: %w", name, err)
		}
	}
	if js, err = orchestrators.Override(name, "js"); err != nil {
		js, err = orchestrators.JS(name)
		if err != nil {
			return nil, fmt.Errorf("orchsite: профиль %q js: %w", name, err)
		}
	}
	var p Profile
	if err := yaml.Unmarshal(yml, &p); err != nil {
		return nil, fmt.Errorf("orchsite: профиль %q yaml: %w", name, err)
	}
	if p.Name == "" {
		p.Name = name
	}
	p.js = js

	if p.URLRegex != "" {
		if p.urlRE, err = regexp.Compile(p.URLRegex); err != nil {
			return nil, fmt.Errorf("orchsite: %q url_regex: %w", name, err)
		}
	}
	if p.LoginURLRegex != "" {
		if p.loginURLRE, err = regexp.Compile(p.LoginURLRegex); err != nil {
			return nil, fmt.Errorf("orchsite: %q login_url_regex: %w", name, err)
		}
	}
	if p.RateLimitRegex != "" {
		if p.rateLimitRE, err = regexp.Compile(p.RateLimitRegex); err != nil {
			return nil, fmt.Errorf("orchsite: %q rate_limit_regex: %w", name, err)
		}
	}
	if p.ErrorRegex != "" {
		if p.errorRE, err = regexp.Compile(p.ErrorRegex); err != nil {
			return nil, fmt.Errorf("orchsite: %q error_regex: %w", name, err)
		}
	}
	return &p, nil
}

// MatchURL — URL принадлежит профилю (url_regex).
func (p *Profile) MatchURL(url string) bool {
	return p.urlRE != nil && p.urlRE.MatchString(url)
}

// ChatKey — ключ чата (именная группа `chat` в url_regex). ok=false, если URL
// не совпадает или группы нет.
func (p *Profile) ChatKey(url string) (string, bool) {
	if p.urlRE == nil {
		return "", false
	}
	m := p.urlRE.FindStringSubmatch(url)
	if m == nil {
		return "", false
	}
	for i, g := range p.urlRE.SubexpNames() {
		if g == "chat" {
			return m[i], true
		}
	}
	return "", false
}

// OnLoginURL — URL это страница входа (login_url_regex, только path).
func (p *Profile) OnLoginURL(path string) bool {
	return p.loginURLRE != nil && p.loginURLRE.MatchString(path)
}

// RateLimit — текст соответствует лимиту запросов.
func (p *Profile) RateLimit(text string) bool {
	return p.rateLimitRE != nil && p.rateLimitRE.MatchString(text)
}

// Error — текст соответствует ошибке оркестратора.
func (p *Profile) Error(text string) bool {
	return p.errorRE != nil && p.errorRE.MatchString(text)
}
