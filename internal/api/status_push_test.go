package api

// П.6(c) — пуш строки статуса/сводки координатор → узлы по изменению текста.
//
// Тесты (задачные):
//  1. смена позиции в очереди → узел получает set_status с НОВЫМ текстом;
//  2. тот же текст повторно не отправляется (кэш last-sent);
//  3. переподключение узла (hello+panes) → переотправка статусов всех его
//     сессий + сводки.
//
// Транспорт — fire-and-forget (Hub.PushPane), ответ узла не ожидается.

import (
	"sync"
	"testing"
	"time"

	"runpilot/internal/clock"
	"runpilot/internal/proto"
)

// recConn — NodeConn, записывающий ВСЕ WriteJSON (fire-and-forset; reply не
// отсылает — пуш не ждёт ответа).
type recConn struct {
	mu   sync.Mutex
	msgs []proto.Msg
}

func (c *recConn) WriteJSON(v any) error {
	m, ok := v.(proto.Msg)
	if !ok {
		return nil
	}
	c.mu.Lock()
	c.msgs = append(c.msgs, m)
	c.mu.Unlock()
	return nil
}

func (c *recConn) WriteControl(int, []byte, time.Time) error { return nil }

// texts — значения Text сообщений заданного kind в порядке отправки.
func (c *recConn) texts(kind proto.Kind) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for _, m := range c.msgs {
		if m.Type == kind {
			out = append(out, m.Text)
		}
	}
	return out
}

// fakeStatusSrc — StatusSource с изменяемыми текстами (симулирует смену
// позиции в очереди / состояния серверов без реального планировщика).
type fakeStatusSrc struct {
	mu      sync.Mutex
	status  string
	summary string
}

func (f *fakeStatusSrc) HeldStatus() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status
}
func (f *fakeStatusSrc) SummaryText() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.summary
}
func (f *fakeStatusSrc) set(status, summary string) {
	f.mu.Lock()
	f.status, f.summary = status, summary
	f.mu.Unlock()
}

// newPusherHarn — hub + узел n1 (recConn) + панели на нём.
func newPusherHarn(t *testing.T, panes ...proto.Pane) (*Hub, *recConn, *fakeStatusSrc) {
	t.Helper()
	hub := NewHub(clock.NewReal())
	conn := &recConn{}
	hub.Register(&NodeInfo{Host: "n1", HostIP: "127.0.0.1", Conn: conn})
	t.Cleanup(func() { hub.Unregister("n1") })
	for _, p := range panes {
		hub.SetPane("n1", p)
	}
	src := &fakeStatusSrc{status: "Q 1/1 held", summary: "s1=UP | Q:1"}
	return hub, conn, src
}

// 1. Смена позиции в очереди (появился второй удерживающий: Q 1/1 → Q 2/2)
//    → узел получает set_status с НОВЫМ текстом.
func TestPusherQueueChangeSendsNewStatus(t *testing.T) {
	hub, conn, src := newPusherHarn(t, proto.Pane{PaneID: "P1", SID: "B"})
	p := NewStatusPusher(hub, src, nil)

	p.PushAll()
	got := conn.texts(proto.KindSetStatus)
	if len(got) != 1 || got[0] != "Q 1/1 held" {
		t.Fatalf("первый пуш: set_status=%v, хочу [\"Q 1/1 held\"]", got)
	}

	// Очередь изменилась: вторая запись стала удерживающей (новая позиция) —
	// N 1→2, M 1→2 → новый текст.
	src.set("Q 2/2 held", "s1=UP | Q:2")
	p.PushAll()
	got = conn.texts(proto.KindSetStatus)
	if len(got) != 2 || got[1] != "Q 2/2 held" {
		t.Fatalf("смена позиции: set_status=%v, хочу 2-е = \"Q 2/2 held\"", got)
	}
}

// 2. Тот же текст повторно не отправляется (кэш last-sent per pane/host).
func TestPusherUnchangedNotResent(t *testing.T) {
	hub, conn, _ := newPusherHarn(t,
		proto.Pane{PaneID: "P1", SID: "B"},
		proto.Pane{PaneID: "P2", SID: "C"},
	)
	p := NewStatusPusher(hub, src0(), nil)

	// Три пуша при неизменном тексте: set_status — по 1 на панель,
	// set_summary — по 1 на хост (а не на каждый пуш).
	p.PushAll()
	p.PushAll()
	p.PushAll()

	if st := conn.texts(proto.KindSetStatus); len(st) != 2 {
		t.Fatalf("неизменённый статус повторяется: %d set_status, хочу 2 (по панели); %v", len(st), st)
	}
	if sm := conn.texts(proto.KindSetSummary); len(sm) != 2 {
		t.Fatalf("неизменённая сводка повторяется: %d set_summary, хочу 2 (по панели хоста); %v", len(sm), sm)
	}
}

// 3. Переподключение узла (hello+panes) → принудительная переотправка
//    set_status + set_summary всем его панелям (кэш обходится).
func TestPusherReconnectResendsAll(t *testing.T) {
	hub, conn1, src := newPusherHarn(t,
		proto.Pane{PaneID: "P1", SID: "B"},
		proto.Pane{PaneID: "P2", SID: "C"},
	)
	p := NewStatusPusher(hub, src, nil)
	p.PushAll()
	if got := conn1.texts(proto.KindSetStatus); len(got) != 2 {
		t.Fatalf("предпосылка: до реконнекта set_status=%d, хочу 2; %v", len(got), got)
	}

	// Узел отключился и переподключился: новый conn, панели пере-сообщены
	// (как в handleNodeWS: Register → первый panes → ResendHost).
	conn2 := &recConn{}
	hub.Register(&NodeInfo{Host: "n1", HostIP: "127.0.0.1", Conn: conn2})
	hub.SetPane("n1", proto.Pane{PaneID: "P1", SID: "B"})
	hub.SetPane("n1", proto.Pane{PaneID: "P2", SID: "C"})
	p.ResendHost("n1")

	// Каждая панель получила set_status + set_summary (принудительно,
	// несмотря на кэш — после реконнекта опция tmux могла потеряться).
	if st := conn2.texts(proto.KindSetStatus); len(st) != 2 {
		t.Fatalf("реконнект: set_status=%d, хочу 2 (все панели); %v", len(st), st)
	}
	if sm := conn2.texts(proto.KindSetSummary); len(sm) != 2 {
		t.Fatalf("реконнект: set_summary=%d, хочу 2 (все панели хоста); %v", len(sm), sm)
	}
	// Старое соединение не тронуто.
	if got := conn1.texts(proto.KindSetStatus); len(got) != 2 {
		t.Fatalf("старый conn после реконнекта: set_status=%d, хочу без изменений 2", len(got))
	}
}

// src0 — фиксированный текст (для теста неизменённого).
func src0() *fakeStatusSrc { return &fakeStatusSrc{status: "Q 1/1 held", summary: "s1=UP | Q:1"} }
