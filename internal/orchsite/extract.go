package orchsite

import (
	"encoding/json"
	"fmt"

	"github.com/go-rod/rod"
)

// InjectJS — определяет функции window.__rp_* на странице.
func (p *Profile) InjectJS(page *rod.Page) error {
	if len(p.js) == 0 {
		return nil
	}
	_, err := page.Eval(string(p.js))
	return err
}

// UserMessages — количество пользовательских сообщений в чате.
func (p *Profile) UserMessages(page *rod.Page) (int, error) {
	res, err := page.Eval(`window.__rp_userMessages()`)
	if err != nil {
		return 0, fmt.Errorf("orchsite: userMessages: %w", err)
	}
	var n int
	if err := json.Unmarshal([]byte(res.Value.Str()), &n); err != nil {
		return 0, fmt.Errorf("orchsite: userMessages: %w", err)
	}
	return n, nil
}

// LastAssistantAfter — индекс последнего сообщения оркестратора после
// userIndex (или -1).
func (p *Profile) LastAssistantAfter(page *rod.Page, userIndex int) (int, error) {
	res, err := page.Eval(`window.__rp_lastAssistantAfter(%d)`, userIndex)
	if err != nil {
		return -1, fmt.Errorf("orchsite: lastAssistantAfter: %w", err)
	}
	var n int
	if err := json.Unmarshal([]byte(res.Value.Str()), &n); err != nil {
		return -1, fmt.Errorf("orchsite: lastAssistantAfter: %w", err)
	}
	return n, nil
}

// Blocks — блоки сообщения по индексу: [{kind, text}].
func (p *Profile) Blocks(page *rod.Page, messageIndex int) ([]Block, error) {
	res, err := page.Eval(`window.__rp_blocks(%d)`, messageIndex)
	if err != nil {
		return nil, fmt.Errorf("orchsite: blocks: %w", err)
	}
	var out []Block
	raw := res.Value.Str()
	if raw == "null" || raw == "" {
		return nil, nil
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("orchsite: blocks: %w", err)
	}
	return out, nil
}
