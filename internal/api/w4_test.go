package api

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"runpilot/internal/model"
)

// notificationsList — GET /api/v1/notifications.
func notificationsList(t *testing.T, url string) []json.RawMessage {
	t.Helper()
	resp, err := http.Get(url + "/api/v1/notifications")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out []json.RawMessage
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("notifications: %v (тело %s)", err, raw)
	}
	return out
}

// TestNotificationsDeriveAndResolve — центр уведомлений: сессия в HOLD создаёт
// активное уведомление (WARN), смена состояния на IDLE закрывает его
// (авто-закрытие при исчезновении условия, раздел 17).
func TestNotificationsDeriveAndResolve(t *testing.T) {
	ts, st, _ := testServer(t)
	createSession(t, st, "S-hold", "hold", "h1")

	// В HOLD → активное уведомление session_hold.
	changeState(t, st, "S-hold", model.SessionHold)
	active := notificationsList(t, ts.URL)
	found := false
	for _, raw := range active {
		var n struct {
			Kind     string `json:"kind"`
			Severity string `json:"severity"`
			SID      string `json:"sid"`
		}
		_ = json.Unmarshal(raw, &n)
		if n.Kind == "session_hold" && n.SID == "S-hold" && n.Severity == "WARN" {
			found = true
		}
	}
	if !found {
		t.Fatalf("нет активного уведомления session_hold для S-hold: %s", active)
	}

	// Возврат в IDLE → условие исчезло → уведомление закрыто (не в активных).
	changeState(t, st, "S-hold", model.SessionIdle)
	active = notificationsList(t, ts.URL)
	for _, raw := range active {
		var n struct {
			Kind string `json:"kind"`
			SID  string `json:"sid"`
		}
		_ = json.Unmarshal(raw, &n)
		if n.Kind == "session_hold" && n.SID == "S-hold" {
			t.Fatalf("session_hold должно быть закрыто, но в активных: %s", raw)
		}
	}
}

// TestJournalPagination — журнал: серверная пагинация по id-cursor.
func TestJournalPagination(t *testing.T) {
	ts, st, _ := testServer(t)
	fireNoise(t, st, 120)
	waitForMaxID(t, st, 120) // асинхронные события — ждём фиксации

	total := 0
	cursor := int64(0)
	for page := 0; page < 10; page++ {
		resp, err := http.Get(ts.URL + "/api/v1/journal?kind=events&limit=50&cursor=" +
			itoaCursor(cursor))
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		var p struct {
			Rows []json.RawMessage `json:"rows"`
			Next int64             `json:"next_cursor"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			t.Fatalf("journal: %v (тело %s)", err, raw)
		}
		total += len(p.Rows)
		if len(p.Rows) == 0 {
			break
		}
		if cursor == 0 && len(p.Rows) != 50 {
			t.Fatalf("страница 1: %d строк, хочу 50", len(p.Rows))
		}
		if p.Next == 0 {
			if total != 120 {
				t.Fatalf("итого %d, хочу 120", total)
			}
			break
		}
		cursor = p.Next
	}
	if total != 120 {
		t.Fatalf("страницами пройдено %d строк, хочу 120", total)
	}
}

// TestJournalCSV — экспорт CSV событий.
func TestJournalCSV(t *testing.T) {
	ts, st, _ := testServer(t)
	fireNoise(t, st, 5)
	resp, err := http.Get(ts.URL + "/api/v1/journal?kind=events&format=csv")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/csv; charset=utf-8" {
		t.Fatalf("Content-Type = %q, хочу text/csv", ct)
	}
	raw, _ := io.ReadAll(resp.Body)
	if len(raw) < len("id,ts,kind,sid,server,payload") {
		t.Fatalf("CSV слишком короткий: %s", raw)
	}
}

// itoaCursor — int64 → строка для query (в тестах).
func itoaCursor(v int64) string {
	if v == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	return string(b[i:])
}
