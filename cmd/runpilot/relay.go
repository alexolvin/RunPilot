// Связь с проектом оркестратора (ТЗ Relay §15.3): команды runpilot relay *.
package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"runpilot/internal/client"
)

// newRelayCmd — `runpilot relay …` (ТЗ Relay §15.3).
func newRelayCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "relay",
		Short: "Связь сессии с проектом оркестратора",
	}
	cmd.AddCommand(
		newRelaySetCmd(),
		newRelayStatusCmd(),
		newRelayActionCmd("send", "Отправить отчёт сейчас"),
		newRelayActionCmd("deliver", "Доставить ответ агенту сейчас"),
		newRelayActionCmd("skip", "Пропустить цикл"),
		newRelayActionCmd("resume", "Продолжить связь"),
		newRelayCheckCmd(),
	)
	return cmd
}

// newRelaySetCmd — `runpilot relay set <t> <url>|--off`.
func newRelaySetCmd() *cobra.Command {
	var off, jsonOut bool
	cmd := &cobra.Command{
		Use:   "set <t> <url>",
		Short: "Связать сессию с чатом (--off — снять)",
		Args:  cobra.RangeArgs(1, twoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newAPIClient()
			t, err := requireTarget(c, args[0], "")
			if err != nil {
				return err
			}
			body := map[string]any{}
			if off {
				body["relay_enabled"] = false
				body["orch_url"] = ""
			} else {
				if len(args) < twoArgs {
					return fmt.Errorf("укажите URL чата или --off")
				}
				body["orch_url"] = args[1]
				body["relay_enabled"] = true
			}
			var res map[string]any
			if _, err := c.Do("PATCH", "/api/v1/sessions/"+t, body, &res); err != nil {
				return err
			}
			if jsonOut {
				return printJSON(res)
			}
			fmt.Println(relaySetSummary(res))
			return nil
		},
	}
	cmd.Flags().BoolVar(&off, "off", false, "снять связь")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "машиночитаемый вывод")
	return cmd
}

// newRelayStatusCmd — `runpilot relay status [<t>] [--json]`.
func newRelayStatusCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "status [<t>]",
		Short: "Статус связи",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newAPIClient()
			if len(args) == 0 {
				var sessions []client.Session
				if _, err := c.Do("GET", "/api/v1/sessions?all=true", nil, &sessions); err != nil {
					return err
				}
				rows := make([][]string, 0, len(sessions))
				for _, s := range sessions {
					if s.OrchURL == "" {
						continue
					}
					var st map[string]any
					if _, err := c.Do("GET", "/api/v1/sessions/"+s.SID+"/relay", nil, &st); err != nil {
						continue
					}
					rows = append(rows, []string{
						s.Name, strVal(st["site"]), strVal(st["state"]), strVal(st["hold_reason"]),
					})
				}
				if jsonOut {
					return printJSON(rows)
				}
				printTable([]string{"SESSION", "SITE", "STATE", "HOLD"}, rows)
				return nil
			}
			t, err := requireTarget(c, args[0], "")
			if err != nil {
				return err
			}
			var st map[string]any
			if _, err := c.Do("GET", "/api/v1/sessions/"+t+"/relay", nil, &st); err != nil {
				return err
			}
			if jsonOut {
				return printJSON(st)
			}
			fmt.Printf("site=%s state=%s hold=%s cycle=%v\n",
				strVal(st["site"]), strVal(st["state"]), strVal(st["hold_reason"]), st["cycle_seq"])
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "машиночитаемый вывод")
	return cmd
}

// newRelayActionCmd — `runpilot relay <action> <t>`.
func newRelayActionCmd(action, short string) *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   action + " <t>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newAPIClient()
			t, err := requireTarget(c, args[0], "")
			if err != nil {
				return err
			}
			var res map[string]any
			if _, err := c.Do("POST", "/api/v1/sessions/"+t+"/relay/"+action, nil, &res); err != nil {
				return err
			}
			if jsonOut {
				return printJSON(res)
			}
			fmt.Printf("%s: state=%s\n", action, strVal(res["state"]))
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "машиночитаемый вывод")
	return cmd
}

// newRelayCheckCmd — `runpilot relay check <url>`.
func newRelayCheckCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "check <url>",
		Short: "Проверить ссылку на чат (сайт, вход, поля)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newAPIClient()
			body := map[string]any{"url": args[0]}
			var res map[string]any
			if _, err := c.Do("POST", "/api/v1/relay/check", body, &res); err != nil {
				return err
			}
			if jsonOut {
				return printJSON(res)
			}
			fmt.Printf("site=%s chat=%s open=%v logged_in=%v composer=%v send=%v files=%v\n",
				strVal(res["site"]), strVal(res["chat_key"]), boolVal(res["open"]),
				boolVal(res["logged_in"]), boolVal(res["composer"]),
				boolVal(res["send_button"]), boolVal(res["file_input"]))
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "машиночитаемый вывод")
	return cmd
}

func relaySetSummary(res map[string]any) string {
	if b, ok := res["linked"].(bool); ok && b {
		return fmt.Sprintf("связь: site=%s chat=%s state=%s",
			strVal(res["site"]), strVal(res["chat_key"]), strVal(res["state"]))
	}
	return "связь снята"
}

func strVal(v any) string {
	s, _ := v.(string)
	return s
}

func boolVal(v any) bool {
	b, _ := v.(bool)
	return b
}

var _ = json.Marshal
