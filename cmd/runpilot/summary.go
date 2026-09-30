package main

import (
	"fmt"

	"runpilot/internal/client"
	"runpilot/internal/model"
)

// runSummary — `runpilot` без аргументов (v2 раздел 1): режим координатора,
// число ходов (идущих), записи очереди и URL веб-панели. Данные — только
// из API (R3): состояния и режим не вычисляются по-другому.
func runSummary() error {
	c := newAPIClient()
	var st client.State
	if _, err := c.Do("GET", "/api/v1/state", nil, &st); err != nil {
		return err
	}
	var q []client.QueueRow
	if _, err := c.Do("GET", "/api/v1/queue", nil, &q); err != nil {
		return err
	}
	active := 0
	for _, s := range st.Sessions {
		switch s.State {
		case string(model.SessionDispatching),
			string(model.SessionRunning),
			string(model.SessionDetached):
			active++
		}
	}
	fmt.Println("RunPilot")
	fmt.Printf("Режим:   %s\n", st.Mode)
	fmt.Printf("Ходы:    %d идут\n", active)
	fmt.Printf("Очередь: %d\n", len(q))
	fmt.Printf("Веб:     %s\n", cfg.Web.PublicURL)
	return nil
}
