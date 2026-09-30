package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"runpilot/internal/client"
)

// newQCmd — `runpilot q [--json]`: очередь с рангом и причиной пропуска.
func newQCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "q",
		Short: "Очередь (ранг, класс, причина пропуска)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newAPIClient()
			var rows []client.QueueRow
			if _, err := c.Do("GET", "/api/v1/queue", nil, &rows); err != nil {
				return err
			}
			if jsonOut {
				return printJSON(rows)
			}
			tbl := make([][]string, 0, len(rows))
			for _, r := range rows {
				why := r.Why
				if why == "" {
					why = "—"
				}
				tbl = append(tbl, []string{
					fmt.Sprintf("%d", r.Rank),
					stringsTitle(r.Class),
					r.Session,
					r.Host,
					why,
					fmtDuration(r.WaitSec),
					attemptsStr(r),
					truncPrompt(r.Prompt),
				})
			}
			printTable(
				[]string{"#", "CLASS", "SESSION", "HOST", "WHY", "WAIT", "TRY", "PROMPT"},
				tbl)
			if len(rows) == 0 {
				fmt.Println("(очередь пуста)")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "машиночитаемый вывод")
	return cmd
}

// stringsTitle — первая буква заглавной (resume→Resume).
func stringsTitle(s string) string {
	if s == "" {
		return s
	}
	b := []byte(s)
	b[0] = b[0] - 'a' + 'A'
	return string(b)
}

// fmtDuration — секунды → 00:31 / 02:10.
func fmtDuration(sec int) string {
	if sec < 0 {
		sec = 0
	}
	m := sec / secondsPerMin
	s := sec % secondsPerMin
	return fmt.Sprintf("%02d:%02d", m, s)
}

// attemptsStr — 1/3 или «—».
func attemptsStr(r client.QueueRow) string {
	if r.Attempts == 0 {
		return "—"
	}
	return fmt.Sprintf("%d/3", r.Attempts)
}

// truncPrompt — до 32 символов + эллипсис.
func truncPrompt(p string) string {
	if len(p) <= promptMaxLen {
		return p
	}
	return p[:promptMaxLen-1] + "…"
}
