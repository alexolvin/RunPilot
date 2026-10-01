package main

// Константы стенда (R2: значения в consts.go, не в логике).
const (
	// loopback — стенд слушает только петлевой адрес.
	loopback = "127.0.0.1"
	// httpScheme — петлевой стенд работает по http (cookie без Secure).
	httpScheme = "http://"
	// tokenEnv — переменная окружения с операторским токеном стенда.
	tokenEnv = "RUNPILOT_STAND_TOKEN"
	// dbFile — имя файла временной БД стенда.
	dbFile = "runpilot.db"
	// defaultClockStart — фиксированный старт виртуальных часов (RFC3339):
	// скриншоты стенда воспроизводимы.
	defaultClockStart = "2026-01-01T00:00:00Z"
	// infoFileMode — режим файла-информации (0600: только владелец читает).
	infoFileMode = 0o600
)

// --- Сценарии W4 (значения сидов для скриншотов/CONTROL; R2) ---
const (
	// Серверы профиля «normal».
	nSrvOne  = "srv-01"
	nSrvTwo  = "srv-02"
	nOneSlots = 4
	nTwoSlots = 2
	nOnePrio  = 10
	nTwoPrio  = 5
	nOneKV    = 42.0
	nTwoKV    = 15.0
	nOneGen   = 128.0
	nTwoGen   = 46.0
	nOneGPU   = 82
	nTwoGPU   = 33

	// Профиль «faults»: серверы в разных состояниях.
	fSrvOne     = "srv-01"
	fSrvTwo     = "srv-02"
	fSrvThr     = "srv-03"
	fSrvFou     = "srv-04"
	fSlots      = 2
	fOneKV      = 30.0
	fOneGen     = 80.0
	fOneGPU     = 55
	fSrvPrioUp  = 10
	fSrvPrioLow = 5

	// Профиль «many»: объёмная нагрузка.
	mSrvPrefix  = "srv-"
	mServerN    = 4
	mSlotEach   = 8
	mSessionN   = 60
	mNodeN      = 8
	mTurnN      = 40
	mEventN     = 120
	mSrvPrio    = 10
	mKVBase     = 40.0
	mKVStep     = 5.0
	mGenBase    = 100.0
	mGenStep    = 20.0
	mGPUBase    = 60
	mGPUStep    = 10
	mRunCut     = 4 // i % mSlotEach < 4 → RUNNING
	mQueueCut   = 6 // i % mSlotEach < 6 → QUEUED
	mHoldAt     = 6 // i % mSlotEach == 6 → HOLD
	mNodeIPPref = "10.0.1."

	// Обычные объёмы сидов.
	nRunningN   = 2
	nQueuedN    = 3
	nIdleN      = 2
	nHoldN      = 1
	nTurnN      = 12
	nEventN     = 30
	nTurnSpanH  = 24
	nSlotStep   = 1

	// Профиль «attention»: внимание оператора (HOLD разных кодов, PROMPT,
	// UNMANAGED, внешняя нагрузка от cron).
	aExtLoad = 3 // внешняя нагрузка на srv-01 (vllm, cron)

	// Узлы.
	nNodeHost = "node-01"
	nNodeIP   = "192.0.2.5"
	nNodeVer  = "0.4.0"
	nSockA    = "/tmp/ssh1001/a"
	nSockB    = "/tmp/ssh1002/b"

	// Ходы/события: длительности и интервалы (секунды).
	nTurnDurSec   = 3600
	nTurnStepMin  = 115
	nEventStepSec = 300
	nLeaseSlotA   = 1

	// Нумерация сидов (SID = «SS» + номер) и параметры ходов.
	sidBase    = 1000 // базовый номер сидов ходов/сессий
	sidModA    = 16   // размах сидов событий (сессии)
	sidBaseQ   = 2000 // базовый номер сидов очереди
	sidModQ    = 8    // размах сидов событий (очередь)
	turnDurDiv = 2    // длительность хода = nTurnDurSec / 2
	turnReqMod = 3    // число запросов = (i % 3) + 1
	nLeaseSlotB   = 2

	// Профили W7 (Устойчивость): внешние кодеры, здоровье узла, версия qwen.
	// X-rows: cron запускает qwen -p (обход) и runpilot exec (JOB в работе).
	xPIDCron   = 48211
	xPIDExec   = 48307
	xPIDKilled = 47120
	xUID       = 1000
	xStartSec  = 120 // секунд до старта (StartTime = now - xStartSec)
	// N9/N10: проблемы узла + расхождение часов (мс).
	wnNodeHost = "node-02"
	wnNodeIP   = "192.0.2.7"
	wnSkewMS   = 2500 // часы узла впереди на 2.5 с
	wnDiskFree = 900  // МБ (мало → disk_low)
	wnQwenVer  = "0.24.99"
	// C6/U3: версии qwen (untested = не в тестированном наборе координатора).
	vTested    = "0.24.4"
	vUntested  = "0.24.99"
	vUntested2 = "0.25.1"
	// X-rows: смещение start_time внешних процессов (секунды от xStartSec).
	xStartOffExec   = 10
	xStartOffKilled = 30
	// Сценарий снимка: суффикс после "~" (база~суффикс) → число частей SplitN.
	scenarioParts = 2
)
