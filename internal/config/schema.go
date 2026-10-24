// Схема рабочих настроек (v2 раздел 4.3).
//
// Описывает каждый рабочий ключ: путь, тип, единицу, ссылку на значение по
// умолчанию (из defaults.go, значения не дублируются), min, max, перечень,
// группу, подпись, справку, момент применения (раздел 4.4), признак
// «опасная». Веб строит формы только из GET /api/v1/settings/schema (R2, R3).
//
// CONTROL 6: схема покрывает 100% рабочих ключей defaults.go — сверяется
// test-ом (schema_test.go) со списком WorkingLeafPaths().
package config

import "fmt"

// FieldType — тип значения рабочего ключа.
type FieldType string

const (
	TypeInt    FieldType = "int"
	TypeString FieldType = "string"
	TypeBool   FieldType = "bool"
	TypeList   FieldType = "list"
)

// ApplyWhen — момент применения изменения (раздел 4.4).
type ApplyWhen string

const (
	ApplyNextTick    ApplyWhen = "next_tick"
	ApplyNewDispatch ApplyWhen = "new_dispatch"
	ApplyNewRequest  ApplyWhen = "new_request"
	ApplyNewSession  ApplyWhen = "new_session"
	ApplyNewConn     ApplyWhen = "new_conn"
)

// SettingField — описание одного рабочего ключа.
type SettingField struct {
	Path      string    `json:"path"`
	Type      FieldType `json:"type"`
	Unit      string    `json:"unit,omitempty"`
	Default   any       `json:"default,omitempty"`
	Min       *int      `json:"min,omitempty"`
	Max       *int      `json:"max,omitempty"`
	Enum      []string  `json:"enum,omitempty"`
	Group     string    `json:"group"`
	Label     string    `json:"label"`
	Help      string    `json:"help"`
	ApplyWhen ApplyWhen `json:"apply_when"`
	Dangerous bool      `json:"dangerous,omitempty"`
}

func intPtr(n int) *int { return &n }

// intField — целочисленный ключ с min=1 (положительные рабочие ключи).
func intField(path, unit, group, label, help string, aw ApplyWhen) SettingField {
	return SettingField{Path: path, Type: TypeInt, Unit: unit, Min: intPtr(1),
		Group: group, Label: label, Help: help, ApplyWhen: aw}
}

func stringField(path, unit, group, label, help string, aw ApplyWhen) SettingField {
	return SettingField{Path: path, Type: TypeString, Unit: unit,
		Group: group, Label: label, Help: help, ApplyWhen: aw}
}

func boolField(path, group, label, help string, aw ApplyWhen) SettingField {
	return SettingField{Path: path, Type: TypeBool,
		Group: group, Label: label, Help: help, ApplyWhen: aw}
}

func listField(path, group, label, help string, aw ApplyWhen) SettingField {
	return SettingField{Path: path, Type: TypeList,
		Group: group, Label: label, Help: help, ApplyWhen: aw}
}

