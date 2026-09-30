// Package doctor — константы (таблица R2): версии, лимиты, коды и режимы.
package doctor

const (
	// tmuxMinMajor — минимальная основная версия tmux.
	tmuxMinMajor = 3
	// tmuxMinMinor — минимальная минорная версия tmux.
	tmuxMinMinor = 2
	// tmuxVersionFields — число полей версии tmux «major.minor» и минимум
	// полей в выводе tmux -V.
	tmuxVersionFields = 2
	// secretFileMode — ожидаемый режим конфигов/БД (0600).
	secretFileMode = 0o600
	// foundListMax — сколько найденных имён показывать в списке.
	foundListMax = 5
	// statusRedirectLow/High — диапазон HTTP-кодов редиректов [300, 400).
	statusRedirectLow  = 300
	statusRedirectHigh = 400
)
