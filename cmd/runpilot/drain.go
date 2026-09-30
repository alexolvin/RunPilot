package main

import (
	"os"

	"github.com/spf13/cobra"
)

// newServerCmd — `runpilot server drain|undrain <S>` (раздел 11 ТЗ).
func newServerCmd() *cobra.Command {
	server := &cobra.Command{
		Use:   "server",
		Short: "Управление сервером",
	}

	drain := &cobra.Command{
		Use:   "drain <server>",
		Short: "Вывести сервер (больше не выдавать)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return postDrain("servers", args[0], true)
		},
	}
	undrain := &cobra.Command{
		Use:   "undrain <server>",
		Short: "Вернуть сервер",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return postDrain("servers", args[0], false)
		},
	}
	server.AddCommand(drain, undrain)
	return server
}

// newNodeDrainSubs — подкоманды `runpilot node drain|undrain [<host>]`
// (раздел 11 ТЗ); прикрепляются к демону `runpilot node`.
func newNodeDrainSubs() []*cobra.Command {
	drain := &cobra.Command{
		Use:   "drain [<host>]",
		Short: "Вывести узел: локальные сессии (не RUNNING/DETACHED) → HOLD(NODE_DRAIN)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return postDrain("nodes", hostOrLocal(args), true)
		},
	}
	undrain := &cobra.Command{
		Use:   "undrain [<host>]",
		Short: "Вернуть узел",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return postDrain("nodes", hostOrLocal(args), false)
		},
	}
	return []*cobra.Command{drain, undrain}
}

// postDrain — POST /api/v1/{servers|nodes}/{name}/drain {on}.
func postDrain(kind, name string, on bool) error {
	c := newAPIClient()
	_, err := c.Do("POST", "/api/v1/"+kind+"/"+name+"/drain", map[string]any{"on": on}, nil)
	return err
}

// hostOrLocal — host из args или локальный hostname.
func hostOrLocal(args []string) string {
	if len(args) == 1 && args[0] != "" {
		return args[0]
	}
	if h, err := os.Hostname(); err == nil {
		return h
	}
	return ""
}

// --- pause / resume ---

func newPauseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pause",
		Short: "Пауза выдачи submit/resume",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newAPIClient()
			_, err := c.Do("POST", "/api/v1/pause", map[string]any{"on": true}, nil)
			return err
		},
	}
}

func newResumeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "resume",
		Short: "Снять паузу",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newAPIClient()
			_, err := c.Do("POST", "/api/v1/resume", nil, nil)
			return err
		},
	}
}
