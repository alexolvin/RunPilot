// Package texts — тексты для оператора (v2 раздел 3, 9.4, 15.1).
//
// Подписи состояний, классов, причин, событий и единиц. В интерфейс
// приходят только через GET /api/v1/meta (R2: тексты живут здесь, а
// соответствие кода токену цвета — единственное web/app/state-style.js).
// Технических значений (пороги, таймауты, пути) здесь нет.
package texts

import (
	"runpilot/internal/model"
)

// Enum — код + подпись. Цвета намеренно нет (см. state-style.js).
type Enum struct {
	Code  string `json:"code"`
	Label string `json:"label"`
}

// Data — ответ GET /api/v1/meta (v2 раздел 15.1).
type Data struct {
	Version     string            `json:"version"`
	Mode        string            `json:"mode"`
	Sessions    []Enum            `json:"sessions"`
	Panes       []Enum            `json:"panes"`
	Classes     []Enum            `json:"classes"`
	Servers     []Enum            `json:"servers"`
	Modes       []Enum            `json:"modes"`
	Severities  []Enum            `json:"severities"`
	HoldReasons map[string]string `json:"hold_reasons"`
	Ineligible  map[string]string `json:"ineligible"`
	Units       map[string]string `json:"units"`
}

// Meta собирает перечисления с подписями. version/mode — из вызывающего.
func Meta(version, mode string) *Data {
	return &Data{
		Version:     version,
		Mode:        mode,
		Sessions:    sessionLabels(),
		Panes:       paneLabels(),
		Classes:     classLabels(),
		Servers:     serverLabels(),
		Modes:       modeLabels(),
		Severities:  severityLabels(),
		HoldReasons: holdReasonText(),
		Ineligible:  ineligibleText(),
		Units:       unitLabels(),
	}
}

func sessionLabels() []Enum {
	return []Enum{
		{Code: string(model.SessionRunning), Label: "Выполняется"},
		{Code: string(model.SessionDispatching), Label: "Запуск"},
		{Code: string(model.SessionQueued), Label: "В очереди"},
		{Code: string(model.SessionDetached), Label: "Выполняется без слота"},
		{Code: string(model.SessionHold), Label: "Требует внимания"},
		{Code: string(model.SessionIdle), Label: "Свободна"},
		{Code: string(model.SessionGone), Label: "Закрыта"},
	}
}

func paneLabels() []Enum {
	return []Enum{
		{Code: string(model.PanePrompt), Label: "Ждёт разрешения"},
		{Code: string(model.PaneUnmanaged), Label: "Вне runpilot"},
		{Code: string(model.PaneIdle), Label: "Свободна"},
		{Code: string(model.PaneBusy), Label: "Выполняется"},
		{Code: string(model.PaneWaitUI), Label: "Служебный экран"},
		{Code: string(model.PaneUnknown), Label: "Экран не распознан"},
	}
}

func classLabels() []Enum {
	return []Enum{
		{Code: "resume", Label: "Продолжение"},
		{Code: "high", Label: "Высокий"},
		{Code: "normal", Label: "Обычный"},
		{Code: "low", Label: "Низкий"},
	}
}

// serverLabels — отображаемые состояния сервера (v2 раздел 3.2).
func serverLabels() []Enum {
	return []Enum{
		{Code: "UP", Label: "Доступен"},
		{Code: "DRAINING", Label: "Обслуживание"},
		{Code: "QUARANTINED", Label: "Карантин"},
		{Code: "DOWN", Label: "Недоступен"},
		{Code: "MODEL_PROBLEM", Label: "Модель не определена"},
		{Code: "KEY_MISSING", Label: "Нет ключа"},
		{Code: "DISABLED", Label: "Отключён"},
		{Code: "REMOVING", Label: "Удаляется"},
	}
}

func modeLabels() []Enum {
	return []Enum{
		{Code: "NORMAL", Label: "Работает"},
		{Code: "PAUSED", Label: "Пауза"},
		{Code: "EMERGENCY", Label: "Аварийная остановка"},
		{Code: "SAFE_MODE", Label: "Безопасный режим"},
	}
}

