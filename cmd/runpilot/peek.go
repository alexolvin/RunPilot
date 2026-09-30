package main

import (
	"os"

	"github.com/spf13/cobra"
)

// newPeekCmd — `runpilot peek <t> [-n 40]` (раздел 11 ТЗ).
func newPeekCmd() *cobra.Command {
	var n, paneID string
	cmd := &cobra.Command{
		Use:   "peek <t>",
		Short: "Экран панели",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newAPIClient()
			t, err := requireTarget(c, args[0], paneID)
			if err != nil {
				return err
			}
			path := "/api/v1/sessions/" + t + "/peek"
			if n != "" {
				path += "?n=" + n
			}
			var out []byte
			if _, err := c.DoRaw("GET", path, nil, &out); err != nil {
				return err
			}
			os.Stdout.Write(out)
			if len(out) > 0 && out[len(out)-1] != '\n' {
				os.Stdout.Write([]byte("\n"))
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&n, "n", "n", "", "число строк (по умолчанию 40)")
	addPaneFlag(cmd, &paneID)
	return cmd
}
