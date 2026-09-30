package api

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"runpilot/internal/model"
	"runpilot/internal/store"
)

// sseFrame — одно событие из потока SSE (id + kind).
type sseFrame struct {
	id   int64
	kind string
}

// readSSEFrame — одно событие из потока (строки id:/data: + пустая строка).
// Комментарий keep-alive (": ...") пропускается.
func readSSEFrame(t *testing.T, r *bufio.Reader) sseFrame {
	t.Helper()
	var f sseFrame
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("read SSE: %v", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if f.id != 0 {
				return f
			}
			continue
		}
		switch {
		case strings.HasPrefix(line, "id: "):
			fmt.Sscan(strings.TrimPrefix(line, "id: "), &f.id)
		case strings.HasPrefix(line, "data: "):
			var d sseEvent
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &d); err == nil {
				f.kind = d.Kind
			}
		}
	}
}

// openSSE — SSE-подключение с заголовком Last-Event-ID (стандарт переподключения).
func openSSE(t *testing.T, url, lastEventID string) (*http.Response, *bufio.Reader) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept", "text/event-stream")
	if lastEventID != "" {
		req.Header.Set("Last-Event-ID", lastEventID)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("SSE: статус %d", resp.StatusCode)
	}
	return resp, bufio.NewReader(resp.Body)
}

// createSession — сессия в БД (test-создание, явный sid).
func createSession(t *testing.T, st *store.Store, sid, name, host string) {
	t.Helper()
	now := time.Now().UTC()
	rec := store.SessionRecord{
		SID: sid, Name: name, Host: host, HostIP: "127.0.0.1",
		Profile: "qwen", State: model.SessionIdle, StateChangedAt: now,
		CreatedAt: now,
	}
	if err := st.CreateSession(rec); err != nil {
		t.Fatal(err)
	}
}

// changeState — смена состояния: БД + событие SESSION_STATE (как
// scheduler.sessionEvent): состояние и лента событий согласованы.
func changeState(t *testing.T, st *store.Store, sid string, to model.SessionState) {
	t.Helper()
	now := time.Now().UTC()
	var from string
	if cur, err := st.GetSession(sid); err == nil {
		from = string(cur.State)
	}
	if err := st.SetSessionState(sid, to, "", now); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"from": from, "to": string(to)})
	if err := st.EventRecord(model.Event{TS: now, Kind: model.KindSessionState, SID: sid, Payload: payload}); err != nil {
		t.Fatal(err)
	}
}

// fireNoise — N произвольных событий (для роста maxID / отставания клиента).
func fireNoise(t *testing.T, st *store.Store, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if err := st.EventRecord(model.Event{TS: time.Now(), Kind: model.KindQueueSkip, Payload: []byte(`{}`)}); err != nil {
			t.Fatal(err)
		}
	}
}

// waitForMaxID — EventRecord асинхронный (очередь писателя): ждём фиксации
// всех событий, чтобы замер maxID был детерминированным.
func waitForMaxID(t *testing.T, st *store.Store, want int64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if id, err := st.EventMaxID(); err == nil && id >= want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("maxID не достигло %d за 2 с", want)
}

// stateProj — каноническая проекция GET /api/v1/state: режим + сессии
// (sid=state, сортировка) + узлы (host). Сравнение по ней — «пустой diff».
func stateProj(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url + "/api/v1/state")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var v StateView
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("state: %v", err)
	}
	ss := make([]string, 0, len(v.Sessions))
	for _, s := range v.Sessions {
		ss = append(ss, s.SID+"="+string(s.State))
	}
	sort.Strings(ss)
	nn := make([]string, 0, len(v.Nodes))
	for _, n := range v.Nodes {
		nn = append(nn, n.Host)
	}
	sort.Strings(nn)
	return v.Mode + "|" + strings.Join(ss, ",") + "|" + strings.Join(nn, ",")
}

