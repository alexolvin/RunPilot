package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"runpilot/internal/clock"
	"runpilot/internal/config"
	"runpilot/internal/model"
	"runpilot/internal/proto"
	"runpilot/internal/scheduler"
	"runpilot/internal/store"
)

// hubPaneSource — scheduler.PaneSource поверх РЕАЛЬНОГО хаба. Тест валиден
// только если планировщик читает InputEmpty живо из хаба (а не из фейка):
// иначе проверка «ввод пуст» не срабатывает и тест проваливался бы тривиально.
type hubPaneSource struct{ h *Hub }

func snapOf(p *PaneInfo) scheduler.PaneSnap {
	return scheduler.PaneSnap{
		PaneID: p.Pane.PaneID, SID: p.Pane.SID,
		State: model.PaneState(p.Pane.State), Hash: p.Pane.Hash,
		InputEmpty: p.Pane.InputEmpty, ReceivedAt: p.ReceivedAt, Host: p.Host,
	}
}

func (a hubPaneSource) PaneBySID(sid string) (scheduler.PaneSnap, bool) {
	p, ok := a.h.PaneBySID(sid)
	if !ok {
		return scheduler.PaneSnap{}, false
	}
	return snapOf(p), true
}

func (a hubPaneSource) PaneByPaneID(paneID string) (scheduler.PaneSnap, bool) {
	p, ok := a.h.Pane(paneID)
	if !ok {
		return scheduler.PaneSnap{}, false
	}
	return snapOf(p), true
}

// Staleness race (enqueue «ввод пуст»): после успешного paste снимок
// InputEmpty обязан быть обновлён ДО Enqueue, иначе планировщик читает
// устаревшее InputEmpty=true (снимок иначе освежается только по периодическому
// циклу узла KindPanes) и отклоняет постановку в очередь.
func TestPasteEnqueueStaleInput(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "runpilot.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	cfg := config.Defaults()
	clk := clock.NewReal()
	hub := NewHub(clk)
	srv := NewServer(cfg, st, hub, clk, slog.New(slog.NewTextHandler(io.Discard, nil)))
	sch := scheduler.New(scheduler.Options{
		Cfg: cfg, Store: st, Clk: clk,
		Servers: &apiFakeServers{}, Panes: hubPaneSource{h: hub},
		Dispatch: func(context.Context, string, proto.Msg) (proto.Msg, error) {
			return proto.Msg{}, errors.New("off")
		},
		ResumeText: "continue", NoAsyncDispatch: true,
	})
	srv.SetScheduler(sch)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	host := "n-stale"
	status, m := doJSON(t, "POST", ts.URL+"/api/v1/run",
		map[string]any{"name": "stalejob", "host": host, "host_ip": "127.0.0.1", "profile": "qwen"})
	if status != http.StatusCreated {
		t.Fatalf("run: %d %v", status, m)
	}
	sid := m["sid"]

	// Узел (отвечает PASTED) + панель IDLE с пустым вводом.
	hub.Register(&NodeInfo{Host: host, HostIP: "127.0.0.1", RUNPILOTVersion: "v", Conn: &r5Conn{hub: hub}})
	hub.SetPane(host, proto.Pane{PaneID: "%77", SID: sid, State: "IDLE", InputEmpty: true, Hash: 1})
	// Планировщик знает панель как IDLE (как после цикла KindPanes).
	sch.PaneUpdate("%77", sid, model.PaneIdle, 1, clk.Now())

	// enqueue=true: вставка → Enqueue. Без фикса — «ввод пуст».
	out, err := srv.CmdPaste(context.Background(), sid, "задача", true)
	if err != nil {
		t.Fatal(err)
	}
	if out.Code == "ENQUEUE" {
		t.Fatalf("staleness race не исправлена: %s %s", out.Code, out.Text)
	}
	if out.Code != "OK" {
		t.Fatalf("paste+enqueue code = %s (%s), хочу OK", out.Code, out.Text)
	}
	// Реальный эффект: запись в очереди создана.
	if _, err := st.QueueGet(sid); err != nil {
		t.Fatalf("запись в очереди не создана: %v", err)
	}
}
