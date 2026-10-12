// Команды браузера координатора (ТЗ Relay §15.3): runpilot browser view.
package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// newBrowserCmd — `runpilot browser …` (ТЗ Relay §15.3).
func newBrowserCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "browser",
		Short: "Браузер координатора (связи)",
	}
	cmd.AddCommand(newBrowserViewCmd())
	return cmd
}

// newBrowserViewCmd — `runpilot browser view`: печатает ссылку на окно.
func newBrowserViewCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "view",
		Short: "Ссылка на окно живого просмотра",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newAPIClient()
			var res struct {
				Running bool     `json:"running"`
				Tabs    []string `json:"tabs"`
			}
			if _, err := c.Do("GET", "/api/v1/browser", nil, &res); err != nil {
				return err
			}
			if jsonOut {
				return printJSON(res)
			}
			if !res.Running {
				fmt.Println("браузер не запущен")
				return nil
			}
			if len(res.Tabs) == 0 {
				fmt.Println("вкладок нет")
				return nil
			}
			base := c.Base()
			for _, tab := range res.Tabs {
				fmt.Println(base + "/api/v1/browser/view?tab=" + tab)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "машиночитаемый вывод")
	return cmd
}
