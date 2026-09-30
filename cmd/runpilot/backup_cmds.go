package main

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"runpilot/internal/backup"
)

// requireServiceStopped — 6.6: rollback/restore — только при остановленной
// службе (координатор не слушает api_port).
func requireServiceStopped() error {
	addr := net.JoinHostPort(loopbackIP, strconv.Itoa(cfg.Coordinator.APIPort))
	conn, err := net.DialTimeout("tcp", addr, time.Duration(serviceStopDialSec)*time.Second)
	if err == nil {
		_ = conn.Close()
		return fmt.Errorf("runpilot: служба запущена — сначала systemctl --user stop runpilot-serve")
	}
	return nil
}

// runpilotPrevPath — путь runpilot.prev рядом с текущим бинарником (раздел 6.6).
func runpilotPrevPath() (string, error) {
	cur, err := os.Executable()
	if err != nil {
		return "", err
	}
	if p, err := filepath.EvalSymlinks(cur); err == nil {
		cur = p
	}
	return filepath.Join(filepath.Dir(cur), "runpilot.prev"), nil
}

// newRollbackCmd — `runpilot rollback` (раздел 6.6, U2): при остановленной службе
// вернуть runpilot.prev и копию БД, сделанную перед последней миграцией.
func newRollbackCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "rollback",
		Short: "Откат: вернуть runpilot.prev и копию БД до последней миграции (6.6)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireServiceStopped(); err != nil {
				return err
			}
			prev, err := runpilotPrevPath()
			if err != nil {
				return err
			}
			cur, err := os.Executable()
			if err != nil {
				return err
			}
			if p, err := filepath.EvalSymlinks(cur); err == nil {
				cur = p
			}
			dbPath, err := cfg.DBPath()
			if err != nil {
				return err
			}
			bin, dbBackup, err := backup.Rollback(prev, cur, dbPath)
			if err != nil {
				return err
			}
			if jsonOut {
				return printJSON(map[string]any{"binary": bin, "db_backup": dbBackup})
			}
			fmt.Printf("Восстановлено:\n  бинарник: %s (из %s)\n  БД:        %s (из %s)\n",
				cur, prev, dbPath, dbBackup)
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "машиночитаемый вывод")
	return cmd
}

// newRestoreCmd — `runpilot restore <файл-копии>` (раздел 6.6): при остановленной
// службе заменить БД указанной копией после quick_check.
func newRestoreCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "restore <файл-копии>",
		Short: "Заменить БД указанной копией после quick_check (6.6)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireServiceStopped(); err != nil {
				return err
			}
			dbPath, err := cfg.DBPath()
			if err != nil {
				return err
			}
			if err := backup.Restore(args[0], dbPath); err != nil {
				return err
			}
			if jsonOut {
				return printJSON(map[string]any{"db": dbPath, "from": args[0]})
			}
			fmt.Printf("БД восстановлена: %s (из %s)\n", dbPath, args[0])
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "машиночитаемый вывод")
	return cmd
}
