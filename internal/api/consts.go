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

	// browserStartTimeout — запуск Xvfb + Chrome (первый раз, холодный профиль).
	browserStartTimeout = 90 * time.Second
	// termReaperTick — период проверки бездействия терминалов (не порог ТЗ:
	// кратно меньше типового terminal_idle_min).
	termReaperTick = 10 * time.Second

	// defFlapsSince — окно flaps по умолчанию (10 мин).
	defFlapsSince = 10 * time.Minute

	// distVersionTimeout — таймаут `<dist> version` (W12: защита от stale dist).
	distVersionTimeout = 10 * time.Second

	// enrollInstallTimeout — таймаут ssh-установки узла (auto_install):
	// скачивание бинарника + install + systemd. 5 мин достаточно для tailnet.
	enrollInstallTimeout = 5 * time.Minute

	// Медиа-пробы (цикл W15, этап 2). Генерация — ffmpeg drawtext (DejaVu).
	// mediaProbeFontPath — шрифт для чисел на кадре (системный DejaVu).
	mediaProbeFontPath = "/usr/share/fonts/dejavu-sans-fonts/DejaVuSans-Bold.ttf"
	// mediaProbeNum6Min / mediaProbeNum6Range — 6-значное число в P-image
	// (100000..999999).
	mediaProbeNum6Min    = 100000
	mediaProbeNum6Range  = 900000
	// mediaProbeNum2Min / mediaProbeNum2Range — 2-значное число в P-video
	// (10..99), три числа по 2 с.
	mediaProbeNum2Min   = 10
	mediaProbeNum2Range = 90
	// mediaProbeNumSegs / mediaProbeSegSec — число сегментов в mp4 и длительность
	// каждого (3 × 2 с = 6 с): сегмент i занимает [i*SegSec, (i+1)*SegSec).
	mediaProbeNumSegs = 3
	mediaProbeSegSec  = 2
	// mediaProbePNGW / mediaProbePNGH / mediaProbeFontSize — кадр PNG/кадр mp4.
	mediaProbePNGW   = 400
	mediaProbePNGH   = 200
	mediaProbeFontSize = 100
	// mediaProbeMP4Sec / mediaProbeMP4FPS — длительность и fps mp4 (6 с, 5 fps).
	mediaProbeMP4Sec = 6
	mediaProbeMP4FPS = 5
	// mediaProbeTimeout — таймаут генерирующего запроса пробы (модель читает
	// медиа и отвечает; запас на 20 картинок / 2 видео).
	mediaProbeTimeout = 120 * time.Second
	// mediaProbeMaxTokens — потолок ответа пробы: модель-raisoner пишет
	// рассуждение + ответ; запас, чтобы ответ не обрезался по max_tokens.
	mediaProbeMaxTokens = 512
)
