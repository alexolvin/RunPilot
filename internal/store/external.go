package store

import (
	"database/sql"
	"strconv"
	"strings"
	"time"

	"runpilot/internal/model"
)

// ExternalUpsert — upsert внешнего кодера по (host, pid, start_time).
// Возвращает isNew=true при первой записи (первое наблюдение → уведомление).
func (s *Store) ExternalUpsert(p *model.ExternalProcess) (bool, error) {
	isNew := false
	err := s.DoWrite(func(tx *sql.Tx) error {
		var exists int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM external_process WHERE host=? AND pid=? AND start_time=?`,
			p.Host, p.PID, p.StartTime).Scan(&exists); err != nil {
			return err
		}
		isNew = exists == 0
		_, err := tx.Exec(`
INSERT INTO external_process
  (host, pid, uid, start_time, source, exe, flags, target, status, first_seen, last_seen)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(host, pid, start_time) DO UPDATE SET
  uid = excluded.uid,
  source = excluded.source,
  target = excluded.target,
  flags = excluded.flags,
  status = excluded.status,
  last_seen = excluded.last_seen`,
			p.Host, p.PID, p.UID, p.StartTime, p.Source, p.Exe, p.Flags, p.Target,
			p.Status, ts(p.FirstSeen), ts(p.LastSeen))
		return err
	})
	if err != nil {
		return false, err
	}
	return isNew, nil
}

// ExternalList — все внешние кодеры (для web/API).
func (s *Store) ExternalList() ([]model.ExternalProcess, error) {
	rows, err := s.db.Query(`SELECT host, pid, uid, start_time, source, exe, flags, target, status, first_seen, last_seen FROM external_process ORDER BY last_seen DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanExternals(rows)
}

// ExternalListByStatus — внешние кодеры со статусом.
func (s *Store) ExternalListByStatus(status string) ([]model.ExternalProcess, error) {
	rows, err := s.db.Query(`SELECT host, pid, uid, start_time, source, exe, flags, target, status, first_seen, last_seen FROM external_process WHERE status = ? ORDER BY last_seen DESC`, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanExternals(rows)
}

func scanExternals(rows *sql.Rows) ([]model.ExternalProcess, error) {
	var out []model.ExternalProcess
	for rows.Next() {
		var p model.ExternalProcess
		var first, last string
		if err := rows.Scan(&p.Host, &p.PID, &p.UID, &p.StartTime, &p.Source, &p.Exe, &p.Flags, &p.Target, &p.Status, &first, &last); err != nil {
			return nil, err
		}
		p.FirstSeen, _ = time.Parse(time.RFC3339Nano, first)
		p.LastSeen, _ = time.Parse(time.RFC3339Nano, last)
		out = append(out, p)
	}
	return out, rows.Err()
}

// ExternalSetStatus — сменить статус (KILLED/GONE/IGNORED).
func (s *Store) ExternalSetStatus(host string, pid int, startTime int64, status string, at time.Time) error {
	return s.DoWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE external_process SET status = ?, last_seen = ? WHERE host=? AND pid=? AND start_time=?`,
			status, ts(at), host, pid, startTime)
		return err
	})
}

// ExternalMarkGone — для хоста: все ACTIVE-строки, отсутствующие в keep
// (ключ "pid:start"), помечаются GONE (процесс завершился).
func (s *Store) ExternalMarkGone(host string, keep map[string]bool, at time.Time) error {
	return s.DoWrite(func(tx *sql.Tx) error {
		rows, err := tx.Query(`SELECT pid, start_time FROM external_process WHERE host=? AND status = ?`, host, model.ExtActive)
		if err != nil {
			return err
		}
		type key struct{ pid int; start int64 }
		var toGone []key
		for rows.Next() {
			var k key
			if err := rows.Scan(&k.pid, &k.start); err != nil {
				rows.Close()
				return err
			}
			if !keep[extKey(k.pid, k.start)] {
				toGone = append(toGone, k)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, k := range toGone {
			if _, err := tx.Exec(`UPDATE external_process SET status = ?, last_seen = ? WHERE host=? AND pid=? AND start_time=?`,
				model.ExtGone, ts(at), host, k.pid, k.start); err != nil {
				return err
			}
		}
		return nil
	})
}

// ExternalPurge — удалить старые строки (last_seen < cutoff), retention.
func (s *Store) ExternalPurge(cutoff time.Time) error {
	return s.DoWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(`DELETE FROM external_process WHERE last_seen < ?`, ts(cutoff))
		return err
	})
}

// extKey — устойчивый ключ процесса для keep-множества.
func extKey(pid int, start int64) string {
	return strconv.Itoa(pid) + ":" + strconv.FormatInt(start, decimalBase)
}

// IgnoreAdd — добавить правило «Игнорировать»; возвращает id.
func (s *Store) IgnoreAdd(host, exe, flags string, at time.Time) (int, error) {
	var id int
	err := s.DoWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`INSERT INTO ignore_rule (host, exe, flags, created) VALUES (?, ?, ?, ?)`,
			host, exe, flags, ts(at))
		if err != nil {
			return err
		}
		v, err := res.LastInsertId()
		if err != nil {
			return err
		}
		id = int(v)
		return nil
	})
	return id, err
}

// IgnoreList — все правила.
func (s *Store) IgnoreList() ([]model.IgnoreRule, error) {
	rows, err := s.db.Query(`SELECT id, host, exe, flags, created FROM ignore_rule ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.IgnoreRule
	for rows.Next() {
		var r model.IgnoreRule
		var created string
		if err := rows.Scan(&r.ID, &r.Host, &r.Exe, &r.Flags, &created); err != nil {
			return nil, err
		}
		r.Created, _ = time.Parse(time.RFC3339Nano, created)
		out = append(out, r)
	}
	return out, rows.Err()
}

// IgnoreDelete — удалить правило по id.
func (s *Store) IgnoreDelete(id int) error {
	return s.DoWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(`DELETE FROM ignore_rule WHERE id = ?`, id)
		return err
	})
}

// IgnoreMatch — есть ли правило, покрывающее кандидата (host пустой = любое;
// flags: все флаги правила должны входить в flags кандидата).
func (s *Store) IgnoreMatch(host, exe, flags string) bool {
	rules, err := s.IgnoreList()
	if err != nil {
		return false
	}
	for _, r := range rules {
		if r.Host != "" && r.Host != host {
			continue
		}
		if r.Exe != "" && r.Exe != exe {
			continue
		}
		if !flagsContain(flags, r.Flags) {
			continue
		}
		return true
	}
	return false
}

// flagsContain — все флаги из rule входят в candidate (по имени).
func flagsContain(candidate, rule string) bool {
	ruleFlags := strings.Fields(rule)
	if len(ruleFlags) == 0 {
		return true
	}
	set := map[string]bool{}
	for _, f := range strings.Fields(candidate) {
		set[f] = true
	}
	for _, f := range ruleFlags {
		if !set[f] {
			return false
		}
	}
	return true
}
