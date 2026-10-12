package relay

import (
	"context"
	"log/slog"
	"time"

	"github.com/go-rod/rod"

	"runpilot/internal/clock"
	"runpilot/internal/config"
)

// Relay — автомат связи одной сессии (ТЗ Relay §5).
type Relay struct {
	cfg  config.Relay
	bcfg config.Browser
	clk  clock.Clock
	log  *slog.Logger

	// lastSent — время последней отправки в этой связи (шаг 5).
	lastSent time.Time
	// onSendingHook — запись SENDING в БД (заполняется координатором).
	onSendingHook func(page *rod.Page, sentHash string) error
	// deliver — хуки доставки агенту (заполняются координатором, раздел 12).
	deliver DeliverHooks
}

// New — сборка автомата.
func New(cfg config.Relay, bcfg config.Browser, clk clock.Clock, log *slog.Logger) *Relay {
	return &Relay{cfg: cfg, bcfg: bcfg, clk: clk, log: log}
}

// SetOnSending — хук записи SENDING (сначала БД, потом действие — §строгое 4).
func (r *Relay) SetOnSending(fn func(page *rod.Page, sentHash string) error) {
	r.onSendingHook = fn
}

// MarkSent — отметка времени отправки (сброс паузы, вызывается при подтверждении).
func (r *Relay) MarkSent() {
	r.lastSent = r.clk.Now()
}

// PageLoadTimeout — таймаут загрузки страницы (browser).
func (r *Relay) PageLoadTimeout() time.Duration {
	return time.Duration(r.bcfg.PageLoadTimeoutSec) * time.Second
}

var _ = context.Background
