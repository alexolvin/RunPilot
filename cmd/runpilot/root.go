package main

import (
	"github.com/spf13/cobra"

	"runpilot/internal/config"
)

var (
	cfg          *config.Config
	resolvedPath string
)

func newRoot(version string) *cobra.Command {
	var cfgPath string
	root := &cobra.Command{
		Use:           "runpilot",
		Short:         "RunPilot: очередь Qwen Code-кодеров в tmux по слотам локальных AI-серверов",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version,
		// Без аргументов — сводка: режим, ходы, очередь, URL веба (v2 раздел 1).
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSummary()
		},
	}
	root.PersistentFlags().StringVar(&cfgPath, "config", "",
		"путь к конфигурации (по умолчанию ~/.config/runpilot/config.yaml)")
	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		// selftest — самопроверка только что скачанного бинарника (14.2):
		// на чистой машине конфига ещё нет, поэтому загрузку пропускаем.
		if cmd.Name() == "selftest" {
			return nil
		}
		if cfgPath == "" {
			p, err := config.DefaultPath()
			if err != nil {
				return err
			}
			cfgPath = p
		}
		c, err := config.Load(cfgPath)
		if err != nil {
			return err
		}
		cfg = c
		resolvedPath = cfgPath
		return nil
	}
	root.AddCommand(
		newServeCmd(),
		newNodeCmd(),
		newRunCmd(),
		newExecCmd(),
		newTmuxSetupCmd(),
		newDoctorCmd(),
		newVersionCmd(version),
		newFixtureCmd(),
		newLsCmd(),
		newQCmd(),
		newWhyCmd(),
		newServersCmd(),
		newPeekCmd(),
		newAttachCmd(),
		newServerCmd(),
		newPauseCmd(),
		newResumeCmd(),
		newStatsCmd(),
		newEventsCmd(),
		newServiceCmd(),
		newSelftestCmd(),
		newRollbackCmd(),
		newRestoreCmd(),
		newUpgradeCmd(),
	)
	root.AddCommand(newQueueCmds()...)
	return root
}
