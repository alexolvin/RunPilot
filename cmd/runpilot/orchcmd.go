// Команды оркестратора (ТЗ Relay §15.3): runpilot orch fixture capture.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/spf13/cobra"
)

// newOrchCmd — `runpilot orch …` (ТЗ Relay §15.3).
func newOrchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "orch",
		Short: "Оркестратор (профили сайтов, фикстуры)",
	}
	cmd.AddCommand(newOrchFixtureCmd())
	return cmd
}

// newOrchFixtureCmd — `runpilot orch fixture capture <t> <state>`.
func newOrchFixtureCmd() *cobra.Command {
	var fixDir string
	capture := &cobra.Command{
		Use:   "capture <t> <state>",
		Short: "Снять DOM чата оркестратора в фикстуру",
		Args:  cobra.ExactArgs(twoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			t, state := args[0], args[1]
			if !stateRe.MatchString(state) {
				return fmt.Errorf("state %q: допустимы [a-z][a-z0-9_]*", state)
			}
			c := newAPIClient()
			sid, err := requireTarget(c, t, "")
			if err != nil {
				return err
			}
			var res map[string]any
			if _, err := c.Do("GET", "/api/v1/sessions/"+sid+"/relay", nil, &res); err != nil {
				return err
			}
			if b, _ := res["linked"].(bool); !b {
				return fmt.Errorf("связь не настроена для %s", t)
			}
			site, _ := res["site"].(string)
			dir := filepath.Join(fixDir, "orch", site)
			if err := os.MkdirAll(dir, plainDirMode); err != nil {
				return err
			}
			path := filepath.Join(dir, fmt.Sprintf("%s-%02d.html", state, nextFixtureSeq(dir, state)))
			// DOM снимает координатор (браузер); сюда приходит html в details.
			details, _ := res["details"].(map[string]any)
			html, _ := details["html"].(string)
			if err := os.WriteFile(path, []byte(html), plainFileMode); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), path)
			return nil
		},
	}
	capture.Flags().StringVar(&fixDir, "dir", "testdata/fixtures", "каталог фикстур (относительно cwd)")
	fixture := &cobra.Command{Use: "fixture", Short: "Фикстуры чата оркестратора"}
	fixture.AddCommand(capture)
	return fixture
}

var _ = regexp.MustCompile
