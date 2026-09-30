package model

// Action — доступное действие сущности в ответе API (v2 раздел 15.2).
// Веб строит кнопки и меню «⋮» только из этого поля; доступность вычисляет
// сервер тем же кодом, что и при выполнении действия.
type Action struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
	Reason  string `json:"reason,omitempty"`
}

// a — конструктор Action: причина заполняется только при недоступности.
func a(id string, enabled bool, reasonWhenDisabled string) Action {
	if enabled {
		return Action{ID: id, Enabled: true}
	}
	return Action{ID: id, Enabled: false, Reason: reasonWhenDisabled}
}

// SessionActions — действия сессии по её состоянию (v2 15.2; W1 — базовый
// набор, полный набор действий сессии — W5/W6).
func SessionActions(st SessionState) []Action {
	inProgress := st == SessionDispatching || st == SessionRunning || st == SessionDetached
	queued := st == SessionQueued
	return []Action{
		a("enqueue", st == SessionIdle || st == SessionHold, "уже в очереди или идёт ход"),
		a("dequeue", queued, "сессия не в очереди"),
		a("cancel", inProgress, "нет идущего хода"),
		a("hold", st == SessionIdle, "доступно только в ожидании"),
		a("release", st == SessionHold, "сессия не удержана"),
		a("close", st != SessionGone, "сессия уже закрыта"),
	}
}

// ServerActions — действия сервера по его состоянию (v2 15.2).
func ServerActions(st ServerState) []Action {
	return []Action{
		a("drain", st == ServerUp, "сервер не в выдаче"),
		a("restore", st == ServerDraining, "сервер уже в выдаче"),
	}
}

// NodeActions — действия узла (v2 15.2): узел зарегистрирован — его можно
// выводить из выдачи (drain).
func NodeActions() []Action {
	return []Action{a("drain", true, "")}
}

// QueueActions — действия записи очереди (v2 15.2): запись можно снять с
// очереди.
func QueueActions() []Action {
	return []Action{a("dequeue", true, "")}
}
