package main

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"

	"runpilot/internal/client"
)

// newAttachCmd — `runpilot attach <t>` (раздел 11 ТЗ):
//
//	ssh -t <host> tmux attach-session -t <session> \; select-pane -t <pane_id>
func newAttachCmd() *cobra.Command {
	var paneID string
	cmd := &cobra.Command{
		Use:   "attach <t>",
		Short: "Присоединиться к панели (ssh + tmux)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newAPIClient()
			s, err := resolveSession(c, args[0], paneID)
			if err != nil {
				return err
			}
			if s.Host == "" || s.TmuxSession == "" || s.PaneID == "" {
				return fmt.Errorf("attach: у сессии нет host/tmux/pane")
			}
			remote := "tmux attach-session -t " + s.TmuxSession +
				" \\; select-pane -t " + s.PaneID
			ssh := exec.Command("ssh", "-t", s.Host, remote)
			ssh.Stdin, ssh.Stdout, ssh.Stderr = os.Stdin, os.Stdout, os.Stderr
			if err := ssh.Run(); err != nil {
				// Оператор мог отстегнуться — печатаем команду вручную.
				fmt.Fprintf(os.Stderr, "attach: ssh завершился: %v\n", err)
				fmt.Fprintf(os.Stderr, "повторите вручную: ssh -t %s '%s'\n", s.Host, remote)
				return nil
			}
			return nil
		},
	}
	addPaneFlag(cmd, &paneID)
	return cmd
}

// resolveSession — адресация <t>/--pane → полная запись сессии.
func resolveSession(c *client.Client, t, paneID string) (client.Session, error) {
	target, err := requireTarget(c, t, paneID)
	if err != nil {
		return client.Session{}, err
	}
	var sessions []client.Session
	if _, err := c.Do("GET", "/api/v1/sessions?all=true", nil, &sessions); err != nil {
		return client.Session{}, err
	}
	for _, s := range sessions {
		if s.SID == target || s.Name == target {
			return s, nil
		}
	}
	return client.Session{}, fmt.Errorf("нет сессии %s", target)
}
