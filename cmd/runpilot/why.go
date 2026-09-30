package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"runpilot/internal/client"
)

// newWhyCmd — `runpilot why <t> [--json]` (раздел 11 ТЗ).
func newWhyCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "why <t>",
		Short: "Почему запись не получает слот",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newAPIClient()
			t, err := requireTarget(c, args[0], "")
			if err != nil {
				return err
			}
			var why client.Why
			if _, err := c.Do("GET", "/api/v1/sessions/"+t+"/why", nil, &why); err != nil {
				return err
			}
			if jsonOut {
				return printJSON(why)
			}
			fmt.Printf("сессия %s  состояние %s\n", why.SID, why.State)
			if why.Reason != "" {
				fmt.Printf("причина: %s\n", why.Reason)
				if len(why.Predicates) > 0 {
					fmt.Println("предикаты:")
					for k, v := range why.Predicates {
						fmt.Printf("  %s: %v\n", k, v)
					}
				}
			} else {
				fmt.Println("причина: — (слот доступен)")
			}
			if len(why.Candidates) > 0 {
				fmt.Printf("кандидаты: %s\n", strings.Join(why.Candidates, ", "))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "машиночитаемый вывод")
	return cmd
}
