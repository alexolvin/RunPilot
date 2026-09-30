package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"runpilot/internal/config"
	"runpilot/internal/model"
)

func openTest(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "runpilot.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, path
}

func TestMigrateAndSchemaVersion(t *testing.T) {
	s, _ := openTest(t)
	v, err := s.SchemaVersion()
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	// 001_init … 006_meta, 007_session_kind, 008_job_heartbeat,
	// 009_external_process, 010_turn_session_name.
	if v != 10 {
		t.Fatalf("schema_version = %d, ждём 10", v)
	}
	// Повторное открытие — идемпотентно.
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}

// K6: fireWriteErr алертит только на ошибки ЗАПИСИ, не на логические
// (no-rows / закрыто).
func TestFireWriteErrClassification(t *testing.T) {
	s, _ := openTest(t)
	defer s.Close()
	fired := make(chan struct{}, 1)
	s.SetWriteErrorHook(func(error) { fired <- struct{}{} })
	// Логические ошибки — не алертят (горутину не спавнят).
	s.fireWriteErr(fmt.Errorf("%w: 0 строк", ErrNotFound))
	s.fireWriteErr(ErrNotFound)
	s.fireWriteErr(ErrClosed)
	s.fireWriteErr(nil)
	select {
	case <-fired:
		t.Fatal("логическая ошибка алертнула, а не должна")
	default:
	}
	// Ошибка записи — алертит (асинхронно).
	s.fireWriteErr(errors.New("SQLITE_FULL: no free space"))
	select {
	case <-fired:
	case <-time.After(2 * time.Second):
		t.Fatal("ошибка записи не алертнула за 2 с")
	}
}

func TestOpenIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runpilot.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("повторное Open: %v", err)
	}
	defer func() { _ = s2.Close() }()
	if v, _ := s2.SchemaVersion(); v != 10 {
		t.Fatalf("schema_version после повторного Open = %d (хотим 10)", v)
	}
}

func TestFileMode0600(t *testing.T) {
	_, path := openTest(t)
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if m := fi.Mode().Perm(); m != 0o600 {
		t.Fatalf("режим файла БД = %o, ждём 0600", m)
	}
}

func TestDoWriteSessionRoundtrip(t *testing.T) {
	s, _ := openTest(t)
	now := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	sid := "AAAAABBBBB"
	err := s.DoWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO session (
			sid, name, host, host_ip, tmux_session, window, pane_id, profile,
			agent_version, state, state_changed_at, class, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			sid, "api-refactor", "node-b", "192.0.2.5", "api-refactor", 0, "%12", "qwen",
			"0.0.1", model.SessionIdle, model.FormatTime(now), int(model.ClassNormal), model.FormatTime(now),
		)
		return err
	})
	if err != nil {
		t.Fatalf("DoWrite: %v", err)
	}
	var (
		name, host, state, changed string
		class                      int
	)
	err = s.DB().QueryRow(
		`SELECT name, host, state, state_changed_at, class FROM session WHERE sid = ?`, sid,
	).Scan(&name, &host, &state, &changed, &class)
	if err != nil {
		t.Fatalf("чтение: %v", err)
	}
	if name != "api-refactor" || host != "node-b" || state != string(model.SessionIdle) {
		t.Fatalf("roundtrip: %q %q %q", name, host, state)
	}
	if class != int(model.ClassNormal) {
		t.Fatalf("class = %d", class)
	}
}

