// Package relay — автомат связи сессии с оркестратором (ТЗ Relay §5–§14).
// Каждый переход состояния сначала пишется в SQLite, затем — побочное действие.
// Тексты отчётов/ответов не пишутся: только SHA-256, размеры, время.
package relay

import "time"

const (
	// pollInterval — период опроса DOM (генерация, стабильность ответа).
	pollInterval = 200 * time.Millisecond
	// loginTimeout — дедлайн поиска logged_in_selector.
	loginTimeout = 5 * time.Second
	// idlePollInterval — период опроса IDLE при ожидании ввода (12.1.1).
	idlePollInterval = 200 * time.Millisecond
)
