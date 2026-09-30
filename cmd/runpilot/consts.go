// Package main — константы runpilot (таблица R2): коды, режимы, параметры CLI.
package main

const (
	// exitConfigError — код выхода CLI при ошибке схемы/семантики конфига.
	exitConfigError = 2
	// twoArgs — число аргументов команд с двумя позициями (cobra.ExactArgs).
	twoArgs = 2
	// setArgs — число аргументов команды `set`.
	setArgs = 3
	// tableMinWidth/tableTabWidth/tablePad — параметры табличного вывода.
	tableMinWidth = 2
	tableTabWidth = 4
	tablePad      = 2
	// compositeParts — сегментов host:tmux_session[:window.pane].
	compositeParts = 3
	// windowPaneParts — полей window.pane.
	windowPaneParts = 2
	// seqFieldIdx — позиция номера в имени фикстуры <state>-NN.txt.
	seqFieldIdx = 2
	// procMinFields/procArgsStart — поля строки процесса (pid,ppid,args).
	procMinFields = 3
	procArgsStart = 2
	// httpNonOK — HTTP-статусы >= httpNonOK считаются не-OK.
	httpNonOK = 300
	// httpTimeoutSec — таймаут HTTP-клиента (сек).
	httpTimeoutSec = 10
	// promptMaxLen — длина промпта в TUI до эллипсиса.
	promptMaxLen = 32
	// secondsPerMin — секунд в минуте.
	secondsPerMin = 60
	// serveErrChanLen — размер канала ошибок в serve.
	serveErrChanLen = 4
	// queueClassLow — номер класса low (раздел 5).
	queueClassLow = 3
	// plainDirMode/plainFileMode — режимы каталога/файла (readable).
	plainDirMode  = 0o755
	plainFileMode = 0o644
	// tmuxDirMode/tmuxFileMode — режимы каталога/файла tmux-блока (0700/0600).
	tmuxDirMode  = 0o700
	tmuxFileMode = 0o600
	// eventChanLen — размер канала событий команды `events`.
	eventChanLen = 256
	// briefPayloadMax — длина payload в выводе `events`.
	briefPayloadMax = 40
	// embeddedCmdChanLen — размер канала команд встроенного узла.
	embeddedCmdChanLen = 64
	// loopbackIP — петлевой адрес (встроенный узел).
	loopbackIP = "127.0.0.1"
	// serviceStopDialSec — таймаут проверки, запущена ли служба (6.6).
	serviceStopDialSec = 1
	// defaultSince — период stats по умолчанию.
	defaultSince = "24h"
	// httpScheme/httpsScheme — схемы HTTP-адресов координатора.
	httpScheme  = "http://"
	httpsScheme = "https://"

	// Коды выхода runpilot exec (раздел 8.1 ТЗ).
	exitNoCoordinator = 69 // координатор недоступен
	exitNoSlot        = 75 // слот не получен за --wait-max
	exitDenied        = 77 // отказ в доступе (токен, IP)
	exitTimeout       = 124 // истёк --timeout
	// exitSelfUpdated — код выхода демонов узла после успешного self-update
	// (раздел 14.2 ТЗ): systemd (Restart) перезапускает новый бинарник.
	// Тот же 75, что exitNoSlot, — разные процессы, код не пересекается.
	exitSelfUpdated = 75
	// jobPollSec — удержание одного long-poll в /jobs/{sid}/wait (не порог
	// ТЗ: меньше jobWaitMaxSec координатора, client ограничивает остатком).
	jobPollSec = 10
	// jobErrBodyLimit — длина тела ответа в сообщении об ошибке runpilot exec.
	jobErrBodyLimit = 200
	// defKillGraceSec/defHeartbeatSec — запасные значения, если в
	// bootstrap-конфиге узла они не заданы (не пороги ТЗ).
	defKillGraceSec  = 5
	defHeartbeatSec  = 5
)
