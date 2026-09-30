// Package pty — константы (таблица R2).
package pty

import "time"

const (
	// sizeTimeout — запрос размера окна (display-message) не дёргает узел:
	// tmux-сервер не отвечает — ошибка открытия PTY.
	sizeTimeout = 3 * time.Second
	// sizeFields — число полей «ширина высота» в ответе display-message.
	sizeFields = 2
)
