// П.6(c) — пуш строки статуса (set_status) и сводки (set_summary)
// координатор → узлы ПО Событию изменения текста.
//
// Строка статуса (@runpilot_status) — «Q N/M held» (Scheduler.HeldStatus):
// состояния сессии/позиции в очереди/флага held меняют N и M → меняется текст.
// Сводка (@runpilot_summary) — состояния серверов + длина очереди
// (Scheduler.SummaryText). Оба текста — глобальные (одинаковы для всех панелей),
// но кэшируются: status — per pane, summary — per host; неизменившийся текст
// повторно не отправляется (правило «по событию изменения текста»).
//
// Транспорт: fire-and-forset (Hub.PushPane, без ожидания reply). После
// переподключения узла (hello+panes) ResendHost принудительно переотправляет
// статусы всех его панелей + сводку (кэш сбрасывается) — на случай, если tmux
// опция потеряна (рестарт tmux-сервера) или панель новая.

package api

import (
	"log/slog"
	"sync"

	"runpilot/internal/proto"
)

// StatusSource — источник текстов статусной строки (п.6c). Продакшн —
// планировщик; тесты — фэйк с изменяемыми текстами.
type StatusSource interface {
	// HeldStatus — строка статуса «Q N/M held» (set_status, @runpilot_status).
	HeldStatus() string
	// SummaryText — сводка «состояния серверов + длина очереди»
	// (set_summary, @runpilot_summary).
	SummaryText() string
}

// StatusPusher — п.6c: считает тексты из StatusSource и шлёт их узлам, только
// при изменении (кэш last-sent). PushAll — событие изменения (тик/смена
// состояния); ResendHost — переподключение узла.
type StatusPusher struct {
	hub *Hub
	src StatusSource
	log *slog.Logger

	mu          sync.Mutex
	lastStatus  map[string]string // pane_id → последний отправленный статус
	lastSummary map[string]string // host → последняя отправленная сводка
}

// NewStatusPusher создаёт пушер. log nil → slog.Default().
func NewStatusPusher(hub *Hub, src StatusSource, log *slog.Logger) *StatusPusher {
	if log == nil {
		log = slog.Default()
	}
	return &StatusPusher{
		hub:         hub,
		src:         src,
		log:         log,
		lastStatus:  map[string]string{},
		lastSummary: map[string]string{},
	}
}

type statusSend struct {
	paneID string
	kind   proto.Kind
	text   string
}

// PushAll — пересчитать оба текста и отправить только изменившиеся. Вызывается
// планировщиком после каждого тика (Tick) и при смене состояния сервера
// (ServerState) — это и есть «событие изменения текста».
func (p *StatusPusher) PushAll() {
	statusText := p.src.HeldStatus()
	summaryText := p.src.SummaryText()
	panes := p.hub.AllPanes()

	// Под p.mu решаем, что шлём (и обновляем кэш); сами отправляем вне lock
	// (PushPane берёт hub.mu + пишет в сокет — не держать p.mu во время I/O).
	p.mu.Lock()
	var sends []statusSend
	// set_status: per pane — текст изменился для панели → отправить.
	for _, pn := range panes {
		if p.lastStatus[pn.Pane.PaneID] != statusText {
			sends = append(sends, statusSend{pn.Pane.PaneID, proto.KindSetStatus, statusText})
			p.lastStatus[pn.Pane.PaneID] = statusText
		}
	}
	// set_summary: per host — сводка изменилась для хоста → всем его панелям.
	summaryChanged := map[string]bool{}
	for _, pn := range panes {
		if summaryChanged[pn.Host] {
			continue
		}
		if p.lastSummary[pn.Host] != summaryText {
			summaryChanged[pn.Host] = true
			p.lastSummary[pn.Host] = summaryText
		}
	}
	for _, pn := range panes {
		if summaryChanged[pn.Host] {
			sends = append(sends, statusSend{pn.Pane.PaneID, proto.KindSetSummary, summaryText})
		}
	}
	p.mu.Unlock()

	for _, s := range sends {
		if err := p.hub.PushPane(s.paneID, p.msg(s.kind, s.paneID, s.text)); err != nil {
			p.log.Warn("api: status: push: " + err.Error(), "pane", s.paneID)
		}
	}
}

// ResendHost — переподключение узла (hello+panes): принудительная
// переотправка set_status + set_summary всем его панелям (кэш обходится).
func (p *StatusPusher) ResendHost(host string) {
	panes := p.hub.PanesOfHost(host)
	if len(panes) == 0 {
		return
	}
	statusText := p.src.HeldStatus()
	summaryText := p.src.SummaryText()

	p.mu.Lock()
	var sends []statusSend
	for _, pn := range panes {
		sends = append(sends,
			statusSend{pn.Pane.PaneID, proto.KindSetStatus, statusText},
			statusSend{pn.Pane.PaneID, proto.KindSetSummary, summaryText})
		p.lastStatus[pn.Pane.PaneID] = statusText
	}
	p.lastSummary[host] = summaryText
	p.mu.Unlock()

	for _, s := range sends {
		if err := p.hub.PushPane(s.paneID, p.msg(s.kind, s.paneID, s.text)); err != nil {
			p.log.Warn("api: status: resend: " + err.Error(), "pane", s.paneID)
		}
	}
}

// msg — сообщение set_status/set_summary для панели (PaneID + Text; CmdID не
// ставится — fire-and-forget, reply узла отбрасывается координатором).
func (p *StatusPusher) msg(kind proto.Kind, paneID, text string) proto.Msg {
	m := proto.New(kind)
	m.PaneID = paneID
	m.Text = text
	return m
}
