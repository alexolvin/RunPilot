package main

import (
	"github.com/spf13/cobra"

	"runpilot/internal/client"
)

// newLsCmd — `runpilot ls [--all] [--json]` (раздел 11 ТЗ).
func newLsCmd() *cobra.Command {
	var all, jsonOut bool
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "Сессии; --all добавляет UNMANAGED и GONE",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newAPIClient()
			path := "/api/v1/sessions"
			if all {
				path += "?all=true"
			}
			var sessions []client.Session
			if _, err := c.Do("GET", path, nil, &sessions); err != nil {
				return err
			}
			if jsonOut {
				return printJSON(sessions)
			}
			rows := make([][]string, 0, len(sessions))
			for _, s := range sessions {
				rows = append(rows, []string{
					s.Name, s.State, s.Host, s.TmuxSession, s.PaneID,
					classStr(s.Class), constraintStr(s),
				})
			}
			printTable(
				[]string{"SESSION", "STATE", "HOST", "TMUX", "PANE", "CLASS", "CONSTRAINT"},
				rows)
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "включая UNMANAGED и GONE")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "машиночитаемый вывод")
	return cmd
}

// classStr — класс очереди по номеру (0..3).
func classStr(class int) string {
	switch class {
	case 0:
		return "resume"
	case 1:
		return "high"
	case queueClassLow:
		return "low"
	default:
		return "normal"
	}
}

// constraintStr — ограничение (pin:/prefer:сервер).
func constraintStr(s client.Session) string {
	switch s.ConstraintKind {
	case "pin":
		return "pin:" + s.ConstraintServer
	case "prefer":
		return "prefer:" + s.ConstraintServer
	default:
		return ""
	}
}
