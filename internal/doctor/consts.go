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
	// client_coordinator: таймаут HTTP-проверки (сек) + пороги статуса.
	// 2xx = PASS (координатор отвечает и принимает токен); 401/403 = FAIL
	// «токен отклонён»; прочие коды и ошибка соединения = FAIL с адресом+кодом.
	clientCoordinatorTimeoutSec = 5
	http2xxLow                  = 200
	http2xxHigh                 = 300
	httpUnauthorized            = 401
	httpForbidden               = 403
	// Позиции полей формата checkCoderTimeoutEnv:
	// "#{pane_dead}\t#{pane_pid}\t#{session_name}\t#{@runpilot_sid}".
	paneFieldDead    = 0
	paneFieldPID     = 1
	paneFieldName    = 2
	paneFieldSID     = 3
	paneFieldCount   = 4
	paneDeadFlag     = "1" // #{pane_dead} для мёртвой панели
	// Позиции полей вывода `ps -A -o pid=,ppid=,args=` (buildProcTree):
	// pid и ppid — одиночные поля, args — всё остальное (spaces схлопнуты).
	psFieldPID       = 0
	psFieldPPID      = 1
	psFieldArgsStart = 2
	psMinFields      = 3
)
