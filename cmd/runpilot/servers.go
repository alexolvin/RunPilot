package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"runpilot/internal/client"
)

// newServersCmd — `runpilot servers [--json]` (раздел 11 ТЗ).
func newServersCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "servers",
		Short: "Серверы и нагрузка",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newAPIClient()
			var rows []client.ServerRow
			if _, err := c.Do("GET", "/api/v1/servers", nil, &rows); err != nil {
				return err
			}
			if jsonOut {
				return printJSON(rows)
			}
			tbl := make([][]string, 0, len(rows))
			for _, r := range rows {
				tbl = append(tbl, []string{
					r.Name,
					fmt.Sprintf("%d", r.Priority),
					r.State,
					fmt.Sprintf("%d/%d", r.Used, r.Slots),
					slotSummary(r.SlotInfo),
					metricInt(r.GPU, "%d%%"),
					metricFloat(r.KV, "%d%%"),
					metricFloat(r.GEN, "%d"),
					metricInt(r.EXT, "%d"),
				})
			}
			printTable([]string{"SERVER", "PRIO", "STATE", "SLOTS", "SESSIONS", "GPU", "KV", "GEN", "EXT"}, tbl)
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "машиночитаемый вывод")
	return cmd
}

// metricInt — nil → «—», иначе формат (метрики Э6).
func metricInt(v *int, format string) string {
	if v == nil {
		return "—"
	}
	return fmt.Sprintf(format, *v)
}

// metricFloat — nil → «—», иначе формат (целая часть).
func metricFloat(v *float64, format string) string {
	if v == nil {
		return "—"
	}
	return fmt.Sprintf(format, int(*v))
}

// slotSummary — занятые слоты: «srv:session» через запятую.
func slotSummary(slots []client.ServerSlotRow) string {
	out := ""
	for _, s := range slots {
		if s.Session != "" {
			if out != "" {
				out += ", "
			}
			out += s.Session
		}
	}
	if out == "" {
		out = "—"
	}
	return out
}
