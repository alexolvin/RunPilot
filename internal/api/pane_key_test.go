package api

// PaneKey (узел + tmux pane_id) как глобальный идентификатор панели
// координатора: два узла с одним host и разными os_user имеют САМЫЕ tmux-
// сервера, где первая панель у обоих %0. Тесты фиксируют, что координатор
// не путает панели разных узлов с одинаковым pane_id.

import (
	"context"
	"sync"
	"testing"
	"time"

	"runpilot/internal/clock"
	"runpilot/internal/model"
	"runpilot/internal/proto"
)

// autoReplyConn — фейковое соединение узла: пишет команды в sent и сразу
// шлёт reply SENT (чтобы hub.Dispatch не ждал реального ответа).
type autoReplyConn struct {
	hub  *Hub
	mu   sync.Mutex
	sent []proto.Msg
}

func (c *autoReplyConn) WriteJSON(v any) error {
	m, _ := v.(proto.Msg)
	c.mu.Lock()
	c.sent = append(c.sent, m)
	c.mu.Unlock()
	if m.CmdID != "" {
		reply := proto.New(proto.KindReply)
		reply.CmdID, reply.Result = m.CmdID, proto.ResSent
		go c.hub.DeliverReply(reply)
	}
	return nil
}

func (c *autoReplyConn) WriteControl(int, []byte, time.Time) error { return nil }

func (c *autoReplyConn) last() (proto.Msg, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.sent) == 0 {
		return proto.Msg{}, false
	}
	return c.sent[len(c.sent)-1], true
}

// TestPaneKeyTwoNodesSamePaneID — два узла (host:u1, host:u2), у обоих первая
// панель %0: SID-роутинг и dispatch идут на ПРАВИЛЬНЫЙ узел.
func TestPaneKeyTwoNodesSamePaneID(t *testing.T) {
	h := NewHub(clock.NewReal())
	c1 := &autoReplyConn{hub: h}
	c2 := &autoReplyConn{hub: h}
	h.Register(&NodeInfo{Host: "big", OsUser: "u1", HostIP: "127.0.0.1", Conn: c1})
	h.Register(&NodeInfo{Host: "big", OsUser: "u2", HostIP: "127.0.0.1", Conn: c2})

	// Оба узла: первая панель %0, разные SID.
	h.SetPane("big:u1", proto.Pane{PaneID: "%0", SID: "A", State: "IDLE"})
	h.SetPane("big:u2", proto.Pane{PaneID: "%0", SID: "B", State: "IDLE"})

	// SID → правильный узел.
	ia, ok := h.PaneBySID("A")
	if !ok || ia.Host != "big:u1" {
		t.Fatalf("PaneBySID(A) = %v ok=%v, хочу узел big:u1", ia, ok)
	}
	ib, ok := h.PaneBySID("B")
	if !ok || ib.Host != "big:u2" {
		t.Fatalf("PaneBySID(B) = %v ok=%v, хочу узел big:u2", ib, ok)
	}

	// Dispatch по SID роутится на правильный узел.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	mA := proto.New(proto.KindDispatch)
	mA.SID, mA.PaneID = "A", "%0"
	if _, err := h.Dispatch(ctx, model.PaneKeyOf("big:u1", "%0"), mA); err != nil {
		t.Fatalf("dispatch A: %v", err)
	}
	mB := proto.New(proto.KindDispatch)
	mB.SID, mB.PaneID = "B", "%0"
	if _, err := h.Dispatch(ctx, model.PaneKeyOf("big:u2", "%0"), mB); err != nil {
		t.Fatalf("dispatch B: %v", err)
	}
	la, _ := c1.last()
	lb, _ := c2.last()
	if la.SID != "A" {
		t.Fatalf("узел u1 получил SID %q, хочу A", la.SID)
	}
	if lb.SID != "B" {
		t.Fatalf("узел u2 получил SID %q, хочу B", lb.SID)
	}
}

// TestDropPanesOfHostRemovesAbsent — полный скан узла без панели: снимок
// снят, SID возвращён (PaneGone).
func TestDropPanesOfHostRemovesAbsent(t *testing.T) {
	h := NewHub(clock.NewReal())
	h.SetPane("n1", proto.Pane{PaneID: "%1", SID: "S1", State: "IDLE"})
	h.SetPane("n1", proto.Pane{PaneID: "%2", SID: "S2", State: "IDLE"})

	// Новый полный список: только %2. %1 исчез.
	gone := h.DropPanesOfHost("n1", map[string]bool{"%2": true})
	if len(gone) != 1 || gone[0] != "S1" {
		t.Fatalf("gone = %v, хочу [S1]", gone)
	}
	if _, ok := h.PaneBySID("S1"); ok {
		t.Fatal("S1 не снят после исчезновения панели из скана")
	}
	if _, ok := h.PaneBySID("S2"); !ok {
		t.Fatal("S2 снят, хотя панель в новом скане")
	}
}

// TestDisconnectDropsNodePanes — отключение узла: все его панели сняты
// (DropPanesOfHost с пустым newIDs), панели других узлов не тронуты.
func TestDisconnectDropsNodePanes(t *testing.T) {
	h := NewHub(clock.NewReal())
	h.SetPane("n1", proto.Pane{PaneID: "%0", SID: "A", State: "IDLE"})
	h.SetPane("n2", proto.Pane{PaneID: "%0", SID: "B", State: "IDLE"})

	// Эмуляция defer-блока отключения n1.
	h.Unregister("n1")
	h.DropPanesOfHost("n1", map[string]bool{})

	if _, ok := h.PaneBySID("A"); ok {
		t.Fatal("панель отключённого узла осталась в реестре")
	}
	if _, ok := h.PaneBySID("B"); !ok {
		t.Fatal("панель другого узла снята при отключении n1")
	}
}
