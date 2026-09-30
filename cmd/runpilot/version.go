package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newVersionCmd(version string) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Версия runpilot",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println(version)
		},
	}
}
