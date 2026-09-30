package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"runpilot/internal/client"
)

// postTarget — POST /api/v1/sessions/{t}/<op>; нон-2xx — *client.Error.
func postTarget(c *client.Client, t, op string, body any) error {
	_, err := c.Do("POST", "/api/v1/sessions/"+t+"/"+op, body, nil)
	return err
}

// addPaneFlag — общий флаг --pane %N (адресация раздела 11 ТЗ).
func addPaneFlag(cmd *cobra.Command, paneID *string) {
	cmd.Flags().StringVar(paneID, "pane", "", "панель %N вместо <t>")
}

// newQueueCmds — команды очереди (раздел 11 ТЗ).
func newQueueCmds() []*cobra.Command {
	var cmds []*cobra.Command

	// enqueue
	{
		var class, pin, prefer, after, paneID string
		var front bool
		cmd := &cobra.Command{
			Use:   "enqueue [<t>]",
			Short: "Поставить в очередь",
			Args:  cobra.MaximumNArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				c := newAPIClient()
				t := ""
				if len(args) == 1 {
					t = args[0]
				}
				t, err := requireTarget(c, t, paneID)
				if err != nil {
					return err
				}
				body := client.EnqueueBody{Class: class, Pin: pin, Prefer: prefer, Front: front, After: after}
				return postTarget(c, t, "enqueue", body)
			},
		}
		cmd.Flags().StringVar(&class, "class", "", "класс: resume|high|normal|low")
		cmd.Flags().StringVar(&pin, "pin", "", "привязка к серверу")
		cmd.Flags().StringVar(&prefer, "prefer", "", "предпочтение сервера")
		cmd.Flags().StringVar(&after, "after", "", "поставить после записи")
		cmd.Flags().BoolVar(&front, "front", false, "в начало очереди")
		addPaneFlag(cmd, &paneID)
		cmds = append(cmds, cmd)
	}

	// dequeue
	{
		var paneID string
		cmd := &cobra.Command{
			Use:   "dequeue <t>",
			Short: "Снять с очереди",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				c := newAPIClient()
				t, err := requireTarget(c, args[0], paneID)
				if err != nil {
					return err
				}
				return postTarget(c, t, "dequeue", nil)
			},
		}
		addPaneFlag(cmd, &paneID)
		cmds = append(cmds, cmd)
	}

	// requeue
	{
		var back bool
		var paneID string
		cmd := &cobra.Command{
			Use:   "requeue <t>",
			Short: "Повторно поставить (по умолчанию в начало)",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				c := newAPIClient()
				t, err := requireTarget(c, args[0], paneID)
				if err != nil {
					return err
				}
				body := map[string]any{"back": back}
				return postTarget(c, t, "requeue", body)
			},
		}
		cmd.Flags().BoolVar(&back, "back", false, "в конец очереди (по умолчанию в начало)")
		addPaneFlag(cmd, &paneID)
		cmds = append(cmds, cmd)
	}

	// prio
	{
		var paneID string
		cmd := &cobra.Command{
			Use:   "prio <t> <class>",
			Short: "Сменить класс очереди: resume|high|normal|low",
			Args:  cobra.ExactArgs(twoArgs),
			RunE: func(cmd *cobra.Command, args []string) error {
				c := newAPIClient()
				t, err := requireTarget(c, args[0], paneID)
				if err != nil {
					return err
				}
				return postTarget(c, t, "prio", map[string]any{"class": args[1]})
			},
		}
		addPaneFlag(cmd, &paneID)
		cmds = append(cmds, cmd)
	}

	// pin
	{
		var paneID string
		cmd := &cobra.Command{
			Use:   "pin <t> <server>",
			Short: "Привязать к серверу",
			Args:  cobra.ExactArgs(twoArgs),
			RunE: func(cmd *cobra.Command, args []string) error {
				c := newAPIClient()
				t, err := requireTarget(c, args[0], paneID)
				if err != nil {
					return err
				}
				return postTarget(c, t, "pin", map[string]any{"server": args[1]})
			},
		}
		addPaneFlag(cmd, &paneID)
		cmds = append(cmds, cmd)
	}

	// prefer
	{
		var paneID string
		cmd := &cobra.Command{
			Use:   "prefer <t> <server>",
			Short: "Предпочитать сервер",
			Args:  cobra.ExactArgs(twoArgs),
			RunE: func(cmd *cobra.Command, args []string) error {
				c := newAPIClient()
				t, err := requireTarget(c, args[0], paneID)
				if err != nil {
					return err
				}
				return postTarget(c, t, "prefer", map[string]any{"server": args[1]})
			},
		}
		addPaneFlag(cmd, &paneID)
		cmds = append(cmds, cmd)
	}

	// unpin
	{
		var paneID string
		cmd := &cobra.Command{
			Use:   "unpin <t>",
			Short: "Снять привязку",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				c := newAPIClient()
				t, err := requireTarget(c, args[0], paneID)
				if err != nil {
					return err
				}
				return postTarget(c, t, "unpin", nil)
			},
		}
		addPaneFlag(cmd, &paneID)
		cmds = append(cmds, cmd)
	}

	// hold
	{
		var paneID string
		cmd := &cobra.Command{
			Use:   "hold <t>",
			Short: "Ручной hold",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				c := newAPIClient()
				t, err := requireTarget(c, args[0], paneID)
				if err != nil {
					return err
				}
				return postTarget(c, t, "hold", nil)
			},
		}
		addPaneFlag(cmd, &paneID)
		cmds = append(cmds, cmd)
	}

	// unhold
	{
		var paneID string
		cmd := &cobra.Command{
			Use:   "unhold <t>",
			Short: "Снять ручной hold",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				c := newAPIClient()
				t, err := requireTarget(c, args[0], paneID)
				if err != nil {
					return err
				}
				return postTarget(c, t, "unhold", nil)
			},
		}
		addPaneFlag(cmd, &paneID)
		cmds = append(cmds, cmd)
	}

	// set <t> auto-enqueue on|off
	{
		var paneID string
		cmd := &cobra.Command{
			Use:   "set <t> auto-enqueue <on|off>",
			Short: "Автопостановка",
			Args:  cobra.ExactArgs(setArgs),
			RunE: func(cmd *cobra.Command, args []string) error {
				if args[1] != "auto-enqueue" {
					return fmt.Errorf("runpilot set: поддерживается только auto-enqueue")
				}
				c := newAPIClient()
				t, err := requireTarget(c, args[0], paneID)
				if err != nil {
					return err
				}
				return postTarget(c, t, "auto-enqueue", map[string]any{"on": args[setArgs-1] == "on"})
			},
		}
		addPaneFlag(cmd, &paneID)
		cmds = append(cmds, cmd)
	}

	// cancel
	{
		var paneID string
		cmd := &cobra.Command{
			Use:   "cancel <t>",
			Short: "Прервать ход",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				c := newAPIClient()
				t, err := requireTarget(c, args[0], paneID)
				if err != nil {
					return err
				}
				return postTarget(c, t, "cancel", nil)
			},
		}
		addPaneFlag(cmd, &paneID)
		cmds = append(cmds, cmd)
	}

	return cmds
}
