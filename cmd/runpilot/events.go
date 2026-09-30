package main

import (
	"context"
	"fmt"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"runpilot/internal/client"
)

// newEventsCmd — `runpilot events [-f]` (раздел 11 ТЗ).
func newEventsCmd() *cobra.Command {
	var follow, jsonOut bool
	var limit string
	cmd := &cobra.Command{
		Use:   "events",
		Short: "Журнал событий; -f — живая лента (SSE)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newAPIClient()
			if follow {
				return eventsFollow(c)
			}
			path := "/api/v1/events?limit=" + limit
			var events []client.Event
			if _, err := c.Do("GET", path, nil, &events); err != nil {
				return err
			}
			if jsonOut {
				return printJSON(events)
			}
			rows := make([][]string, 0, len(events))
			for i := len(events) - 1; i >= 0; i-- {
				e := events[i]
				rows = append(rows, []string{
					fmtTime(e.TS), e.Server, e.SID, e.Kind, briefPayload(e.Payload),
				})
			}
			printTable([]string{"TIME", "SERVER", "SID", "KIND", "DETAIL"}, rows)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "живая лента (SSE)")
	cmd.Flags().StringVar(&limit, "limit", "200", "число событий (без -f)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "машиночитаемый вывод")
	return cmd
}

// eventsFollow — SSE-лента до Ctrl-C.
func eventsFollow(c *client.Client) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	var lastID int64
	ch := make(chan client.Event, eventChanLen)
	go func() {
		defer close(ch)
		_ = c.StreamEvents(ctx, &lastID, ch)
	}()
	for e := range ch {
		fmt.Printf("%s  %-12s %-14s %-16s %s\n",
			fmtTime(e.TS), e.Server, e.SID, e.Kind, briefPayload(e.Payload))
	}
	return nil
}

// fmtTime — HH:MM:SS из RFC3339.
func fmtTime(ts string) string {
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return ts
	}
	return t.Local().Format("15:04:05")
}

// briefPayload — короткое содержание payload.
func briefPayload(p []byte) string {
	if len(p) == 0 {
		return ""
	}
	s := string(p)
	if len(s) > briefPayloadMax {
		return s[:briefPayloadMax-1] + "…"
	}
	return s
}
