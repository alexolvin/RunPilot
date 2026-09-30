package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"runpilot/internal/clock"
	"runpilot/internal/config"
	"runpilot/internal/proto"
	"runpilot/internal/store"
)

// r5Conn — фейковый транспорт узла: отвечает PASTED на каждую команду.
type r5Conn struct{ hub *Hub }

func (c *r5Conn) WriteJSON(v any) error {
	if m, ok := v.(proto.Msg); ok {
		c.hub.DeliverReply(proto.Msg{CmdID: m.CmdID, Result: proto.ResPasted})
	}
	return nil
}

func (c *r5Conn) WriteControl(int, []byte, time.Time) error { return nil }

// W5 CONTROL 4 (R5): текст задания НЕ сохраняется — уникальная метка из текста
// находится в БД и в журнале 0 раз. Успешный путь: PANE(IDLE, пустой ввод) →
// KindPaste → PASTED → auditPaste (в журнал только bytes/lines/operator).
func TestR5PasteTextNotStored(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "runpilot.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	clk := clock.NewReal()
	hub := NewHub(clk)
	srv := NewServer(cfg, st, hub, clk, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	host := "n-r5"
	status, m := doJSON(t, "POST", ts.URL+"/api/v1/run",
		map[string]any{"name": "r5job", "host": host, "host_ip": "127.0.0.1", "profile": "qwen"})
	if status != http.StatusCreated {
		t.Fatalf("run: %d %v", status, m)
	}
	sid := m["sid"]

	// Узел с фейковым конном (отвечает PASTED) + панель IDLE с пустым вводом.
	hub.Register(&NodeInfo{Host: host, HostIP: "127.0.0.1", RUNPILOTVersion: "v", Conn: &r5Conn{hub: hub}})
	hub.SetPane(host, proto.Pane{PaneID: "%42", SID: sid, State: "IDLE", InputEmpty: true, Hash: 1})

	marker := "RUNPILOTR5MARKER_X7Q9Z"
	out, err := srv.CmdPaste(context.Background(), sid, marker, false)
	if err != nil {
		t.Fatal(err)
	}
	if out.Code != "OK" {
		t.Fatalf("paste code = %s (%s), хочу OK", out.Code, out.Text)
	}

	// 1) Журнал: событие paste записано (EventRecord асинхронен — ждём),
	//    метки в payload нет.
	var havePaste bool
	deadline := time.Now().Add(2 * time.Second)
	for {
		evs, err := st.EventList(0, 100)
		if err != nil {
			t.Fatal(err)
		}
		havePaste = false
		for _, e := range evs {
			if e.Kind == "paste" && e.SID == sid {
				havePaste = true
				if strings.Contains(string(e.Payload), marker) {
					t.Fatalf("R5: метка в payload события: %s", e.Payload)
				}
			}
		}
		if havePaste || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !havePaste {
		t.Fatalf("R5: событие paste не записано — тест тривиален")
	}

	// 2) Сырые файлы БД: метки нет нигде (включая WAL).
	_ = st.Close()
	for _, f := range []string{dbPath, dbPath + "-wal", dbPath + "-shm"} {
		raw, err := os.ReadFile(f)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if n := strings.Count(string(raw), marker); n != 0 {
			t.Fatalf("R5: метка в файле %s — %d раз, хочу 0", filepath.Base(f), n)
		}
	}
}
