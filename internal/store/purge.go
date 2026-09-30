package store

import (
	"database/sql"
	"fmt"
	"time"

	"runpilot/internal/config"
	"runpilot/internal/model"
)

// Purged — сколько строк удалено.
type Purged struct {
	Requests int
	Events   int
	Sessions int
}

// Purge — очистка по retention.* (раздел 14 ТЗ).
// GONE-сессии стареют с момента перехода в GONE (state_changed_at).
func (s *Store) Purge(now time.Time, ret config.Retention) (Purged, error) {
	var out Purged
	err := s.DoWrite(func(tx *sql.Tx) error {
		reqCutoff := model.FormatTime(now.AddDate(0, 0, -ret.RequestsDays))
		if res, err := tx.Exec(`DELETE FROM request WHERE t_start < ?`, reqCutoff); err == nil {
			if n, err := res.RowsAffected(); err == nil {
				out.Requests = int(n)
			}
		} else {
			return fmt.Errorf("purge request: %w", err)
		}

		evCutoff := model.FormatTime(now.AddDate(0, 0, -ret.EventsDays))
		if res, err := tx.Exec(`DELETE FROM event WHERE ts < ?`, evCutoff); err == nil {
			if n, err := res.RowsAffected(); err == nil {
				out.Events = int(n)
			}
		} else {
			return fmt.Errorf("purge event: %w", err)
		}

		goneCutoff := model.FormatTime(now.AddDate(0, 0, -ret.GoneSessionsDays))
		if res, err := tx.Exec(
			`DELETE FROM session WHERE state = ? AND state_changed_at < ?`,
			model.SessionGone, goneCutoff,
		); err == nil {
			if n, err := res.RowsAffected(); err == nil {
				out.Sessions = int(n)
			}
		} else {
			return fmt.Errorf("purge session: %w", err)
		}
		return nil
	})
	return out, err
}
