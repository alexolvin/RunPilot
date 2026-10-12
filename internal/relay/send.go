package relay

import (
	"context"
	"fmt"
	"time"

	"github.com/go-rod/rod"

	"runpilot/internal/orchsite"
)

// hold-коды отправки (ТЗ Relay §10).
const (
	HoldWrongChat       = "WRONG_CHAT"
	HoldNeedsLogin      = "NEEDS_LOGIN"
	HoldRateLimit       = "ORCH_RATE_LIMIT"
	HoldOrchError       = "ORCH_ERROR"
	HoldAttachFailed    = "ATTACH_FAILED"
	HoldComposerMismatch = "COMPOSER_MISMATCH"
	HoldSendUnconfirmed  = "SEND_UNCONFIRMED"
)

// Attachment — .md-вложение для отправки.
type Attachment struct {
	Name string
	Path string
}

// SendResult — результат отправки (шаги 1–9).
type SendResult struct {
	SentHash string
	Hold     string
}

// Send — отправка сообщения + вложений (ТЗ Relay §10, шаги 1–9).
// Возвращает SentHash (нормализованный хеш) или Hold-код.
func (r *Relay) Send(ctx context.Context, page *rod.Page, profile *orchsite.Profile,
	text string, files []Attachment) (SendResult, error) {

	// Шаг 1: URL совпадает с url_regex.
	if !profile.MatchURL(page.MustInfo().URL) {
		return SendResult{Hold: HoldWrongChat}, nil
	}

	// Шаг 2: проверка входа.
	if err := r.checkLogin(ctx, page, profile); err != nil {
		return SendResult{Hold: HoldNeedsLogin}, nil
	}

	// Шаг 3: генерация на странице → ждать её конца.
	if err := r.waitGenerationDone(ctx, page, profile); err != nil {
		return SendResult{Hold: HoldOrchError}, err
	}

	// Шаг 4: тексты лимита и ошибки.
	if hold := r.checkRateError(page, profile); hold != "" {
		return SendResult{Hold: hold}, nil
	}

	// Шаг 5: пауза между отправками.
	if err := r.waitMinSendInterval(ctx); err != nil {
		return SendResult{}, err
	}

	// Шаг 6: запись SENDING + sent_hash (до действия в странице).
	sentHash := normHash(text)
	if err := r.onSending(page, sentHash); err != nil {
		return SendResult{}, err
	}

	// Шаг 7: вложения SetFiles.
	if len(files) > 0 {
		if err := r.attachFiles(ctx, page, profile, files); err != nil {
			return SendResult{SentHash: sentHash, Hold: HoldAttachFailed}, nil
		}
	}

	// Шаг 8: фокус + insertText.
	if err := r.typeComposer(ctx, page, profile, text); err != nil {
		return SendResult{SentHash: sentHash, Hold: HoldComposerMismatch}, nil
	}

	// Шаг 9: нажатие отправки + подтверждение.
	confirmed, err := r.pressSend(ctx, page, profile, sentHash)
	if err != nil {
		return SendResult{SentHash: sentHash}, err
	}
	if !confirmed {
		return SendResult{SentHash: sentHash, Hold: HoldSendUnconfirmed}, nil
	}
	return SendResult{SentHash: sentHash}, nil
}

// checkLogin — шаг 2: выполнен ли вход (ТЗ Relay §8.3).
func (r *Relay) checkLogin(ctx context.Context, page *rod.Page, p *orchsite.Profile) error {
	_, err := page.Timeout(loginTimeout).Element(p.LoggedInSelector)
	return err
}

// waitGenerationDone — шаг 3: ждать конца генерации.
func (r *Relay) waitGenerationDone(ctx context.Context, page *rod.Page, p *orchsite.Profile) error {
	deadline := r.clk.Now().Add(time.Duration(r.cfg.AnswerTimeoutMin) * time.Minute)
	for {
		if r.clk.Now().After(deadline) {
			return fmt.Errorf("relay: %s", HoldOrchError)
		}
		// Короткий таймаут: нет generating → генерации нет (быстрый выход).
		if _, err := page.Timeout(pollInterval).Element(p.GeneratingSelector); err != nil {
			return nil
		}
		if err := sleepCtx(ctx, pollInterval); err != nil {
			return err
		}
	}
}

// checkRateError — шаг 4: тексты лимита/ошибки. "" = ок.
func (r *Relay) checkRateError(page *rod.Page, p *orchsite.Profile) string {
	res, err := page.Eval(`() => document.body ? document.body.innerText : ''`)
	if err != nil {
		return ""
	}
	text := res.Value.Str()
	if p.RateLimit(text) {
		return HoldRateLimit
	}
	if p.Error(text) {
		return HoldOrchError
	}
	return ""
}

