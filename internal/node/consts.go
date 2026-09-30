// Package node — константы (таблица R2): периоды, буферы, поля, схемы.
package node

import "time"

const (
	// gpuTimeoutSec — таймаут на одну команду GPU (не порог ТЗ).
	gpuTimeoutSec = 10

	// binFilePerm — права загруженного бинарника self-update (14.2): исполняемый.
	binFilePerm = 0o755

	// scanInitBuf — начальный буфер сканера (64 KiB).
	scanInitBuf = 64 * 1024
	// procScanMaxBuf — макс. буфер processTable (4 MiB).
	procScanMaxBuf = 4 * 1024 * 1024
	// paneScanMaxBuf — макс. буфер parsePanes (1 MiB).
	paneScanMaxBuf = 1024 * 1024
	// psLeadingFields — число ведущих полей ps-строки (pid, ppid, uid, etimes).
	psLeadingFields = 4
	// psField* — позиции полей ps-строки (pid, ppid, uid, etimes, args…).
	psFieldUID    = 2
	psFieldEtimes = 3

	// Имена процессов-источников для классификации source (8.2 ТЗ):
	// цепочка родителей до PID 1, первое совпадение.
	procRootPID    = 1
	sourceCron     = "cron"
	sourceSystemd  = "systemd"
	sourceSshd     = "sshd"
	sourceTmux     = "tmux"
	sourceOther    = "other"
	// runpilotExe — базовое имя бинарника runpilot (исключение runpilot exec из внешних).
	runpilotExe = "runpilot"

	// proc — окружение процесса (8.2: OPENAI_BASE_URL из /proc environ).
	procDir          = "/proc"
	procEnvironName  = "environ"
	procStatusName   = "status"
	procEnvSep       = "\x00"
	openaiBaseURLKey = "OPENAI_BASE_URL="
	procUidField     = "Uid:"

	// killStartTimeToleranceSec — допуск сверки start_time при kill (8.2):
	// start_time стабилен (now−etimes), допуск покрывает rounding ps etimes.
	killStartTimeToleranceSec = 3
	// decimalBase / int64Bits — основание и разрядность strconv.
	decimalBase = 10
	int64Bits   = 64
	// mibInBytes — МБ в байтах (node_health disk_free_mb, 14.4).
	mibInBytes = 1024 * 1024
	// paneFieldCount — число tab-полей вывода list-panes.
	paneFieldCount = 10
	// paneField* — позиции полей list-panes (0=session, 1=window).
	paneFieldPaneIndex = 2
	paneFieldPaneID    = 3
	paneFieldPanePID   = 4
	paneFieldCommand   = 5
	paneFieldDead      = 6
	paneFieldSID       = 7
	paneFieldExit      = 8 // @runpilot_exit (7.3 C1): код завершения кодера
	paneFieldDir       = 9 // cwd панели (8.3 X1): каталог UNMANAGED-панели

	// Периоды снимков управляемых панелей (раздел 7 ТЗ): 1 с для
	// DISPATCHING/RUNNING/DETACHED, 2 с для QUEUED, 5 с для остальных.
	// BUSY — активный ход (1 с); IDLE/PROMPT/WAIT_UI — ожидание или
	// удержание (2 с); UNKNOWN — не диспетчеризуется (5 с).
	periodBusy    = time.Second
	periodWaiting = 2 * time.Second
	periodUnknown = 5 * time.Second

	// adopt (8.3 X1): ожидание выхода кодера после quit_text (дальше
	// respawn-pane -k принудительно) и период опроса.
	adoptQuitWait = 20 * time.Second
	adoptPoll     = time.Second

	// Pulse — пульс координатору без смены состояния (раздел 7 ТЗ).
	pulseInterval = 10 * time.Second

	// inputPreviewLen — сколько символов ввода показывают в очереди TUI.
	inputPreviewLen = 80

	// Задержки переподключения (раздел 13 ТЗ): экспоненциальные 1 → 30 с.
	wsBackoffBase = time.Second
	wsBackoffMax  = 30 * time.Second
	// wsBackoffFactor — множитель экспоненциальной задержки.
	wsBackoffFactor = 2
	// wsReadDeadline — максимум молчания координатора (защита от
	// «мёртвого» соединения; пульс узла, команды и ping обновляют канал).
	wsReadDeadline = 2 * time.Minute
	// wsPongWriteTimeout — дедлайн записи pong-ответа на ping.
	wsPongWriteTimeout = 5 * time.Second
	// wsOutBufLen — размер буфера канала исходящих сообщений.
	wsOutBufLen = 64
	// wsScheme / wssScheme — схемы ws-адресов.
	wsScheme  = "ws://"
	wssScheme = "wss://"
	// flushBarrierMax — максимум ожидания на каждый этап барьера записи
	// перед выходом после self-update (14.2): сток out, отправка барьера,
	// дописывание кадров писателем.
	flushBarrierMax = 2 * time.Second
	// flushDrainPoll — шаг опроса стока исходящего канала (14.2).
	flushDrainPoll = 10 * time.Millisecond

	// Вставка задания (раздел 13.1 ТЗ, v2): узел переснимает панель после
	// paste-buffer, пока не отрисовалась вставка (короткие паузы между
	// переснятиями; полный бюджет web.paste_verify_sec держит координатор).
	pasteSettle = 150 * time.Millisecond
	pastePoll   = 8

	// dimsFields — число полей «ширина высота» из display-message (screen).
	dimsFields = 2

	// ptyReadBuf — буфер чтения вывода PTY-терминала (13.4 ТЗ): 4 KiB —
	// достаточно для одного «кванта» tmux-отрисовки без потерь.
	ptyReadBuf = 4096
	// ptyKillGrace — SIGTERM tmux attach до SIGKILL при закрытии терминала
	// (13.4): tmux штатно детачится по SIGTERM, SIGKILL — страховка.
	ptyKillGrace = 2 * time.Second

	// qwenSettingsFileMode — режим ~/.qwen/settings.json (W9 доп-3c).
	qwenSettingsFileMode = 0o600
)
