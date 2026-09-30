package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"
)

// newUpgradeCmd — `runpilot upgrade <каталог dist>` (раздел 6.6, U1): самопроверка
// нового бинарника, сохранение текущего как runpilot.prev, установка, копирование
// бинарников целей в coordinator.dist_dir, перезапуск службы.
func newUpgradeCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "upgrade <каталог dist>",
		Short: "Обновление координатора: selftest, runpilot.prev, установка, dist (6.6)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cur, err := os.Executable()
			if err != nil {
				return err
			}
			if p, err := filepath.EvalSymlinks(cur); err == nil {
				cur = p
			}
			out, err := runUpgrade(args[0], cur, cfg.Coordinator.DistDir)
			if err != nil {
				return err
			}
			out["restart"] = "systemctl --user restart runpilot-serve"
			if jsonOut {
				return printJSON(out)
			}
			fmt.Printf("Обновлено: %s (prev: %s)\nПерезапуск: systemctl --user restart runpilot-serve\n",
				out["installed"], out["prev"])
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "машиночитаемый вывод")
	return cmd
}

// runpilotPrevNameIn — путь runpilot.prev в каталоге (рядом с бинарником).
func runpilotPrevNameIn(dir string) string {
	return filepath.Join(dir, "runpilot.prev")
}

// runUpgrade — ядро `runpilot upgrade` (раздел 6.6, U1): selftest нового
// бинарника из каталога dist, сохранение текущего как runpilot.prev, установка
// нового поверх cur, копирование бинарников целей в d (узлы скачают сами).
// Указывает путь перезапуска — фактический рестарт делает менеджер службы.
func runUpgrade(dist, cur, d string) (map[string]any, error) {
	newBin := filepath.Join(dist, "runpilot")
	if _, err := os.Stat(newBin); err != nil {
		return nil, fmt.Errorf("upgrade: бинарник %s: %w", newBin, err)
	}
	// 1) самопроверка нового бинарника (не прошёл — ничего не меняем).
	if out, err := exec.Command(newBin, "selftest").CombinedOutput(); err != nil {
		return nil, fmt.Errorf("upgrade: selftest нового бинарника: %w: %s", err, out)
	}
	prev := runpilotPrevNameIn(filepath.Dir(cur))
	// 2) сохранить текущий как runpilot.prev (откат, 6.6).
	if err := copyBin(cur, prev); err != nil {
		return nil, fmt.Errorf("upgrade: runpilot.prev: %w", err)
	}
	// 3) установить новый.
	if err := copyBin(newBin, cur); err != nil {
		return nil, fmt.Errorf("upgrade: установка: %w", err)
	}
	// 4) бинарники целей в coordinator.dist_dir (узлы скачают сами).
	var copied []string
	if d != "" {
		copied = copyDistTargets(dist, d)
	}
	return map[string]any{"prev": prev, "installed": cur, "dist_targets": copied}, nil
}

// copyBin — копия бинарника с правами исполняемого.
func copyBin(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.WriteFile(dst, data, plainDirMode); err != nil {
		return err
	}
	return os.Chmod(dst, plainDirMode)
}

// copyDistTargets — бинарники из dist в dist_dir (для узлов, 14.2).
func copyDistTargets(dist, dstDir string) []string {
	entries, err := os.ReadDir(dist)
	if err != nil {
		return nil
	}
	if err := os.MkdirAll(dstDir, plainDirMode); err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if name == "runpilot" {
			continue // координатор установлен отдельно
		}
		data, err := os.ReadFile(filepath.Join(dist, name))
		if err != nil {
			continue
		}
		if err := os.WriteFile(filepath.Join(dstDir, name), data, plainDirMode); err == nil {
			out = append(out, name)
		}
	}
	return out
}
