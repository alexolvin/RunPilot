package main

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"runpilot/internal/doctor"
)

// errDoctorFail — хотя бы один FAIL; main преобразует в код выхода 1.
var errDoctorFail = errors.New("doctor: есть FAIL")

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Построчные проверки PASS/FAIL/WARN; код 1 при любом FAIL",
		RunE: func(cmd *cobra.Command, args []string) error {
			results := doctor.Run(&doctor.Context{Cfg: cfg, CfgPath: resolvedPath}, doctor.DefaultChecks())
			for _, r := range results {
				fmt.Println(r)
			}
			if doctor.HasFail(results) {
				return errDoctorFail
			}
			return nil
		},
	}
}