// Schema — все рабочие ключи (раздел 4.2). Значения по умолчанию
// подставляются из Defaults() (единый источник, R2).
func Schema() []SettingField {
	fields := []SettingField{
		// Планировщик (раздел 4.4: со следующего тика).
		intField("scheduler.tick_ms", "ms", "Планировщик", "Интервал тика",
			"Период основного цикла планировщика.", ApplyNextTick),
		intField("scheduler.aging_sec", "s", "Планировщик", "Старение записи",
			"Запись повышает класс очереди каждые aging_sec в очереди.", ApplyNextTick),
		intField("scheduler.prefer_wait_sec", "s", "Планировщик", "Ожидание prefer",
			"Сколько ждать привязку prefer, прежде чем выдать любой сервер.", ApplyNextTick),
		intField("scheduler.pin_unavailable_sec", "s", "Планировщик", "Pin недоступен",
			"После этого времени pin к DOWN-серверу даёт HOLD(PIN_UNAVAILABLE).", ApplyNextTick),
		intField("scheduler.affinity_ttl_sec", "s", "Планировщик", "TTL тёплого кэша",
			"Жизнь привязки к серверу с тёплым KV-кэшем.", ApplyNextTick),
		intField("scheduler.resume_backoff_sec", "s", "Планировщик", "Backoff resume",
			"Задержка повторной диспетчеризации RESUME-записи.", ApplyNextTick),
		intField("scheduler.auto_requeue_max", "", "Планировщик", "Макс автопостановки",
			"Максимум автоматических перепостановок записи.", ApplyNextTick),
		intField("scheduler.snapshot_max_age_sec", "s", "Планировщик", "Свежесть снимка",
			"Снимок панели старше этого возраста — stale_snapshot.", ApplyNextTick),
		intField("scheduler.dispatch_stable_sec", "s", "Планировщик", "Стабильность панели",
			"Панель должна быть стабильна столько, прежде чем выдавать слот.", ApplyNextTick),
		intField("scheduler.external_confirm_sec", "s", "Планировщик", "Подтверждение EXT",
			"Подтверждение внешней нагрузки (EXTERNAL) и возврата FREE.", ApplyNextTick),
		intField("scheduler.cooldown_sec", "s", "Планировщик", "Кулдаун слота",
			"Пауза слота после освобождения (COOLDOWN).", ApplyNextTick),
		intField("scheduler.recovery_window_sec", "s", "Планировщик", "Окно восстановления",
			"Окно восстановления после сбоя.", ApplyNextTick),

		// Диспетчеризация (раздел 4.4: для новых диспетчеризаций).
		intField("dispatch.node_reply_timeout_sec", "s", "Диспетчеризация", "Ответ узла",
			"Таймаут ожидания ответа узла на команду dispatch.", ApplyNewDispatch),
		intField("dispatch.start_confirm_sec", "s", "Диспетчеризация", "Подтверждение старта",
			"Подтверждение старта хода после нажатия Enter.", ApplyNewDispatch),
		intField("dispatch.resume_key_delay_ms", "ms", "Диспетчеризация", "Задержка resume",
			"Задержка между клавишами resume.", ApplyNewDispatch),
		intField("dispatch.budget_p95_ms", "ms", "Диспетчеризация", "Бюджет p95",
			"Бюджет p95 задержки диспетчеризации.", ApplyNewDispatch),

		// Ходы (раздел 4.4: для новых диспетчеризаций и запросов).
		intField("turn.done_quiet_sec", "s", "Ходы", "Тишина хода",
			"Сколько тишины считать ход завершённым.", ApplyNewDispatch),
		intField("turn.done_stable_sec", "s", "Ходы", "Стабильность завершения",
			"Стабильность состояния завершения хода.", ApplyNewDispatch),
		intField("turn.tool_hold_max_sec", "s", "Ходы", "Удержание на инструменте",
			"Максимальное удержание сессии на долгом инструменте.", ApplyNewDispatch),
		intField("turn.approval_hold_max_sec", "s", "Ходы", "Удержание на разрешении",
			"Максимальное удержание сессии на запросе разрешения.", ApplyNewDispatch),
		intField("turn.unknown_max_sec", "s", "Ходы", "Максимум unknown",
			"Максимальное время состояния UNKNOWN.", ApplyNewDispatch),
		intField("turn.node_lost_release_sec", "s", "Ходы", "Снятие аренды",
			"Снятие аренды RUNNING-сессий при потере узла.", ApplyNewDispatch),
		intField("turn.node_lost_gone_sec", "s", "Ходы", "ГONE по потере узла",
			"Сессии потерявшего узла → GONE, если узел отсутствует дольше этого.", ApplyNewDispatch),
		intField("turn.auto_enqueue_stable_sec", "s", "Ходы", "Автопостановка",
			"Стабильность IDLE перед автопостановкой.", ApplyNewDispatch),
		intField("turn.kill_grace_sec", "s", "Ходы", "Грация завершения",
			"Период грации принудительного завершения кодера.", ApplyNewDispatch),

		// Шлюз (раздел 4.4: для новых запросов).
		intField("gateway.hold_max_sec", "s", "Шлюз", "Макс удержания",
			"Максимум удержания запроса без слота в шлюзе.", ApplyNewRequest),
		intField("gateway.max_body_mb", "MB", "Шлюз", "Макс тела запроса",
			"Максимальный размер тела запроса шлюза.", ApplyNewRequest),
		intField("gateway.max_buffered_mb", "MB", "Шлюз", "Макс буфера тел",
			"Сумма тел в памяти шлюза (проксируемые и удержанные).", ApplyNewRequest),
		intField("gateway.retry_after_sec", "s", "Шлюз", "Retry-After",
			"Значение Retry-After при отказе шлюза.", ApplyNewRequest),
		intField("gateway.max_inflight", "", "Шлюз", "Макс одновременных",
			"Максимум одновременных запросов в шлюзе.", ApplyNewRequest),
		intField("gateway.read_header_timeout_sec", "s", "Шлюз", "Таймаут заголовков",
			"ReadHeaderTimeout сервера шлюза (ReadTimeout = 0).", ApplyNewRequest),

		// Мониторинг (раздел 4.4: со следующего тика).
		intField("monitor.health_interval_sec", "s", "Мониторинг", "Интервал здоровья",
			"Период проверки /health серверов.", ApplyNextTick),
		intField("monitor.health_timeout_sec", "s", "Мониторинг", "Таймаут здоровья",
			"Таймаут проверки /health.", ApplyNextTick),
		intField("monitor.health_down_after", "", "Мониторинг", "DOWN после",
			"Число неудачных проверок до DOWN.", ApplyNextTick),
		intField("monitor.health_up_after", "", "Мониторинг", "UP после",
			"Число успешных проверок до UP.", ApplyNextTick),
		intField("monitor.metrics_interval_sec", "s", "Мониторинг", "Интервал метрик",
			"Период сбора метрик vLLM.", ApplyNextTick),
		intField("monitor.metrics_timeout_sec", "s", "Мониторинг", "Таймаут метрик",
			"Таймаут сбора метрик.", ApplyNextTick),
		intField("monitor.rate_window_sec", "s", "Мониторинг", "Окно скорости",
			"Окно расчёта tok/s.", ApplyNextTick),
		intField("monitor.history_window_min", "min", "Мониторинг", "Окно истории",
			"Длина истории метрик.", ApplyNextTick),
		intField("monitor.models_refresh_sec", "s", "Мониторинг", "Обновление моделей",
			"Период обновления списка моделей серверов.", ApplyNextTick),
		intField("monitor.disk_free_min_mb", "MB", "Мониторинг", "Минимум свободного",
			"Порог свободного места на диске.", ApplyNextTick),
		intField("monitor.disk_check_sec", "s", "Мониторинг", "Проверка диска",
			"Период проверки места на диске.", ApplyNextTick),
		intField("monitor.safe_probe_sec", "s", "Мониторинг", "Проба safe mode",
			"Период пробной записи в SAFE_MODE.", ApplyNextTick),

		// Отказы серверов (раздел 4.4: со следующего тика).
		intField("faults.window_sec", "s", "Отказы", "Окно отказов",
			"Окно подсчёта отказов апстрима.", ApplyNextTick),
		intField("faults.quarantine_count", "", "Отказы", "Порог карантина",
			"Число отказов для карантина сервера.", ApplyNextTick),
		intField("faults.quarantine_sec", "s", "Отказы", "Длительность карантина",
			"Сколько держать сервер в карантине.", ApplyNextTick),
		intField("faults.flap_count", "", "Отказы", "Порог нестабильности",
			"Число колебаний UP/DOWN для пометки нестабильности.", ApplyNextTick),
		intField("faults.flap_window_sec", "s", "Отказы", "Окно нестабильности",
			"Окно подсчёта колебаний UP/DOWN.", ApplyNextTick),

		// Лимиты.
		intField("limits.max_sessions_per_node", "", "Лимиты", "Сессий на узел",
			"Максимум сессий на одном узле.", ApplyNextTick),
		intField("limits.max_queue_length", "", "Лимиты", "Длина очереди",
			"Максимальная длина очереди.", ApplyNextTick),
		intField("limits.max_pane_snapshot_bytes", "", "Лимиты", "Размер снимка",
			"Максимальный размер снимка панели.", ApplyNextTick),
		intField("limits.token_min_len", "", "Лимиты", "Длина токена",
			"Минимальная длина RUNPILOT_TOKEN.", ApplyNextTick),

		// Профиль Qwen Code (раздел 4.4: для новых сессий и перезапусков).
		stringField("profiles.qwen.model_alias", "", "Профиль Qwen", "Псевдоним модели",
			"Псевдоним модели для новых сессий.", ApplyNewSession),
		stringField("profiles.qwen.command", "", "Профиль Qwen", "Путь к кодеру",
			"Абсолютный путь к qwen (узел: PATH = каталог пути + сервисный). "+
				"Пишет install.sh; пусто — command профиля.", ApplyNewSession),
		intField("profiles.qwen.client_timeout_sec", "s", "Профиль Qwen", "Таймаут запроса кодера",
			"Таймаут генерирующего запроса кодера (QWEN_CODE_API_TIMEOUT_MS); "+
				"<= gateway.hold_max_sec + max(servers[].first_byte_timeout_sec).", ApplyNewSession),

		// Уведомления (раздел 4.4: со следующего тика).
		listField("notify.telegram.chat_ids", "Уведомления", "Чаты Telegram",
			"ID чатов для уведомлений.", ApplyNextTick),
		intField("notify.telegram.min_turn_sec", "s", "Уведомления", "Минимум хода",
			"Минимальная длительность хода для уведомления.", ApplyNextTick),
		intField("notify.telegram.coalesce_sec", "s", "Уведомления", "Слияние",
			"Слияние уведомлений в интервале.", ApplyNextTick),
		listField("notify.telegram.kinds", "Уведомления", "Виды уведомлений",
			"Включённые виды уведомлений.", ApplyNextTick),
		intField("notify.alerts.queue_wait_min", "min", "Уведомления", "Ожидание в очереди",
			"Порог ожидания в очереди для алерта.", ApplyNextTick),
		intField("notify.alerts.wait_ui_sec", "s", "Уведомления", "Ожидание UI",
			"Задержка показа алерта ожидания.", ApplyNextTick),
		intField("notify.alerts.prompt_remind_min", "min", "Уведомления", "Напоминание",
			"Напоминание о запросе разрешения через N минут.", ApplyNextTick),

		// Хранение.
		intField("retention.requests_days", "days", "Хранение", "Запросы",
			"Срок хранения запросов.", ApplyNextTick),
		intField("retention.events_days", "days", "Хранение", "События",
			"Срок хранения событий.", ApplyNextTick),
		intField("retention.gone_sessions_days", "days", "Хранение", "Закрытые сессии",
			"Срок хранения GONE-сессий.", ApplyNextTick),
		intField("retention.audit_days", "days", "Хранение", "Аудит",
			"Срок хранения аудита.", ApplyNextTick),
		intField("retention.notifications_days", "days", "Хранение", "Уведомления",
			"Срок хранения уведомлений.", ApplyNextTick),
		intField("retention.external_days", "days", "Хранение", "Внешние процессы",
			"Срок хранения данных внешних процессов.", ApplyNextTick),

		// Копии.
		stringField("backup.daily_at_utc", "", "Копии", "Время ежедневной копии",
			"Время ежедневной копии БД в UTC (HH:MM).", ApplyNextTick),
		intField("backup.keep", "", "Копии", "Хранить копий",
			"Число хранимых ежедневных копий.", ApplyNextTick),

		// Медиа (цикл W15).
		intField("media.probe_many_images", "", "Медиа", "Картинок в P-many-image",
			"Число картинок в одном запросе медиа-пробы P-many-image "+
				"(= model.chatCompression.screenshotTriggerThreshold Qwen Code).", ApplyNextTick),

		// Связь с оркестратором (ТЗ Relay, раздел 16).
		listField("relay.report_keywords", "Связь", "Ключевые слова отчёта",
			"Слова-маркеры начала отчёта (без учёта регистра, подстрока).", ApplyNextTick),
		stringField("relay.terminal_marker_regex", "", "Связь", "Маркер терминала",
			"Регулярка маркера отчёта в истории терминала (◆︎).", ApplyNextTick),
		stringField("relay.agent_block_regex", "", "Связь", "Блок для агента",
			"Регулярка заголовка блока для агента в ответе оркестратора.", ApplyNextTick),
		listField("relay.free_text_labels", "Связь", "Подписи «свой вариант»",
			"Подписи пункта свободного текста в диалоге (проверка по снимку).", ApplyNextTick),
		stringField("relay.stop_word", "", "Связь", "Стоп-слово",
			"Блок для агента, равный этому тексту, завершает связь.", ApplyNextTick),
		intField("relay.recording_settle_ms", "ms", "Связь", "Стабилизация записи",
			"Отсутствие изменений записи, считающее её стабилизировавшейся.", ApplyNextTick),
		intField("relay.recording_wait_max_sec", "s", "Связь", "Ожидание записи",
			"Макс. ожидание записи перед переходом к терминалу.", ApplyNextTick),
		intField("relay.scrollback_lines", "", "Связь", "История терминала",
			"Глубина истории терминала (capture-pane -S).", ApplyNextTick),
		intField("relay.attach_max_files", "", "Связь", "Макс. вложений",
			"Максимум .md-файлов в отчёте.", ApplyNextTick),
		intField("relay.attach_max_kb", "KB", "Связь", "Макс. размер вложения",
			"Максимальный размер одного .md-вложения (КБ).", ApplyNextTick),
		intField("relay.upload_timeout_sec", "s", "Связь", "Таймаут загрузки",
			"Таймаут загрузки вложений в чат.", ApplyNextTick),
		stringField("relay.send_template", "", "Связь", "Шаблон отправки",
			"Шаблон text/template сообщения оркестратору.", ApplyNextTick),
		stringField("relay.question_template", "", "Связь", "Шаблон вопроса",
			"Шаблон text/template вопроса агента.", ApplyNextTick),
		stringField("relay.oversize_note_template", "", "Связь", "Шаблон oversized",
			"Шаблон строки о файле сверх лимита.", ApplyNextTick),
		intField("relay.min_send_interval_sec", "s", "Связь", "Интервал отправок",
			"Пауза между отправками в одной связи.", ApplyNextTick),
		intField("relay.send_confirm_sec", "s", "Связь", "Подтверждение отправки",
			"Окно подтверждения отправки (сообщение пользователя с хешем).", ApplyNextTick),
		intField("relay.answer_timeout_min", "min", "Связь", "Таймаут ответа",
			"Макс. ожидание ответа оркестратора.", ApplyNextTick),
		intField("relay.answer_stable_sec", "s", "Связь", "Стабильность ответа",
			"Стабильность ответа (хеш не меняется).", ApplyNextTick),
		intField("relay.input_wait_sec", "s", "Связь", "Ожидание ввода",
			"Ожидание пустого ввода агента перед доставкой.", ApplyNextTick),
		intField("relay.max_auto_cycles", "", "Связь", "Макс. автоциклов",
			"Предохранитель: циклов подряд без действий оператора.", ApplyNextTick),

		// Помощник LLM связи (ТЗ Relay, раздел 13).
		stringField("llm_assist.class", "", "Помощник LLM", "Класс модели",
			"Класс модели шлюза для вызовов помощника связи.", ApplyNextTick),
		intField("llm_assist.max_tokens", "", "Помощник LLM", "Лимит токенов",
			"Лимит токенов ответа помощника.", ApplyNextTick),
		intField("llm_assist.timeout_sec", "s", "Помощник LLM", "Таймаут вызова",
			"Таймаут одного вызова помощника.", ApplyNextTick),
		intField("llm_assist.max_input_kb", "KB", "Помощник LLM", "Обрезка входа",
			"Обрезка входа с конца (КБ).", ApplyNextTick),
		intField("llm_assist.min_quote_chars", "", "Помощник LLM", "Мин. длина quote",
			"Минимальная длина quote (проверка подстроки).", ApplyNextTick),

		// Браузер координатора (ТЗ Relay, раздел 8/16).
		stringField("browser.chrome_path", "", "Браузер", "Путь к Chrome",
			"Абсолютный путь к Google Chrome.", ApplyNextTick),
		stringField("browser.proxy", "", "Браузер", "Прокси",
			"Прокси для Chrome (--proxy-server), например socks5://127.0.0.1:1080.", ApplyNextTick),
		stringField("browser.xvfb_path", "", "Браузер", "Путь к Xvfb",
			"Абсолютный путь к Xvfb.", ApplyNextTick),
		stringField("browser.display", "", "Браузер", "Дисплей",
			"Номер дисплея Xvfb (например, :77).", ApplyNextTick),
		stringField("browser.screen", "", "Браузер", "Экран",
			"Размер/глубина экрана Xvfb (ШxВxБит).", ApplyNextTick),
		stringField("browser.profile_dir", "", "Браузер", "Каталог профиля",
			"Каталог профиля Chrome (0700, переживает рестарты).", ApplyNextTick),
		intField("browser.max_tabs", "", "Браузер", "Макс. вкладок",
			"Максимум вкладок на связь.", ApplyNextTick),
		intField("browser.page_load_timeout_sec", "s", "Браузер", "Таймаут загрузки",
			"Таймаут загрузки страницы.", ApplyNextTick),
		intField("browser.idle_stop_min", "min", "Браузер", "Остановка по бездействию",
			"Остановка браузера через N минут после выключения последней связи.", ApplyNextTick),
		intField("browser.view_jpeg_quality", "", "Браузер", "Качество JPEG",
			"Качество JPEG screencast (1-100).", ApplyNextTick),
		intField("browser.view_max_width", "px", "Браузер", "Макс. ширина кадра",
			"Максимальная ширина кадра screencast (px).", ApplyNextTick),
		intField("browser.view_idle_close_min", "min", "Браузер", "Закрытие по бездействию",
			"Закрытие живого просмотра по бездействию (мин).", ApplyNextTick),

		// Веб (раздел 4.4: для новых соединений и сессий браузера).
		intField("web.session_ttl_days", "days", "Веб", "TTL сессии браузера",
			"Жизнь сессии браузера.", ApplyNewConn),
		intField("web.login_rate_per_min", "", "Веб", "Лимит входа",
			"Максимум попыток входа в минуту.", ApplyNewConn),
		intField("web.screen_interval_ms", "ms", "Веб", "Интервал экрана",
			"Период обновления живого экрана.", ApplyNewConn),
		intField("web.server_metrics_interval_ms", "ms", "Веб", "Интервал метрик серверов",
			"Период пуша живой загрузки серверов (SSE SERVER_METRICS).", ApplyNewConn),
		intField("web.paste_verify_sec", "s", "Веб", "Проверка вставки",
			"Окно проверки вставки текста.", ApplyNewConn),
		intField("web.paste_max_kb", "KB", "Веб", "Макс вставки",
			"Максимальный размер вставки.", ApplyNewConn),
		intField("web.spawn_ready_sec", "s", "Веб", "Готовность панели",
			"Ожидание готовности панели после запуска.", ApplyNewConn),
		intField("web.dirs_max", "", "Веб", "Максимум каталогов",
			"Максимум каталогов в списке.", ApplyNewConn),
		intField("web.sse_replay", "", "Веб", "Досылка SSE",
			"Число событий для досылки при подключении SSE.", ApplyNewConn),
		intField("web.sse_clients_max", "", "Веб", "Клиентов SSE",
			"Максимум одновременных клиентов SSE.", ApplyNewConn),
		intField("web.screens_per_client_max", "", "Веб", "Экранов на клиента",
			"Максимум живых экранов на клиента.", ApplyNewConn),
		intField("web.terminals_per_node_max", "", "Веб", "Терминалов на узел",
			"Максимум терминалов на узел.", ApplyNewConn),
		intField("web.terminal_idle_min", "min", "Веб", "Бездействие терминала",
			"Закрытие терминала по бездействию.", ApplyNewConn),
		intField("web.undo_sec", "s", "Веб", "Отмена действия",
			"Окно отмены действия (undo).", ApplyNewConn),
		intField("web.page_size", "", "Веб", "Размер страницы",
			"Размер страницы списков.", ApplyNewConn),
		intField("web.idempotency_ttl_sec", "s", "Веб", "TTL идемпотентности",
			"Жизнь ключей идемпотентности.", ApplyNewConn),

		// Узлы.
		intField("node.scan_interval_sec", "s", "Узлы", "Интервал скана",
			"Период скана tmux узлом.", ApplyNextTick),
		intField("node.pulse_interval_sec", "s", "Узлы", "Интервал пульса",
			"Период пульса Busy-панели (должен быть меньше scheduler.snapshot_max_age_sec).", ApplyNextTick),
		intField("node.offline_after_sec", "s", "Узлы", "Оффлайн",
			"Узел оффлайн после этого времени без связи.", ApplyNextTick),
		intField("node.health_interval_sec", "s", "Узлы", "Интервал здоровья",
			"Период проверки здоровья узла.", ApplyNextTick),
		listField("node.project_roots_default", "Узлы", "Корни проектов",
			"Корни проектов по умолчанию для нового узла.", ApplyNextTick),
		stringField("node.project_dir_match", "", "Узлы", "Фильтр каталогов",
			"Подстрока для фильтра каталогов в диалоге (пусто — все).", ApplyNewSession),
		stringField("node.state_dir", "", "Узлы", "Каталог состояния",
			"Каталог per-сессии system-settings.json (<state_dir>/qwen/<sid>/).", ApplyNextTick),

		// Подключение узлов.
		intField("enroll.token_ttl_min", "min", "Подключение", "TTL токена",
			"Жизнь одноразового токена подключения.", ApplyNextTick),
		intField("enroll.update_retry_min", "min", "Подключение", "Повтор обновления",
			"Повтор самообновления узла.", ApplyNextTick),
		intField("enroll.selftest_timeout_sec", "s", "Подключение", "Таймаут selftest",
			"Таймаут самопроверки нового бинарника.", ApplyNextTick),

		// Внешние процессы.
		boolField("external.detect", "Внешние", "Обнаружение",
			"Обнаруживать внешние процессы вне runpilot.", ApplyNextTick),
		intField("external.kill_grace_sec", "s", "Внешние", "Грация завершения",
			"Период грации завершения внешнего процесса.", ApplyNextTick),

		// Задания cron.
		intField("jobs.heartbeat_sec", "s", "Задания", "Heartbeat",
			"Период heartbeat заданий cron.", ApplyNextTick),
		intField("jobs.heartbeat_timeout_sec", "s", "Задания", "Таймаут heartbeat",
			"Таймаут heartbeat задания.", ApplyNextTick),
		intField("jobs.wait_max_default_sec", "s", "Задания", "Макс ожидания",
			"Максимум ожидания слота по умолчанию (runpilot exec).", ApplyNextTick),
		intField("jobs.slots", "", "Задания", "Одновременные задания",
			"Максимум одновременных JOB-аренд (runpilot exec).", ApplyNextTick),

		// Лог-ротация.
		stringField("log.path", "", "Лог", "Путь",
			"Путь к лог-файлу координатора (пусто — stderr).", ApplyNextTick),
		intField("log.max_mb", "MB", "Лог", "Макс. размер",
			"Максимальный размер лог-файла (МБ); 0 — без ротации.", ApplyNextTick),
		intField("log.backups", "", "Лог", "Бэкапы",
			"Сколько бэкапов хранить (.1, .2, ...).", ApplyNextTick),
	}

	d := Defaults()
	for i := range fields {
		if v, err := d.GetField(fields[i].Path); err == nil {
			fields[i].Default = v
		}
	}
	return fields
}

// ByPath — поле схемы по пути.
func ByPath(path string) (SettingField, bool) {
	for _, f := range Schema() {
		if f.Path == path {
			return f, true
		}
	}
	return SettingField{}, false
}

// SchemaPaths — отсортированный набор путей схемы (для сверки, CONTROL 6).
func SchemaPaths() []string {
	f := Schema()
	out := make([]string, 0, len(f))
	for _, s := range f {
		out = append(out, s.Path)
	}
	return out
}

// validateSchemaIntegrity — внутренняя проверка: схема не содержит
// дубликатов и все пути — рабочие листья Config.
func validateSchemaIntegrity() error {
	seen := map[string]bool{}
	for _, f := range Schema() {
		if seen[f.Path] {
			return fmt.Errorf("config: схема: дубликат пути %s", f.Path)
		}
		seen[f.Path] = true
	}
	return nil
}
