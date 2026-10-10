package logx

import (
	"context"
	"log/slog"
	"sync"

	"runpilot/internal/store"
)

// SessEnrich — slog.Handler-обёртка: к записям, несущим атрибут "sid",
// добавляет атрибут "session" = "host/name" (поиск сессии по PK в локальной
// SQLite). Исходный атрибут "sid" сохраняется. Без store (или при неизвестном
// sid) запись проходит без изменений.
//
// Зачем: sid — хеш, который не ассоциируется с сессией; "узел/имя" делает
// строку лога читаемой. Обёртка прозрачна для записей без "sid" (напр.,
// серверные/мониторные логи), поэтому её можно вешать на общий логгер.
type SessEnrich struct {
	inner slog.Handler
	mu    sync.RWMutex
	st    *store.Store
}

// NewSessEnrich — обёртка над inner. Store ставится позже (SetStore), после
// store.Open: логгер создаётся до открытия БД.
func NewSessEnrich(inner slog.Handler) *SessEnrich {
	return &SessEnrich{inner: inner}
}

// SetStore — ссылка на БД для поиска сессии. Вызывается один раз после
// store.Open; до вызова обёртка прозрачна.
func (h *SessEnrich) SetStore(st *store.Store) {
	h.mu.Lock()
	h.st = st
	h.mu.Unlock()
}

func (h *SessEnrich) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

// Handle — добавить "session"=host/name к записи с "sid", затем inner.
func (h *SessEnrich) Handle(ctx context.Context, r slog.Record) error {
	if st := h.store(); st != nil {
		var sid string
		r.Attrs(func(a slog.Attr) bool {
			if a.Key == "sid" {
				sid = a.Value.String()
			}
			return true
		})
		if sid != "" {
			if sess, err := st.GetSession(sid); err == nil {
				r.AddAttrs(slog.String("session", sessLabel(sess)))
			}
		}
	}
	return h.inner.Handle(ctx, r)
}

func (h *SessEnrich) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &SessEnrich{inner: h.inner.WithAttrs(attrs), st: h.store()}
}

func (h *SessEnrich) WithGroup(name string) slog.Handler {
	return &SessEnrich{inner: h.inner.WithGroup(name), st: h.store()}
}

// store — текущая ссылка на БД (nil до SetStore).
func (h *SessEnrich) store() *store.Store {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.st
}

// sessLabel — "host/name"; без host — просто name.
func sessLabel(sess store.SessionRecord) string {
	if sess.Host != "" {
		return sess.Host + "/" + sess.Name
	}
	return sess.Name
}
