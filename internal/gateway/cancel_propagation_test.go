package gateway

// Разрыв до апстрима (раздел 6 ТЗ): и обрыв клиента, и CancelSID закрывают
// соединение с апстримом немедленно (fakellm фиксирует разрыв ≤ 100 мс).
// Длинный поток (chunks=1000, chunkPause=50 мс → ~50 с): короткий поток
// доказательством не считается (оператор).
import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"runpilot/internal/config"
	"runpilot/internal/model"
	"runpilot/internal/store"
	"runpilot/internal/testutil/fakellm"
)

const (
	longChunks   = 1000
	longChunkMs  = 50
	breakBudget  = 100 * time.Millisecond
)

// startLongStream — сессия RUNNING + аренда + потоковый запрос в полёте;
// возвращает sid и канал завершения обработчика шлюза (handlerDone).
func startLongStream(t *testing.T, st *store.Store, gw *Gateway, ts string) (string, <-chan struct{}) {
	t.Helper()
	s := mkSession(t, st, model.SessionRunning)
	if _, err := st.LeaseCreate(model.Lease{
		SID: s, Server: "a", Slot: 1, State: model.LeaseActive,
		Origin: model.LeaseOriginDispatch, GrantedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	handlerDone := make(chan struct{})
	go func() {
		resp, err := http.Post(ts+"/s/"+s+"/v1/chat/completions",
			"application/json", strings.NewReader(chatBody("runpilot", `"stream":true`)))
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
		close(handlerDone)
	}()
	waitInflight(t, gw, s)
	return s, handlerDone
}

// waitInflight — дождаться, пока запрос войдёт в inflight(sid)>0.
func waitInflight(t *testing.T, gw *Gateway, s string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		gw.im.Lock()
		n := gw.bySID[s]
		gw.im.Unlock()
		if n > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("запрос не вошёл в inflight")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// waitDisconnect — дождаться фиксации разрыва fakellm; возвращает момент
// разрыва и время ожидания (для проверки бюджета breakBudget).
func waitDisconnect(t *testing.T, a *fakellm.Server, tTrigger time.Time) (time.Time, time.Duration) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for a.Disconnects() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("fakellm не зафиксировал разрыв соединения")
		}
		time.Sleep(2 * time.Millisecond)
	}
	tBreak := a.LastDisconnect()
	return tBreak, tBreak.Sub(tTrigger)
}