func severityLabels() []Enum {
	return []Enum{
		{Code: "CRIT", Label: "Критично"},
		{Code: "WARN", Label: "Внимание"},
		{Code: "INFO", Label: "Инфо"},
	}
}

func holdReasonText() map[string]string {
	return map[string]string{
		"EMPTY_INPUT":         "Ввод кодера пуст",
		"START_NOT_CONFIRMED": "Старт не подтверждён",
		"REQUEUE_LIMIT":       "Превышен лимит перепостановок",
		"PANE_UNKNOWN":        "Экран не распознан",
		"NODE_LOST":           "Нет связи с узлом",
		"NODE_DRAIN":          "Узел на обслуживании",
		"OPERATOR":            "Удержано оператором",
		"UPSTREAM_4XX":        "Ошибка апстрима (4xx)",
		"DEPENDENCY_FAILED":   "Зависимость недоступна",
		"PIN_UNAVAILABLE":     "Закреплённый сервер недоступен",
		"AGENT_EXITED":        "Кодер завершился",
		"EMERGENCY":           "Аварийная остановка",
	}
}

func ineligibleText() map[string]string {
	return map[string]string{
		model.ReasonPause:          "Очередь на паузе",
		model.ReasonNotBefore:      "Ждёт времени not_before",
		model.ReasonAfterWait:      "Ждёт хода зависимости",
		model.ReasonStaleSnapshot:  "Снимок панели устарел",
		model.ReasonNotIdle:        "Сессия не свободна",
		model.ReasonOperatorTyping: "Оператор печатает",
		model.ReasonWaitUI:         "Служебный экран кодера",
		model.ReasonPinMismatch:    "Не совпадает закрепление",
		model.ReasonPinDown:        "Закреплённый сервер недоступен",
		model.ReasonPreferWait:     "Ждём предпочитаемый сервер",
		model.ReasonAccept:         "Сервер не принимает класс",
		model.ReasonCooldown:       "Слоты в паузе",
		model.ReasonExternal:       "Внешняя нагрузка",
		model.ReasonNoUpServer:     "Нет доступного сервера",
		model.ReasonNodeDrain:      "Узел на обслуживании",
		model.ReasonHeldOK:         "Удержание запроса",
		model.ReasonEmergency:      "Координатор в аварийном режиме",
		model.ReasonSafeMode:       "Координатор в безопасном режиме",
		model.ReasonServerDisabled: "Подходящие серверы отключены или удаляются",
		model.ReasonServerQuarantine: "Подходящие серверы в карантине",
		model.ReasonModelProblem:   "У подходящих серверов модель не определена",
		model.ReasonKeyMissing:     "У подходящих серверов нет ключа в окружении",
	}
}

func unitLabels() map[string]string {
	return map[string]string{
		"sec": "с", "ms": "мс", "min": "мин",
		"gb": "ГБ", "mb": "МБ",
		"tok_s": "ток/с", "pct": "%",
		"count": "шт",
	}
}

// Label — подпись состояния сессии (для why.text и журналов).
func SessionLabel(s model.SessionState) string {
	for _, e := range sessionLabels() {
		if e.Code == string(s) {
			return e.Label
		}
	}
	return string(s)
}

// HoldLabel — текст причины удержания (кода hold_reason).
func HoldLabel(h model.HoldReason) string {
	if t, ok := holdReasonText()[string(h)]; ok {
		return t
	}
	return string(h)
}

// IneligibleLabel — текст ineligible_reason.
func IneligibleLabel(code string) string {
	if t, ok := ineligibleText()[code]; ok {
		return t
	}
	return code
}

// ServerLabel — подпись отображаемого состояния сервера (раздел 3.2).
func ServerLabel(code string) string {
	for _, e := range serverLabels() {
		if e.Code == code {
			return e.Label
		}
	}
	return code
}