// waitMinSendInterval — шаг 5: пауза min_send_interval_sec.
func (r *Relay) waitMinSendInterval(ctx context.Context) error {
	if r.cfg.MinSendIntervalSec <= 0 || r.lastSent.IsZero() {
		return nil
	}
	elapsed := r.clk.Now().Sub(r.lastSent)
	need := time.Duration(r.cfg.MinSendIntervalSec) * time.Second
	if elapsed < need {
		return sleepCtx(ctx, need-elapsed)
	}
	return nil
}

// attachFiles — шаг 7: SetFiles + ожидание чипов.
func (r *Relay) attachFiles(ctx context.Context, page *rod.Page, p *orchsite.Profile, files []Attachment) error {
	input, err := page.Element(p.FileInputSelector)
	if err != nil {
		return err
	}
	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.Path
	}
	if err := input.SetFiles(paths); err != nil {
		return err
	}
	deadline := r.clk.Now().Add(time.Duration(r.cfg.UploadTimeoutSec) * time.Second)
	for {
		if r.clk.Now().After(deadline) {
			return fmt.Errorf("relay: %s", HoldAttachFailed)
		}
		chips, err := page.Eval(`() => document.querySelectorAll(` + jsq(p.AttachmentChipSel) + `).length`)
		if err == nil && chips.Value.Int() == len(files) {
			up, err := page.Eval(`() => document.querySelectorAll(` + jsq(p.UploadingSelector) + `).length`)
			if err == nil && up.Value.Int() == 0 {
				return nil
			}
		}
		if err := sleepCtx(ctx, pollInterval); err != nil {
			return err
		}
	}
}

// typeComposer — шаг 8: фокус + insertText, проверка нормализованного текста.
func (r *Relay) typeComposer(ctx context.Context, page *rod.Page, p *orchsite.Profile, text string) error {
	comp, err := page.Element(p.ComposerSelector)
	if err != nil {
		return err
	}
	comp.MustFocus()
	if err := page.InsertText(text); err != nil {
		return err
	}
	got, err := page.Eval(`() => { const e = document.querySelector(` + jsq(p.ComposerSelector) + `); return e ? e.value : ''; }`)
	if err != nil {
		return err
	}
	if normalize(got.Value.Str()) != normalize(text) {
		_, _ = page.Eval(`() => { const e = document.querySelector(` + jsq(p.ComposerSelector) + `); if (e) e.value = ''; }`)
		return fmt.Errorf("relay: %s", HoldComposerMismatch)
	}
	return nil
}

// pressSend — шаг 9: нажатие + подтверждение за send_confirm_sec.
func (r *Relay) pressSend(ctx context.Context, page *rod.Page, p *orchsite.Profile, sentHash string) (bool, error) {
	before, err := r.userMessageCount(page, p)
	if err != nil {
		return false, err
	}
	btn, err := page.Element(p.SendButtonSelector)
	if err != nil {
		return false, err
	}
	btn.MustClick()

	deadline := r.clk.Now().Add(time.Duration(r.cfg.SendConfirmSec) * time.Second)
	for {
		if r.clk.Now().After(deadline) {
			return false, nil
		}
		after, err := r.userMessageCount(page, p)
		if err == nil && after == before+1 {
			last, err := r.userTextAt(page, p, after-1)
			if err == nil && normHash(last) == sentHash {
				if empty, err := r.composerEmpty(page, p); err == nil && empty {
					r.lastSent = r.clk.Now()
					return true, nil
				}
			}
		}
		if err := sleepCtx(ctx, pollInterval); err != nil {
			return false, err
		}
	}
}

// userMessageCount — число пользовательских сообщений.
func (r *Relay) userMessageCount(page *rod.Page, p *orchsite.Profile) (int, error) {
	res, err := page.Eval(`() => document.querySelectorAll(` + jsq(p.UserMessageSelector) + `).length`)
	if err != nil {
		return 0, err
	}
	return res.Value.Int(), nil
}

// userTextAt — текст n-го пользовательского сообщения.
func (r *Relay) userTextAt(page *rod.Page, p *orchsite.Profile, idx int) (string, error) {
	res, err := page.Eval(`(i) => { const els = document.querySelectorAll(` + jsq(p.UserMessageSelector) + `); return els[i] ? els[i].innerText : ''; }`, idx)
	if err != nil {
		return "", err
	}
	return res.Value.Str(), nil
}

// composerEmpty — пустое ли поле ввода.
func (r *Relay) composerEmpty(page *rod.Page, p *orchsite.Profile) (bool, error) {
	res, err := page.Eval(`() => { const e = document.querySelector(` + jsq(p.ComposerSelector) + `); return e ? e.value : ''; }`)
	if err != nil {
		return false, err
	}
	return isBlank(res.Value.Str()), nil
}

// jsq — экранирует селектор для вставки в JS-строку.
func jsq(sel string) string {
	return "'" + sel + "'"
}

// sleepCtx — сон с учётом ctx.
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// onSending — хук записи SENDING в БД (заполняется координатором).
func (r *Relay) onSending(page *rod.Page, sentHash string) error {
	if r.onSendingHook != nil {
		return r.onSendingHook(page, sentHash)
	}
	return nil
}
