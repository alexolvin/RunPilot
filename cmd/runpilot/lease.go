package main

import (
	"fmt"
	"net/http"

	"github.com/spf13/cobra"
)

// newLeaseCmd — `runpilot lease release <sid>`: административное снятие аренды.
func newLeaseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "lease",
		Short: "Управление аренды",
	}

	release := &cobra.Command{
		Use:   "release <sid>",
		Short: "Снять аренду по sid (причина OPERATOR)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sid := args[0]
			c := newAPIClient()
			var out struct {
				Released bool   `json:"released"`
				SID      string `json:"sid"`
				Server   string `json:"server"`
			}
			status, err := c.Do(http.MethodPost, "/api/v1/leases/release",
				map[string]string{"sid": sid}, &out)
			if err != nil {
				return err
			}
			if status != http.StatusOK {
				return fmt.Errorf("lease release: %s", out.Server)
			}
			fmt.Printf("аренда снята: %s (server: %s)\n", out.SID, out.Server)
			return nil
		},
	}

	cmd.AddCommand(release)
	return cmd
}