func TestNameUniquenessAmongLive(t *testing.T) {
	s, _ := openTest(t)
	now := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	insert := func(sid, name string, state model.SessionState) error {
		return s.DoWrite(func(tx *sql.Tx) error {
			_, err := tx.Exec(`INSERT INTO session (
				sid, name, host, host_ip, tmux_session, window, pane_id, profile,
				state, state_changed_at, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				sid, name, "h", "192.0.2.1", name, 0, "%1", "qwen",
				state, model.FormatTime(now), model.FormatTime(now),
			)
			return err
		})
	}
	if err := insert("AAAABBBBBB", "coder", model.SessionIdle); err != nil {
		t.Fatal(err)
	}
	if err := insert("BBBCCCCCCC", "coder", model.SessionIdle); err == nil {
		t.Fatal("две живые сессии с одним именем: ожидался UNIQUE-конфликт")
	}
	// GONE-запись с тем же именем допустима.
	if err := insert("CCCDDDDDDD", "coder", model.SessionGone); err != nil {
		t.Fatalf("GONE-дубликат имени: %v", err)
	}
}

func TestQueueWriteThenClose(t *testing.T) {
	s, _ := openTest(t)
	now := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		if err := s.QueueWrite(func(tx *sql.Tx) error {
			_, err := tx.Exec(`INSERT INTO event (ts, kind) VALUES (?, ?)`,
				model.FormatTime(now), "TEST")
			return err
		}); err != nil {
			t.Fatalf("QueueWrite %d: %v", i, err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Записи после Close отклоняются.
	if err := s.DoWrite(func(tx *sql.Tx) error { return nil }); err != ErrClosed {
		t.Fatalf("DoWrite после Close: %v", err)
	}
}

func TestPurge(t *testing.T) {
	s, _ := openTest(t)
	now := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	old := now.AddDate(0, 0, -40)
	fresh := now.AddDate(0, 0, -1)

	err := s.DoWrite(func(tx *sql.Tx) error {
		// Сессии: одна GONE старая, одна GONE свежая, одна живая старая.
		for _, sc := range []struct {
			sid, state string
			at         time.Time
		}{
			{"GGGGGGGGG1", string(model.SessionGone), old},
			{"GGGGGGGGG2", string(model.SessionGone), fresh},
			{"LLLLLLLLL1", string(model.SessionIdle), old},
		} {
			if _, err := tx.Exec(`INSERT INTO session (
				sid, name, host, host_ip, tmux_session, window, pane_id, profile,
				state, state_changed_at, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				sc.sid, sc.sid, "h", "192.0.2.1", sc.sid, 0, "%1", "qwen",
				sc.state, model.FormatTime(sc.at), model.FormatTime(sc.at),
			); err != nil {
				return err
			}
		}
		for _, at := range []time.Time{old, fresh} {
			if _, err := tx.Exec(
				`INSERT INTO request (sid, turn_id, server, path, status, t_start)
				 VALUES (?, ?, ?, ?, ?, ?)`,
				"LLLLLLLLL1", 1, "srv", "/v1/chat/completions", 200, model.FormatTime(at),
			); err != nil {
				return err
			}
			if _, err := tx.Exec(`INSERT INTO event (ts, kind) VALUES (?, ?)`,
				model.FormatTime(at), "TURN_END"); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	ret := config.Retention{RequestsDays: 30, EventsDays: 30, GoneSessionsDays: 7}
	out, err := s.Purge(now, ret)
	if err != nil {
		t.Fatalf("Purge: %v", err)
	}
	if out.Requests != 1 || out.Events != 1 || out.Sessions != 1 {
		t.Fatalf("Purge = %+v, ждём {1 1 1}", out)
	}

	count := func(q string) int {
		var n int
		if err := s.DB().QueryRow(q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count(`SELECT COUNT(*) FROM session WHERE sid = 'GGGGGGGGG1'`); n != 0 {
		t.Fatal("старая GONE не удалена")
	}
	if n := count(`SELECT COUNT(*) FROM session WHERE sid = 'GGGGGGGGG2'`); n != 1 {
		t.Fatal("свежая GONE удалена")
	}
	if n := count(`SELECT COUNT(*) FROM session WHERE sid = 'LLLLLLLLL1'`); n != 1 {
		t.Fatal("живая сессия удалена")
	}
	var nReq int
	if err := s.DB().QueryRow(
		`SELECT COUNT(*) FROM request WHERE t_start >= ?`, model.FormatTime(fresh),
	).Scan(&nReq); err != nil {
		t.Fatal(err)
	}
	if nReq != 1 {
		t.Fatal("свежие request удалены")
	}
}

func TestTimeFormatOrdering(t *testing.T) {
	a := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	b := a.Add(500 * time.Millisecond)
	// Секунда и секунда+500мс: лексикографически порядок обязан совпасть с хронологией.
	if !sort.StringsAreSorted([]string{model.FormatTime(a), model.FormatTime(b)}) {
		t.Fatalf("порядок: %q vs %q", model.FormatTime(a), model.FormatTime(b))
	}
	rt, err := model.ParseTime(model.FormatTime(b))
	if err != nil || !rt.Equal(b) {
		t.Fatalf("ParseTime: %v, %v", rt, err)
	}
}
