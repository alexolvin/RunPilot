package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"runpilot/internal/clock"
	"runpilot/internal/node"
	"runpilot/profiles"
)

// tokenFromSecrets — фолбэк токена узла: env (client.token_env) не задан —
// ~/.config/runpilot/secrets.env (пишет install.sh, 14.1; ТЗ 666). Без него после
// перезагрузки systemd-узел не сможет подключиться.
func tokenFromSecrets(envName string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(home, ".config", "runpilot", "secrets.env"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if v, ok := strings.CutPrefix(line, envName+"="); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// newNodeCmd — узловой демон (разделы 7, 13 ТЗ) + подкоманды drain/undrain.
func newNodeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "node",
		Short: "Узловой демон runpilot: сканер панелей, снимки, канал к координатору",
		RunE: func(cmd *cobra.Command, args []string) error {
			log := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
			clk := clock.NewReal()

			prof, err := profiles.LoadQwen()
			if err != nil {
				return err
			}
			n, err := node.New(cfg, prof, node.CmdExecer{}, clk, log)
			if err != nil {
				return err
			}
			token := os.Getenv(cfg.Client.TokenEnv)
			if token == "" {
				token = tokenFromSecrets(cfg.Client.TokenEnv)
			}
			n.SetNodeToken(token) // self-update: загрузка дистрибутива (14.2)
			ws := node.NewWSClient(cfg.Client.Coordinator, token, version, clk, log, n)

			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			log.Info("node: старт", "coordinator", cfg.Client.Coordinator,
				"sockets", cfg.Node.TmuxSockets, "scan_sec", cfg.Node.ScanIntervalSec)
			if err := ws.Run(ctx); err != nil && ctx.Err() == nil {
				// 14.2 ТЗ: self-update успешен → бинарник заменён; выходим
				// кодом 75, systemd (Restart) перезапустит новый бинарник.
				// tmux и кодеры не дети процесса — сессии не теряются.
				if errors.Is(err, node.ErrUpdated) {
					log.Info("node: self-update завершён, выход кодом 75")
					os.Exit(exitSelfUpdated)
				}
				return fmt.Errorf("node: %w", err)
			}
			log.Info("node: остановлен")
			return nil
		},
	}
	cmd.AddCommand(newNodeDrainSubs()...)
	return cmd
}
