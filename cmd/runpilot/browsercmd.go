// Команды браузера координатора (ТЗ Relay §15.3): runpilot browser open/view.
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
	cmd.AddCommand(newBrowserOpenCmd())
	cmd.AddCommand(newBrowserViewCmd())
	return cmd
}

// newBrowserOpenCmd — `runpilot browser open <session> <url>`: запускает Xvfb
// и открывает вкладку сессии в её профиле (своя авторизация); печатает ссылку
// на окно живого просмотра.
func newBrowserOpenCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "open <session> <url>",
		Short: "Запустить браузер и открыть вкладку сессии",
		Args:  cobra.ExactArgs(twoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newAPIClient()
			var res struct {
				Tab     string `json:"tab"`
				ViewURL string `json:"view_url"`
			}
			body := map[string]string{"session": args[0], "url": args[1]}
			if _, err := c.Do("POST", "/api/v1/browser/tabs", body, &res); err != nil {
				return err
			}
			if jsonOut {
				return printJSON(res)
			}
			fmt.Println("tab: " + res.Tab)
			fmt.Println("view: " + res.ViewURL)
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "машиночитаемый вывод")
	return cmd
}

// newBrowserViewCmd — `runpilot browser view`: печатает ссылки на окна
// живого просмотра открытых вкладок.
func newBrowserViewCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "view",
		Short: "Ссылки на окна живого просмотра",
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
			base := cfg.Web.PublicURL
			if base == "" {
				base = c.Base()
			}
			for _, tab := range res.Tabs {
				fmt.Println(base + "/web/ws/browser/view?tab=" + tab)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "машиночитаемый вывод")
	return cmd
}
