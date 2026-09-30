package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"runpilot/internal/client"
)

// newStatsCmd — `runpilot stats [--since 24h]` (раздел 11 ТЗ). Без токенов.
func newStatsCmd() *cobra.Command {
	var since string
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Ходы и длительности по серверам и сессиям (без токенов)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newAPIClient()
			path := "/api/v1/stats?since=" + since
			var res client.StatsResult
			if _, err := c.Do("GET", path, nil, &res); err != nil {
				return err
			}
			if jsonOut {
				return printJSON(res)
			}
			if len(res.ByServer) > 0 {
				fmt.Println("Серверы:")
				srv := make([][]string, 0, len(res.ByServer))
				for _, s := range res.ByServer {
					srv = append(srv, []string{
						s.Server, fmt.Sprintf("%d", s.Turns),
						fmt.Sprintf("%d", s.OK), fmt.Sprintf("%d", s.Err),
						fmt.Sprintf("%ds", s.TotalSec), fmt.Sprintf("%.1fs", s.AvgSec),
					})
				}
				printTable([]string{"SERVER", "TURNS", "OK", "ERR", "TOTAL", "AVG"}, srv)
			}
			if len(res.BySession) > 0 {
				fmt.Println("Сессии:")
				sess := make([][]string, 0, len(res.BySession))
				for _, s := range res.BySession {
					name := s.Session
					if name == "" {
						name = s.SID
					}
					sess = append(sess, []string{
						name, fmt.Sprintf("%d", s.Turns),
						fmt.Sprintf("%d", s.OK), fmt.Sprintf("%d", s.Err),
						fmt.Sprintf("%ds", s.TotalSec),
					})
				}
				printTable([]string{"SESSION", "TURNS", "OK", "ERR", "TOTAL"}, sess)
			}
			if len(res.ByServer) == 0 && len(res.BySession) == 0 {
				fmt.Println("за период нет ходов")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&since, "since", defaultSince, "период (например 24h, 60m)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "машиночитаемый вывод")
	return cmd
}
