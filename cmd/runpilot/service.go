package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
)

// newServiceCmd — `runpilot service install serve|node` (раздел 11 ТЗ).
// systemd (linux) / launchd (darwin). Пишет юнит и включает его; при
// отсутствии init-системы печатает созданный файл и команды вручную.
func newServiceCmd() *cobra.Command {
	install := &cobra.Command{
		Use:   "install <serve|node>",
		Short: "Установить systemd/launchd-службу",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			role := args[0]
			if role != "serve" && role != "node" {
				return fmt.Errorf("service install: только serve или node")
			}
			bin, err := os.Executable()
			if err != nil {
				return err
			}
			switch runtime.GOOS {
			case "linux":
				return installSystemd(bin, resolvedPath, role)
			case "darwin":
				return fmt.Errorf("launchd: не реализовано в этой среде (см. systemd)")
			default:
				return fmt.Errorf("service install: unsupported OS %s", runtime.GOOS)
			}
		},
	}
	parent := &cobra.Command{Use: "service", Short: "Системные службы"}
	parent.AddCommand(install)
	return parent
}

// installSystemd — генерирует юнит, кладёт его, включает и запускает
// (раздел 14.1 ТЗ: после install узел появляется онлайн).
func installSystemd(bin, config, role string) error {
	desc := "RunPilot " + role
	// PATH сервиса минимален, а tmux передаёт окружение создающего клиента
	// первой панели сессии: без user-bin в PATH кодер (shebang «env node»)
	// не стартует (W9 доп-3e). EnvironmentFile — опциональный (-): токен
	// узла/координатора из секрета, роль определяет файл.
	envFile := ""
	if role == "node" {
		envFile = "EnvironmentFile=-%h/.config/runpilot/secrets.env\n"
	} else {
		envFile = "EnvironmentFile=-%h/.config/runpilot/env\n"
	}
	unit := fmt.Sprintf(`[Unit]
Description=%s
After=network.target

[Service]
Type=simple
ExecStart=%s %s --config %s
Environment=PATH=%%h/.local/bin:/usr/local/bin:/usr/bin:/bin
%sRestart=always
RestartSec=2

[Install]
WantedBy=multi-user.target
`, desc, bin, role, config, envFile)

	// System-юнит (нужны root); иначе user-юнит (дефолтный таргет —
	// default.target, а не multi-user).
	home, _ := os.UserHomeDir()
	target := "/etc/systemd/system/runpilot-" + role + ".service"
	var cmdReload, cmdEnable, cmdStart []string
	if os.Geteuid() != 0 {
		target = home + "/.config/systemd/user/runpilot-" + role + ".service"
		unit = strings.Replace(unit, "WantedBy=multi-user.target", "WantedBy=default.target", 1)
		cmdReload = []string{"systemctl", "--user", "daemon-reload"}
		cmdEnable = []string{"systemctl", "--user", "enable", "runpilot-" + role}
		cmdStart = []string{"systemctl", "--user", "start", "runpilot-" + role}
	} else {
		cmdReload = []string{"systemctl", "daemon-reload"}
		cmdEnable = []string{"systemctl", "enable", "runpilot-" + role}
		cmdStart = []string{"systemctl", "start", "runpilot-" + role}
	}
	if err := os.MkdirAll(dirOf(target), plainDirMode); err != nil {
		return err
	}
	if err := os.WriteFile(target, []byte(unit), plainFileMode); err != nil {
		return err
	}
	fmt.Println("записан юнит:", target)
	if runSystemd(cmdReload) && runSystemd(cmdEnable) && runSystemd(cmdStart) {
		fmt.Println("служба включена и запущена")
		return nil
	}
	fmt.Println("systemctl недоступен — выполните вручную:")
	fmt.Println("  " + strings.Join(cmdReload, " "))
	fmt.Println("  " + strings.Join(cmdEnable, " "))
	fmt.Println("  " + strings.Join(cmdStart, " "))
	return nil
}

func runSystemd(args []string) bool {
	if _, err := exec.LookPath(args[0]); err != nil {
		return false
	}
	return exec.Command(args[0], args[1:]...).Run() == nil
}

func dirOf(p string) string {
	for i := len(p) - 1; i > 0; i-- {
		if p[i] == '/' {
			return p[:i]
		}
	}
	return "."
}
