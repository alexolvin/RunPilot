package web

import (
	"context"
	"log/slog"
	"net"
	"time"
)

// ListenWithRetry — повторяет net.Listen, пока адрес недоступен (v2 раздел
// 2.3: адрес Tailscale появляется с задержкой после перезагрузки). Веб-
// слушатель при этом работает. Интервал — из вызывающего (coordinator.
// bind_retry_sec), не из литерала.
func ListenWithRetry(ctx context.Context, addr string, interval time.Duration, log *slog.Logger) (net.Listener, error) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		ln, err := net.Listen("tcp", addr)
		if err == nil {
			return ln, nil
		}
		log.Warn("web: адрес недоступен, повтор привязки", "addr", addr,
			"err", err.Error(), "retry_sec", int(interval.Seconds()))
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}
