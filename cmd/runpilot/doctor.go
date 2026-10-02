package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"runpilot/internal/doctor"
	"runpilot/internal/store"
)

// errDoctorFail — хотя бы один FAIL; main преобразует в код выхода 1.
var errDoctorFail = errors.New("doctor: есть FAIL")

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Построчные проверки PASS/FAIL/WARN; код 1 при любом FAIL",
		RunE: func(cmd *cobra.Command, args []string) error {
			// v2 раздел 4: рабочие значения (model_alias и пр.) живут в БД,
			// YAML — только bootstrap-значения. Doctor сверяет с живым:
			// best-effort загрузка из БД (нет файла/ошибка — значения YAML).
			if dbPath, err := cfg.DBPath(); err == nil {
				if _, serr := os.Stat(dbPath); serr == nil {
					if st, oerr := store.Open(dbPath); oerr == nil {
						_ = st.LoadWorkingInto(cfg)
						_ = st.Close()
					}
				}
			}
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