// TestClientKillBreaksStream — клиент убит посреди потока (TCP-разрыв):
// fakellm фиксирует разрыв ≤ breakBudget. Сырое TCP-подключение — имитация
// kill -9 кодера (ядро закрывает сокет).
func TestClientKillBreaksStream(t *testing.T) {
	a := fakellm.Start("srv-model")
	defer a.Close()
	a.SetDelays(0, longChunkMs*time.Millisecond, longChunks)
	t.Setenv("RUNPILOT_TEST_KEY", "secret-key")
	st, gw, ts := testEnv(t, []config.Server{srvCfg("a", 10, 1, a.URL, "RUNPILOT_TEST_KEY")})

	s := mkSession(t, st, model.SessionRunning)
	if _, err := st.LeaseCreate(model.Lease{
		SID: s, Server: "a", Slot: 1, State: model.LeaseActive,
		Origin: model.LeaseOriginDispatch, GrantedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	// Сырое TCP-подключение: POST + чтение части потока + разрыв.
	conn, err := net.Dial("tcp", strings.TrimPrefix(ts.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	host := strings.TrimPrefix(ts.URL, "http://")
	req := fmt.Sprintf("POST /s/%s/v1/chat/completions HTTP/1.1\r\nHost: %s\r\nContent-Type: application/json\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s",
		s, host, len(chatBody("runpilot", `"stream":true`)), chatBody("runpilot", `"stream":true`))
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatal(err)
	}
	// Дождаться, пока запрос войдёт в inflight и пойдёт поток.
	waitInflight(t, gw, s)
	// Прочитать часть потока (заголовок + хотя бы один чанк).
	br := bufio.NewReader(conn)
	deadline := time.Now().Add(3 * time.Second)
	sawChunk := false
	for time.Now().Before(deadline) {
		line, err := br.ReadString('\n')
		if err != nil {
			break
		}
		if strings.HasPrefix(line, "data: {") {
			sawChunk = true
			break
		}
	}
	if !sawChunk {
		t.Fatal("поток не пошёл (не прочитан ни один чанк)")
	}

	// Убийство клиента: разрыв TCP.
	tKill := time.Now()
	_ = conn.Close()

	tBreak, dt := waitDisconnect(t, a, tKill)
	if dt > breakBudget {
		t.Errorf("разрыв апстрима через %v после убийства клиента (бюджет %v)", dt, breakBudget)
	}
	t.Logf("клиент убит → разрыв апстрима за %v (tBreak=%s)", dt, tBreak)

	// Обработчик шлюза завершён, inflight(sid)=0, аренда снята.
	deadline = time.Now().Add(3 * time.Second)
	for {
		gw.im.Lock()
		n := gw.bySID[s]
		gw.im.Unlock()
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("inflight(sid) не дошёл до 0 после убийства клиента")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestCancelSIDBreaksStreamLiveClient — CancelSID при живом клиенте:
// fakellm фиксирует разрыв ≤ breakBudget, обработчик завершён, inflight=0.
func TestCancelSIDBreaksStreamLiveClient(t *testing.T) {
	a := fakellm.Start("srv-model")
	defer a.Close()
	a.SetDelays(0, longChunkMs*time.Millisecond, longChunks)
	t.Setenv("RUNPILOT_TEST_KEY", "secret-key")
	st, gw, ts := testEnv(t, []config.Server{srvCfg("a", 10, 1, a.URL, "RUNPILOT_TEST_KEY")})

	s, handlerDone := startLongStream(t, st, gw, ts.URL)

	tCancel := time.Now()
	gw.CancelSID(s)

	tBreak, dt := waitDisconnect(t, a, tCancel)
	if dt > breakBudget {
		t.Errorf("разрыв апстрима через %v после CancelSID (бюджет %v)", dt, breakBudget)
	}
	t.Logf("CancelSID → разрыв апстрима за %v (tBreak=%s)", dt, tBreak)

	// Обработчик шлюза завершён.
	select {
	case <-handlerDone:
	case <-time.After(3 * time.Second):
		t.Fatal("обработчик шлюза не завершён после CancelSID")
	}
	// inflight(sid)=0.
	deadline := time.Now().Add(3 * time.Second)
	for {
		gw.im.Lock()
		n := gw.bySID[s]
		gw.im.Unlock()
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("inflight(sid) не дошёл до 0 после CancelSID")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestGoneLongStreamRelease — GONE (ReleaseOnGone) на длинном потоке:
// LEASE_RELEASE не раньше разрыва и inflight=0.
func TestGoneLongStreamRelease(t *testing.T) {
	a := fakellm.Start("srv-model")
	defer a.Close()
	a.SetDelays(0, longChunkMs*time.Millisecond, longChunks)
	t.Setenv("RUNPILOT_TEST_KEY", "secret-key")
	st, gw, ts := testEnv(t, []config.Server{srvCfg("a", 10, 1, a.URL, "RUNPILOT_TEST_KEY")})

	s, _ := startLongStream(t, st, gw, ts.URL)

	tGone := time.Now()
	gw.ReleaseOnGone(s, model.ReleaseCrash)

	_, dt := waitDisconnect(t, a, tGone)
	if dt > breakBudget {
		t.Errorf("разрыв апстрима через %v после GONE (бюджет %v)", dt, breakBudget)
	}

	// Аренда снята (inflight=0).
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := st.LeaseGet(s); errors.Is(err, store.ErrNotFound) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("аренда не снята после GONE на длинном потоке")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
