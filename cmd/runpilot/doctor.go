package main

import (
	"errors"
	"fmt"
	"os"
	"regexp"

	"github.com/spf13/cobra"

	"runpilot/internal/doctor"
	"runpilot/internal/store"
	"runpilot/profiles"
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
			// cmdline_regex профиля кодера — чтобы doctor нашёл ПРОЦЕСС КОДЕРА
			// (потомок pane_pid), а не обёртку. Best-effort: не загрузился → nil
			// (coder_timeout_env деградирует до pane_pid).
			var cmdline *regexp.Regexp
			if prof, perr := profiles.LoadQwen(); perr == nil {
				cmdline = prof.Cmdline()
			}
			results := doctor.Run(&doctor.Context{Cfg: cfg, CfgPath: resolvedPath, Cmdline: cmdline}, doctor.DefaultChecks())
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
