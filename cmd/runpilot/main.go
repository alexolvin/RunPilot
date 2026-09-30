// runpilot — RunPilot: один бинарник для координатора, узла и клиента (ТЗ раздел 3).
package main

import (
	"errors"
	"fmt"
	"os"

	"runpilot/internal/config"
)

// version — подставляется через -ldflags "-X main.version=...".
var version = "0.0.0-dev"

func main() {
	root := newRoot(version)
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
