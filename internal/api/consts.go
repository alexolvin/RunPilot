// Package api — константы (таблица R2): SSE, ping, порты и размеры.
package api

import "time"

const (
	// httpScheme — схема URL дистрибутива узла (14.2): API координатора
	// слушает на http (наружу только через tailscale serve/VPN).
	httpScheme = "http://"
	// decimalBase / int64Bits — основание и разрядность strconv.ParseInt.
	decimalBase = 10
	int64Bits   = 64
	// sseInitialEvents — сколько последних событий вернуть при after=0.
	sseInitialEvents = 200
	// ssePollMS — период опроса хранилища событий (мс).
	ssePollMS = 100
	// sseKeepAliveSec — период SSE keep-alive (сек).
	sseKeepAliveSec = 15
	// sseBatchSize — размер пачки в store.EventList.
	sseBatchSize = 500
	// pingWriteDeadlineSec — дедлайн записи WS ping-кадра (сек).
	pingWriteDeadlineSec = 5
	// p95Percent / percentWhole — процентиль p95: n * p95 / whole.
	p95Percent   = 95
	percentWhole = 100
	// hoursPerDay — часов в сутках (дни → часы для retention).
	hoursPerDay = 24
	// statsDefaultHours — окно stats по умолчанию (часов).
	statsDefaultHours = 24
	// peekDefault — количество записей по умолчанию в /peek.
	peekDefault = 40
	// settingsRevisionsLimit — сколько ревизий настроек вернуть списком.
	settingsRevisionsLimit = 100
	// journalDefaultPageSize — страница журнала при unset web.page_size.
	journalDefaultPageSize = 50
	// kbInBytes — килобайт в байте (web.paste_max_kb → байты).
	kbInBytes = 1024

	// idemCacheTTL — время жизни записи Idempotency-Key (O2): достаточно
	// покрыть окно повторного нажатия / повторного запроса сетью (не порог ТЗ).
	idemCacheTTL = 10 * time.Minute
	// idemCacheMax — потолок кэша идемпотентности (записей).
	idemCacheMax = 1000

	// nodePingInterval — период ping координатор→узел (Э6): держит канал
	// живым при простое — read deadline узла (2 мин) не истекает.
	// Не порог ТЗ: достаточно быть кратно меньше read deadline узла.
	nodePingInterval = 15 * time.Second

	// jobWaitPollMS — период опроса аренды в /jobs/{sid}/wait (long-poll,
	// раздел 8.1): не порог ТЗ, достаточно кратно меньше тика планировщика.
	jobWaitPollMS = 200
	// jobWaitDefaultSec — удержание long-poll при отсутствии wait_sec (не
	// порог ТЗ: меньше типовых таймаутов прокси).
	jobWaitDefaultSec = 15
	// jobWaitMaxSec — потолок удержания одного long-poll.
	jobWaitMaxSec = 25

	// nodeCmdTimeout — таймаут команды координатор→узел (8.2: kill_process).
	nodeCmdTimeout = 5 * time.Second

	// adoptOpTimeout — ожидание adopt (8.3 X1): таймаут выхода кодера на
	// узле (20 с) + запас на respawn-pane и set-option.
	adoptOpTimeout = 60 * time.Second
	// retentionLoopTick — период очистки хранилища (retention.*, раздел 14).
	retentionLoopTick = time.Hour

	// status2xxLo / status2xxHi — диапазон «успех» 2xx (O2: кэш идемпотентности
	// кэширует только успешные ответы первого запроса).
	status2xxLo = 200
	status2xxHi = 300

	// Терминал в браузере (13.4 ТЗ).
	// ptyOpenTimeout — ожидание pty_open→reply при открытии терминала.
	ptyOpenTimeout = 10 * time.Second
	// termReaperTick — период проверки бездействия терминалов (не порог ТЗ:
	// кратно меньше типового terminal_idle_min).
	termReaperTick = 10 * time.Second
)
