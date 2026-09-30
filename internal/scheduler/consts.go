package scheduler

// Числовые константы планировщика (R2: литералы только в consts.go).
const (
	// signalCodeBase — в exit-коде > signalCodeBase число означает сигнал
	// (номер = код − signalCodeBase); 7.3 C1.
	signalCodeBase = 128
	// sigKill — SIGKILL (OOM-killer); 7.3 C1.
	sigKill = 9
	// secPerMinute — секунд в минуте (уведомление «ждёт в очереди N мин»).
	secPerMinute = 60
)
