package relay

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/go-rod/rod"

	"runpilot/internal/orchsite"
)

// hold-коды ожидания/извлечения (ТЗ Relay §11).
const (
	HoldAnswerTimeout = "ANSWER_TIMEOUT"
	HoldNoAgentBlock  = "NO_AGENT_BLOCK"
	HoldRepeatAnswer  = "REPEAT_ANSWER"
)

// StopDone — ответ завершён стоп-словом.
const StopDone = "DONE"

// WaitResult — результат ожидания и извлечения.
type WaitResult struct {
	AgentBlock string // извлечённый блок для агента
	Stop       bool   // стоп-слово
	AnswerHash string // хеш блока
	Hold       string // hold-код ("" = ок)
}

// WaitAnswer — ожидание завершения ответа (ТЗ Relay §11.1–3).
// Возвращает индекс сообщения оркестратора.
func (r *Relay) WaitAnswer(ctx context.Context, page *rod.Page, p *orchsite.Profile, userIndex int) (int, string, error) {
	deadline := r.clk.Now().Add(time.Duration(r.cfg.AnswerTimeoutMin) * time.Minute)
	stableSince := time.Time{}
	var lastHash string
	for {
		if r.clk.Now().After(deadline) {
			return 0, HoldAnswerTimeout, nil
		}
		// Идёт ли генерация (короткий таймаут: нет элемента → false).
		_, genErr := page.Timeout(pollInterval).Element(p.GeneratingSelector)
		genActive := genErr == nil
		// Индекс последнего ответа после нашего сообщения.
		idx, err := p.LastAssistantAfter(page, userIndex)
		if err != nil || idx < 0 {
			if err := sleepCtx(ctx, pollInterval); err != nil {
				return 0, "", err
			}
			continue
		}
		if genActive {
			stableSince = time.Time{}
			lastHash = ""
			if err := sleepCtx(ctx, pollInterval); err != nil {
				return 0, "", err
			}
			continue
		}
		// Хеш innerText ответа.
		text, err := r.assistantText(page, p, idx)
		if err != nil {
			if err := sleepCtx(ctx, pollInterval); err != nil {
				return 0, "", err
			}
			continue
		}
		h := textHash(text)
		if h != lastHash {
			lastHash = h
			stableSince = r.clk.Now()
			if err := sleepCtx(ctx, pollInterval); err != nil {
				return 0, "", err
			}
			continue
		}
		// Стабильность answer_stable_sec.
		if r.clk.Now().Sub(stableSince) >= time.Duration(r.cfg.AnswerStableSec)*time.Second {
			return idx, "", nil
		}
		if err := sleepCtx(ctx, pollInterval); err != nil {
			return 0, "", err
		}
	}
}

// Extract — извлечение блока для агента (ТЗ Relay §11.4–7).
func (r *Relay) Extract(page *rod.Page, p *orchsite.Profile, answerIdx int, prevAnswerHash string) (WaitResult, error) {
	blocks, err := p.Blocks(page, answerIdx)
	if err != nil {
		return WaitResult{Hold: HoldNoAgentBlock}, nil
	}
	block := r.findAgentBlock(blocks)
	if block == "" {
		// Кандидатов нет → LLM T-ANSWER (R4). Пока: NO_AGENT_BLOCK.
		return WaitResult{Hold: HoldNoAgentBlock}, nil
	}
	trimmed := strings.TrimSpace(block)
	if trimmed == r.cfg.StopWord {
		return WaitResult{AgentBlock: trimmed, Stop: true}, nil
	}
	h := textHash(trimmed)
	if prevAnswerHash != "" && h == prevAnswerHash {
		return WaitResult{Hold: HoldRepeatAnswer}, nil
	}
	return WaitResult{AgentBlock: trimmed, AnswerHash: h}, nil
}

// findAgentBlock — последний code-блок с предшествующим текстом под agent_block_regex.
func (r *Relay) findAgentBlock(blocks []orchsite.Block) string {
	re := regexp.MustCompile(r.cfg.AgentBlockRegex)
	var last string
	var lastIsAgent bool
	for i := 0; i < len(blocks); i++ {
		b := blocks[i]
		if b.Kind != "code" {
			lastIsAgent = re.MatchString(b.Text)
			continue
		}
		if lastIsAgent {
			last = b.Text
		}
	}
	return last
}

// assistantText — innerText сообщения оркестратора по индексу в полном
// списке .msg (индекс от LastAssistantAfter).
func (r *Relay) assistantText(page *rod.Page, p *orchsite.Profile, idx int) (string, error) {
	res, err := page.Eval(`(i) => { const els = document.querySelectorAll(` + jsq(p.AsstMessageSelector) + `); if (!els[i]) return ''; return els[i].innerText; }`, idx)
	if err != nil {
		return "", err
	}
	return res.Value.Str(), nil
}

// textHash — SHA-256 текста.
func textHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

var _ = fmt.Sprintf
