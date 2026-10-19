package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"runpilot/internal/api"
	"runpilot/internal/config"
)

// newNodeKeyCmd — SSH-ключ координатора для подключения к узлам: генерация
// нового ключа (ротация без простоя) и печать строки authorized_keys
// (restrict + from="<tailnet-ip>").
func newNodeKeyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "node-key",
		Short: "SSH-ключ координатора для узлов: gen (новый ключ) / line (строка authorized_keys)",
	}
	cmd.AddCommand(newNodeKeyGenCmd(), newNodeKeyLineCmd())
	return cmd
}

// homeSSH — ~/.ssh (абсолютный путь).
func homeSSH() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ssh"), nil
}

// newNodeKeyGenCmd — генерация нового ed25519-ключа для подключения к узлам.
// Новый ключ не конфликтует со старым (отдельное имя) — оператор добавляет его
// в coordinator.ssh_keys (ssh попробует оба) и разносит публичный по узлам.
func newNodeKeyGenCmd() *cobra.Command {
	var out string
	cmd := &cobra.Command{
		Use:   "gen",
		Short: "Сгенерировать новый ed25519-ключ для подключения к узлам",
		RunE: func(cmd *cobra.Command, args []string) error {
			if out == "" {
				dir, err := homeSSH()
				if err != nil {
					return err
				}
				out = filepath.Join(dir, "id_ed25519_runpilot")
			} else if p, err := config.ExpandPath(out); err == nil {
				out = p
			}
			if _, err := os.Stat(out); err == nil {
				return fmt.Errorf("ключ уже существует: %s (укажите другой путь --out)", out)
			}
			if err := os.MkdirAll(filepath.Dir(out), sshDirMode); err != nil {
				return err
			}
			// ssh-keygen — тот же формат, что у оператора; без доп. зависимостей.
			if err := exec.Command("ssh-keygen", "-t", "ed25519", "-f", out,
				"-N", "", "-C", "runpilot-node").Run(); err != nil {
				return fmt.Errorf("ssh-keygen: %w", err)
			}
			fmt.Println(out)
			fmt.Println(out + ".pub")
			return nil
		},
	}
	cmd.Flags().StringVar(&out, "out", "", "путь к приватному ключу (по умолчанию ~/.ssh/id_ed25519_runpilot)")
	return cmd
}

// newNodeKeyLineCmd — печать строки authorized_keys с restrict + from="<ip>".
// from=<ip> — узел принимает ключ только с tailnet-IP координатора.
func newNodeKeyLineCmd() *cobra.Command {
	var key, from string
	cmd := &cobra.Command{
		Use:   "line",
		Short: "Печать строки authorized_keys (restrict,from=\"<ip>\")",
		RunE: func(cmd *cobra.Command, args []string) error {
			if key == "" {
				dir, err := homeSSH()
				if err != nil {
					return err
				}
				key = filepath.Join(dir, "id_ed25519.pub")
			} else if p, err := config.ExpandPath(key); err == nil {
				key = p
			}
			data, err := os.ReadFile(key)
			if err != nil {
				return fmt.Errorf("не удалось прочитать публичный ключ: %w", err)
			}
			// "<keytype> <base64> <comment>" → "<keytype> <base64>".
			fields := strings.Fields(strings.TrimSpace(string(data)))
			if len(fields) < pubKeyFields {
				return fmt.Errorf("некорректный формат публичного ключа в %s", key)
			}
			if from == "" {
				ip, ok := api.TailnetSelfIP()
				if !ok {
					return fmt.Errorf("не удалось определить tailnet-IP машины (tailscale status) — укажите --from <ip>")
				}
				from = ip
			}
			fmt.Printf("restrict,from=\"%s\" %s %s\n", from, fields[0], fields[1])
			return nil
		},
	}
	cmd.Flags().StringVar(&key, "key", "", "путь к публичному ключу (по умолчанию ~/.ssh/id_ed25519.pub)")
	cmd.Flags().StringVar(&from, "from", "", "источный IP для from= (по умолчанию tailnet-IP этой машины)")
	return cmd
}
