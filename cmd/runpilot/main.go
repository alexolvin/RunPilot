// runpilot — RunPilot: один бинарник для координатора, узла и клиента (ТЗ раздел 3).
package main

import (
	"debug/buildinfo"
	"errors"
	"fmt"
	"os"
	"time"

	"runpilot/internal/config"
)

// versionInfo — версия (git-тег) + коммит (vcs.revision); единственный
// источник — debug.ReadBuildInfo (W15: ldflags и short-hash убраны).
var versionInfo VersionInfo

// buildTimeUnix — commit timestamp (vcs.time) из buildinfo, unix-сек; 0 если
// не из git-дерева. Для детекта отката при self-update
// (W12: откат на более старую версию — только с downgrade=true).
var buildTimeUnix int64

// buildTime — commit timestamp (vcs.time) из buildinfo; 0 если нет.
func buildTime() int64 {
	exe, err := os.Executable()
	if err != nil {
		return 0
	}
	bi, err := buildinfo.ReadFile(exe)
	if err != nil {
		return 0
	}
	for _, s := range bi.Settings {
		if s.Key == "vcs.time" {
			if t, perr := time.Parse(time.RFC3339, s.Value); perr == nil {
				return t.Unix()
			}
			return 0
		}
	}
	return 0
}

func main() {
	versionInfo = buildVersionInfo()
	buildTimeUnix = buildTime()
	root := newRoot(versionInfo.Version)
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "runpilot:", err)
		switch {
		case errors.Is(err, config.ErrUnknownField), errors.Is(err, config.ErrInvalid):
			os.Exit(exitConfigError) // нарушение схемы/семантики конфигурации
		default:
			os.Exit(1)
		}
	}
}
