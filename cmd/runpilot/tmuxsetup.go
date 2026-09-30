package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// Блок интеграции runpilot в ~/.tmux.conf (раздел 11 ТЗ). Пишется между
// маркерами; повторный запуск заменяет только блок.
const (
	tmuxBlockStart = "# >>> runpilot >>>"
	tmuxBlockEnd   = "# <<< runpilot <<<"
)

// tmuxBlock — содержимое блока (раздел 11 ТЗ).
const tmuxBlock = tmuxBlockStart + `
bind-key Q run-shell -b "runpilot enqueue --pane '#{pane_id}'"
bind-key M-q run-shell -b "runpilot dequeue --pane '#{pane_id}'"
bind-key M-a display-popup -E -w 90% -h 90% "runpilot"
set -g status-interval 2
set -ag status-right " #{@runpilot_status} #{@runpilot_summary}"
` + tmuxBlockEnd

// newTmuxSetupCmd — `runpilot tmux-setup`.
func newTmuxSetupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tmux-setup",
		Short: "Блок интеграции runpilot в ~/.tmux.conf (между маркерами) + source-file",
		RunE: func(cmd *cobra.Command, args []string) error {
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			path := filepath.Join(home, ".tmux.conf")
			data, err := os.ReadFile(path)
			var new string
			switch {
			case os.IsNotExist(err):
				new = tmuxBlock + "\n"
			case err != nil:
				return err
			default:
				new = replaceBlock(string(data), tmuxBlock)
			}
			if err := os.MkdirAll(filepath.Dir(path), tmuxDirMode); err != nil {
				return err
			}
			if err := os.WriteFile(path, []byte(new), tmuxFileMode); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "блок runpilot записан в", path)
			// source-file — только при живом tmux-сервере.
			if out, err := exec.Command("tmux", "source-file", path).CombinedOutput(); err != nil {
				fmt.Fprintln(cmd.OutOrStdout(),
					"tmux source-file пропущен (сервер не запущен?):",
					strings.TrimSpace(string(out)))
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "tmux source-file выполнен")
			}
			return nil
		},
	}
}

// replaceBlock — замена (или добавление) блока между маркерами.
func replaceBlock(conf, block string) string {
	block += "\n"
	start := strings.Index(conf, tmuxBlockStart)
	end := strings.Index(conf, tmuxBlockEnd)
	switch {
	case start >= 0 && end > start:
		tail := conf[end+len(tmuxBlockEnd):]
		if i := strings.IndexByte(tail, '\n'); i >= 0 {
			tail = tail[i+1:]
		}
		return conf[:start] + block + tail
	case start >= 0:
		// Стартованный, но не закрытый блок — пересобираем.
		return conf[:start] + block + "\n"
	default:
		return conf + "\n" + block
	}
}
