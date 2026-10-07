package main

import (
	"debug/buildinfo"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// versionDev — версия без vcs-информации (buildinfo нет / не из git-дерева).
const versionDev = "0.0.0-dev"

// buildVersion — версия из debug.ReadBuildInfo: короткий хеш vcs.revision,
// суффикс «-dirty» при vcs.modified=true. Переопределение
// -ldflags "-X main.version=..." имеет приоритет.
func buildVersion() string {
	if version != versionDev {
		return version
	}
	exe, err := os.Executable()
	if err != nil {
		return version
	}
	bi, err := buildinfo.ReadFile(exe)
	if err != nil {
		return version
	}
	var rev, modified string
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			modified = s.Value
		}
	}
	if len(rev) < versionShortHashLen {
		return version
	}
	v := rev[:versionShortHashLen]
	if modified == "true" {
		v += "-dirty"
	}
	return v
}

func newVersionCmd(version string) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Версия runpilot",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println(version)
		},
	}
}
