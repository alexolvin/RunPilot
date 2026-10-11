package main

import (
	"debug/buildinfo"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// VersionInfo — версия (git-тег) + коммит (vcs.revision).
type VersionInfo struct {
	Version string
	Commit  string
}

// buildVersionInfo — версия из debug.ReadBuildInfo().Main.Version (git-тег,
// единственный источник; ldflags и short-hash как источники убраны, W15).
// Коммит — vcs.revision (полный хеш).
func buildVersionInfo() VersionInfo {
	var vi VersionInfo
	exe, err := os.Executable()
	if err != nil {
		return vi
	}
	bi, err := buildinfo.ReadFile(exe)
	if err != nil {
		return vi
	}
	// Version — git-тег (vMAJOR.MINOR.PATCH); «(devel)» → «dev».
	v := bi.Main.Version
	if v == "" || v == "(devel)" {
		v = "dev"
	}
	vi.Version = v
	for _, s := range bi.Settings {
		if s.Key == "vcs.revision" {
			vi.Commit = s.Value
			break
		}
	}
	return vi
}

func newVersionCmd(vi VersionInfo) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Версия runpilot",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println(vi.Version)
			if vi.Commit != "" {
				fmt.Println(vi.Commit)
			}
		},
	}
}
