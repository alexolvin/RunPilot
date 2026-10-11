package main

import (
	"debug/buildinfo"
	_ "embed"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// VERSION — человекочитаемый номер версии (формат <год>.<номер обновления в
// году>.<коррекция>), единственный источник строки версии. Обновляется
// оператором при каждом релизе.
//
//go:embed VERSION
var versionFile string

// VersionInfo — версия (VERSION файл) + коммит (vcs.revision).
type VersionInfo struct {
	Version string
	Commit  string
}

// buildVersionInfo — версия из VERSION файла (формат YYYY.N.R), коммит —
// vcs.revision (полный хеш).
func buildVersionInfo() VersionInfo {
	var vi VersionInfo
	v := strings.TrimSpace(versionFile)
	if v == "" {
		v = "dev"
	}
	vi.Version = v
	exe, err := os.Executable()
	if err != nil {
		return vi
	}
	bi, err := buildinfo.ReadFile(exe)
	if err != nil {
		return vi
	}
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
