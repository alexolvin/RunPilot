package main

import (
	"fmt"
	"net/http"

	"github.com/spf13/cobra"

	"runpilot/internal/client"
)

// newApiClientCmd — `runpilot api-client ...`: API-клиенты шлюза
// (безсессионные OpenAI-запросы в общую очередь).
func newApiClientCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "api-client",
		Short: "API-клиенты шлюза (список, создание, ключи)",
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newAPIClient()
			var rows []client.ApiClientRow
			if _, err := c.Do("GET", "/api/v1/api-clients", nil, &rows); err != nil {
				return err
			}
			if jsonOut {
				return printJSON(rows)
			}
			tbl := make([][]string, 0, len(rows))
			for _, r := range rows {
				tbl = append(tbl, []string{
					r.Name, r.Class,
					boolStr(r.Enabled),
					fmt.Sprintf("%d", r.Requests24h),
					fmt.Sprintf("%d", r.PromptTokens24h+r.CompletionTokens24h),
				})
			}
			printTable([]string{"NAME", "CLASS", "ENABLED", "REQ/24H", "TOKENS/24H"}, tbl)
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "машиночитаемый вывод")

	create := &cobra.Command{
		Use:   "create <name>",
		Short: "Создать API-клиент (ключ показывается один раз)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newAPIClient()
			var out struct {
				Name string `json:"name"`
				Key  string `json:"key"`
			}
			status, err := c.Do(http.MethodPost, "/api/v1/api-clients",
				map[string]string{"name": args[0]}, &out)
			if err != nil {
				return err
			}
			if status != http.StatusOK {
				return fmt.Errorf("api-client create: HTTP %d", status)
			}
			fmt.Printf("создан: %s\nключ (один раз): %s\n", out.Name, out.Key)
			return nil
		},
	}
	disable := &cobra.Command{
		Use:   "disable <name>",
		Short: "Отключить API-клиент",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newAPIClient()
			status, err := c.Do(http.MethodPost, "/api/v1/api-clients/"+args[0]+"/disable", nil, nil)
			if err != nil {
				return err
			}
			if status != http.StatusNoContent {
				return fmt.Errorf("api-client disable: HTTP %d", status)
			}
			fmt.Println("отключён:", args[0])
			return nil
		},
	}
	enable := &cobra.Command{
		Use:   "enable <name>",
		Short: "Включить API-клиент",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newAPIClient()
			status, err := c.Do(http.MethodPost, "/api/v1/api-clients/"+args[0]+"/enable", nil, nil)
			if err != nil {
				return err
			}
			if status != http.StatusNoContent {
				return fmt.Errorf("api-client enable: HTTP %d", status)
			}
			fmt.Println("включён:", args[0])
			return nil
		},
	}
	reissue := &cobra.Command{
		Use:   "reissue <name>",
		Short: "Перевыпустить ключ (показывается один раз)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newAPIClient()
			var out struct {
				Name string `json:"name"`
				Key  string `json:"key"`
			}
			status, err := c.Do(http.MethodPost, "/api/v1/api-clients/"+args[0]+"/reissue", nil, &out)
			if err != nil {
				return err
			}
			if status != http.StatusOK {
				return fmt.Errorf("api-client reissue: HTTP %d", status)
			}
			fmt.Printf("ключ (один раз): %s\n", out.Key)
			return nil
		},
	}

	cmd.AddCommand(create, disable, enable, reissue)
	return cmd
}

func boolStr(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
