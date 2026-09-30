package main

import (
	"github.com/spf13/cobra"
)

// newSelftestCmd — `runpilot selftest` (раздел 14.2 ТЗ): быстрая самопроверка
// целостности бинарника перед self-update. Вызывается узлом у только что
// скачанного runpilot.new; при успехе — exit 0, при неудаче бинарник не ставится.
// Не требует конфига: на чистой машине ~/.config/runpilot/config.yaml может быть
// ещё не создан (PersistentPreRunE пропускает загрузку для selftest).
func newSelftestCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "selftest",
		Short: "Быстрая самопроверка бинарника (для self-update)",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Минимальная проверка жизнеспособности процесса и инициализации
			// runtime. Глубокая проверка (tmux, сеть) — `runpilot doctor`, здесь не
			// нужна: selftest отвечает «бинарник цел и исполним».
			return nil
		},
	}
}
