package scheduler

// W6 (5.2, CONTROL 7): удаление сервера в режиме after_turns завершается
// тиком, когда у сервера нет активных ходов — срабатывает OnServerIdle.

import (
	"testing"

	"runpilot/internal/model"
)

// TestServerRemovalAfterTurnsIdle — сервер помечен на удаление; когда
// активных ходов нет, тик вызывает onServerIdle (координатор завершает
// удаление: DeleteServer + снять из реестра).
func TestServerRemovalAfterTurnsIdle(t *testing.T) {
	h := newHarn(t, []ServerView{srv("s", 10, 1)})
	var got []string
	h.sch.onServerIdle = func(name string) { got = append(got, name) }

	// Пока есть активная аренда — удаление не завершается.
	h.addSession("s1", "s1", "host", model.SessionRunning, model.ClassNormal, model.NoConstraint)
	if _, err := h.st.LeaseCreate(model.Lease{SID: "s1", Server: "s", Slot: 1,
		State: model.LeaseActive, Origin: model.LeaseOriginImplicit, GrantedAt: h.clk.Now()}); err != nil {
		t.Fatal(err)
	}
	h.sch.MarkServerRemoving("s")
	h.tick()
	if len(got) != 0 {
		t.Fatalf("удаление завершилось при активной аренде: %v", got)
	}

	// Аренда снята → следующий тик завершает удаление.
	if err := h.st.LeaseRelease("s1", "test", h.clk.Now()); err != nil {
		t.Fatal(err)
	}
	h.tick()
	if len(got) != 1 || got[0] != "s" {
		t.Fatalf("onServerIdle = %v, хочу [s]", got)
	}
}