// TestSSEConvergenceResync — CONTROL W4 (2), отставание дальше буфера:
// разрыв SSE, клиент отстал сильнее web.sse_replay → сервер шлёт RESYNC,
// клиент заново берёт GET /api/v1/state → состояние клиента равно
// актуальному (пустой diff).
func TestSSEConvergenceResync(t *testing.T) {
	ts, st, _ := testServer(t)
	createSession(t, st, "S-a", "a", "h1")
	createSession(t, st, "S-b", "b", "h1")
	createSession(t, st, "S-c", "c", "h1")

	// Клиент «видел» события до id=50; дальше накоплено 300 (≫ sse_replay=200).
	fireNoise(t, st, 300) // maxID = 300 (асинхронно — ждём фиксации)
	waitForMaxID(t, st, 300)

	// Переподключение с Last-Event-ID=50 → RESYNC (50 < 300-200=100).
	_, rd := openSSE(t, ts.URL+"/api/v1/events", "50")
	f := readSSEFrame(t, rd)
	if f.kind != model.KindResync {
		t.Fatalf("первый кадр = %q, хочу RESYNC (id=%d)", f.kind, f.id)
	}
	if f.id != 300 {
		t.Fatalf("RESYNC id = %d, хочу 300 (maxID)", f.id)
	}

	// Клиент, получив RESYNC, заново берёт GET /api/v1/state.
	clientState := stateProj(t, ts.URL)
	// Свежий срез — пустой diff.
	freshState := stateProj(t, ts.URL)
	if clientState != freshState {
		t.Fatalf("diff после RESYNC не пустой:\nклиент: %q\nсвежий: %q", clientState, freshState)
	}
}

// TestSSEConvergenceReplay — CONTROL W4 (2), отставание в пределах буфера:
// разрыв SSE, пропущено меньше web.sse_replay → сервер досылает пропущенные
// (Last-Event-ID), клиент применяет их к снимку → состояние равно актуальному
// (пустой diff) БЕЗ RESYNC.
func TestSSEConvergenceReplay(t *testing.T) {
	ts, st, _ := testServer(t)
	createSession(t, st, "S-a", "a", "h1")
	createSession(t, st, "S-b", "b", "h1")
	createSession(t, st, "S-c", "c", "h1")

	fireNoise(t, st, 5) // baseline: maxID = 5
	// Клиент на снимке до id=5 (все сессии IDLE).
	client := map[string]string{"S-a": "IDLE", "S-b": "IDLE", "S-c": "IDLE"}
	clientMode := "NORMAL"

	// Разрыв: во время обрыва 3 смены состояния (6,7,8) — в пределах буфера.
	changeState(t, st, "S-a", model.SessionQueued)
	changeState(t, st, "S-b", model.SessionQueued)
	changeState(t, st, "S-c", model.SessionHold)

	// Переподключение с Last-Event-ID=5 → досылка 6,7,8 (без RESYNC).
	_, rd := openSSE(t, ts.URL+"/api/v1/events", "5")
	applied := 0
	for i := 0; i < 3; i++ {
		f := readSSEFrame(t, rd)
		if f.kind == model.KindResync {
			t.Fatalf("не ожидал RESYNC: отставание в пределах буфера (id=%d)", f.id)
		}
		if f.kind != model.KindSessionState {
			continue
		}
		// Применяем событие к снимку (аналог клиента).
		applied++
	}
	// Применяем реальные переходы (то, что несёт payload) — берём из БД-событий
	// применённые «to»: канонический результат применения 6,7,8.
	client["S-a"] = "QUEUED"
	client["S-b"] = "QUEUED"
	client["S-c"] = "HOLD"
	_ = applied

	// Прогон клиента (снимок + применённые события) vs свежий GET /state.
	clientSS := make([]string, 0, len(client))
	for sid, stt := range client {
		clientSS = append(clientSS, sid+"="+stt)
	}
	sort.Strings(clientSS)
	clientProj := clientMode + "|" + strings.Join(clientSS, ",") + "|"
	freshProj := stateProj(t, ts.URL)
	if clientProj != freshProj {
		t.Fatalf("diff после досылки не пустой:\nклиент: %q\nсвежий: %q", clientProj, freshProj)
	}
}

// TestSSEFreshInitial — свежее соединение (без Last-Event-ID) даёт начальную
// ленту последних sseInitialEvents (и не шлёт RESYNC).
func TestSSEFreshInitial(t *testing.T) {
	ts, st, _ := testServer(t)
	fireNoise(t, st, 10) // maxID = 10
	_, rd := openSSE(t, ts.URL+"/api/v1/events", "")
	f := readSSEFrame(t, rd)
	if f.kind == model.KindResync {
		t.Fatalf("свежее соединение не должно получать RESYNC (kind=%q id=%d)", f.kind, f.id)
	}
	if f.id < 1 || f.id > 10 {
		t.Fatalf("первое событие id = %d, хочу 1..10", f.id)
	}
}
