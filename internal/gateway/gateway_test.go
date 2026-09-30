package gateway

import (
	"encoding/json"
	"fmt"
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
	"runpilot/internal/model"
	"runpilot/internal/sid"
	"runpilot/internal/store"
	"runpilot/internal/testutil/fakellm"
)

// --- инфраструктура тестов ---

func testEnv(t *testing.T, servers []config.Server) (*store.Store, *Gateway, *httptest.Server) {
	t.Helper()
	cfg := config.Defaults()
	cfg.Servers = servers
	cfg.Gateway.HoldMaxSec = 1
	cfg.Gateway.RetryAfterSec = 7
	cfg.Gateway.MaxInflight = 2
	cfg.Gateway.MaxBodyMB = 1
	cfg.Coordinator.ShutdownGraceSec = 1
	st, err := store.Open(filepath.Join(t.TempDir(), "runpilot.db"))
	if err != nil {
		t.Fatal(err)
	}
	gw, err := NewGateway(cfg, st, clock.NewReal(),
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		st.Close()
		t.Fatal(err)
	}
	ts := httptest.NewServer(gw.Handler())
	t.Cleanup(func() {
		ts.Close()
		_ = st.Close()
	})
	return st, gw, ts
}

func srvCfg(name string, priority, slots int, url, keyEnv string) config.Server {
	return config.Server{
		Name:                name,
		Priority:            priority,
		Slots:               slots,
		Accept:              []string{"resume", "high", "normal", "low"},
		HealthURL:           url + "/health",
		MaxOutputTokens:     1000,
		FirstByteTimeoutSec: 5,
		Upstreams: config.Upstreams{OpenAI: config.UpstreamOpenAI{
			URL: url, Model: "srv-model", KeyEnv: keyEnv,
		}},
	}
}

func mkSession(t *testing.T, st *store.Store, state model.SessionState) string {
	t.Helper()
	s, err := sid.New()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := st.CreateSession(store.SessionRecord{
		SID: s, Name: "t-" + s, Host: "test", HostIP: "127.0.0.1",
		TmuxSession: "op", PaneID: "%1", Profile: "qwen",
		State: state, StateChangedAt: now, Class: model.ClassNormal,
		ConstraintKind: "none", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	return s
}

func postOpen(t *testing.T, url, body string) *http.Response {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	return resp
}

func postJSON(t *testing.T, url, body string) (*http.Response, string) {
	t.Helper()
	resp := postOpen(t, url, body)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp, string(raw)
}

func chatBody(model string, extra string) string {
	if extra == "" {
		return fmt.Sprintf(`{"model":%q,"max_tokens":10}`, model)
	}
	return fmt.Sprintf(`{"model":%q,%s}`, model, extra)
}

// --- доступ (раздел 6 ТЗ) ---

func TestAccess(t *testing.T) {
	a := fakellm.Start("srv-model")
	defer a.Close()
	t.Setenv("RUNPILOT_TEST_KEY", "secret-key")
	st, _, ts := testEnv(t, []config.Server{srvCfg("a", 10, 1, a.URL, "RUNPILOT_TEST_KEY")})

	ok := mkSession(t, st, model.SessionIdle)
	_ = st.SetSessionState(mkSession(t, st, model.SessionGone), model.SessionGone, "", time.Now())

	// 404: неизвестный sid.
	resp, _ := postJSON(t, ts.URL+"/s/NOPE000000/v1/chat/completions", chatBody("runpilot", ""))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("неизвестный sid: %d, хочу 404", resp.StatusCode)
	}
	// 404: GONE.
	gone := mkSession(t, st, model.SessionIdle)
	_ = st.SetSessionState(gone, model.SessionGone, "", time.Now())
	resp, _ = postJSON(t, ts.URL+"/s/"+gone+"/v1/chat/completions", chatBody("runpilot", ""))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GONE: %d, хочу 404", resp.StatusCode)
	}
	// 403: чужой IP.
	other := mkSession(t, st, model.SessionIdle)
	_, _ = st.DB().Exec(`UPDATE session SET host_ip = '10.9.9.9' WHERE sid = ?`, other)
	resp, _ = postJSON(t, ts.URL+"/s/"+other+"/v1/chat/completions", chatBody("runpilot", ""))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("чужой IP: %d, хочу 403", resp.StatusCode)
	}
	// 200: свой IP.
	resp, _ = postJSON(t, ts.URL+"/s/"+ok+"/v1/chat/completions", chatBody("runpilot", ""))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("свой IP: %d, хочу 200", resp.StatusCode)
	}
}

// --- тело генерирующего запроса (раздел 6 ТЗ) ---

func TestBodyTransform(t *testing.T) {
	a := fakellm.Start("srv-model")
	defer a.Close()
	t.Setenv("RUNPILOT_TEST_KEY", "secret-key")
	st, _, ts := testEnv(t, []config.Server{srvCfg("a", 10, 1, a.URL, "RUNPILOT_TEST_KEY")})
	s := mkSession(t, st, model.SessionIdle)

	// model подменяется, max_tokens ограничен сверху, ключ ставится из
	// key_env; stream_options шлюз НЕ добавляет.
	resp, _ := postJSON(t, ts.URL+"/s/"+s+"/v1/chat/completions",
		`{"model":"runpilot","max_tokens":999999,"max_completion_tokens":5000}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("статус: %d", resp.StatusCode)
	}
	if got := a.LastModel(); got != "srv-model" {
		t.Fatalf("model на апстриме: %q, хочу srv-model", got)
	}
	if a.LastAuth() != "Bearer secret-key" {
		t.Fatalf("Authorization на апстриме: %q", a.LastAuth())
	}
	mt, mct := a.LastMaxTokens()
	if mt != 1000 || mct != 1000 {
		t.Fatalf("лимит max_*: %d/%d, хочу 1000/1000", mt, mct)
	}
	if a.SawStreamOptions() {
		t.Fatal("шлюз добавил stream_options — запрещено разделом 6 ТЗ")
	}
}

func TestBodyLimit413(t *testing.T) {
	a := fakellm.Start("srv-model")
	defer a.Close()
	t.Setenv("RUNPILOT_TEST_KEY", "k")
	st, _, ts := testEnv(t, []config.Server{srvCfg("a", 10, 1, a.URL, "RUNPILOT_TEST_KEY")})
	s := mkSession(t, st, model.SessionIdle)

	big := map[string]any{"model": "runpilot", "messages": make([]string, 0, 2048)}
	for range 2048 {
		big["messages"] = append(big["messages"].( []string), strings.Repeat("x", 512))
	}
	raw, _ := json.Marshal(big)
	resp, _ := postJSON(t, ts.URL+"/s/"+s+"/v1/chat/completions", string(raw))
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("большое тело: %d, хочу 413", resp.StatusCode)
	}
}

// --- ёмкость (раздел 6 ТЗ) ---

// TestMaxInflight503 — превышение gateway.max_inflight: 503 runpilot_no_slot
// БЕЗ удержания (запись в очередь не создаётся).
func TestMaxInflight503(t *testing.T) {
	a := fakellm.Start("srv-model")
	defer a.Close()
	a.SetDelays(300*time.Millisecond, 0, 1)
	t.Setenv("RUNPILOT_TEST_KEY", "k")
	st, gw, ts := testEnv(t, []config.Server{srvCfg("a", 10, 2, a.URL, "RUNPILOT_TEST_KEY")})

	// Два параллельных запроса с одной сессии держат max_inflight=2.
	fly := func(s string) chan int {
		done := make(chan int, 1)
		go func() {
			resp, err := http.Post(ts.URL+"/s/"+s+"/v1/chat/completions",
				"application/json", strings.NewReader(chatBody("runpilot", "")))
			if err != nil {
				done <- -1
				return
			}
			defer resp.Body.Close()
			_, _ = io.Copy(io.Discard, resp.Body)
			done <- resp.StatusCode
		}()
		return done
	}
	s1 := mkSession(t, st, model.SessionIdle)
	s2 := mkSession(t, st, model.SessionIdle)
	d1, d2 := fly(s1), fly(s2)
	time.Sleep(100 * time.Millisecond) // оба в полёте
	if got := gw.InflightByServer("a"); got != 2 {
		t.Fatalf("inflight: %d, хочу 2", got)
	}

	// Третий: лимит достигнут → 503 сразу (без удержания).
	s3 := mkSession(t, st, model.SessionIdle)
	t0 := time.Now()
	resp, body := postJSON(t, ts.URL+"/s/"+s3+"/v1/chat/completions", chatBody("runpilot", ""))
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("лимит: %d, хочу 503", resp.StatusCode)
	}
	if !strings.Contains(body, "queue position") {
		t.Fatalf("тело: %s", body)
	}
	if time.Since(t0) > 100*time.Millisecond {
		t.Fatal("503 по лимиту должен быть немедленным (без удержания)")
	}
	if _, err := st.QueueGet(s3); err == nil {
		t.Fatal("запись очереди при 503 по лимиту запрещена (без удержания)")
	}
	<-d1
	<-d2
}

// --- удержание: тело и код побайтно (раздел 6 ТЗ) ---

func TestHoldBodyExact(t *testing.T) {
	a := fakellm.Start("srv-model")
	defer a.Close()
	t.Setenv("RUNPILOT_TEST_KEY", "k")
	st, _, ts := testEnv(t, []config.Server{srvCfg("a", 10, 1, a.URL, "RUNPILOT_TEST_KEY")})
	holder := mkSession(t, st, model.SessionIdle)
	waiter := mkSession(t, st, model.SessionIdle)

	// Занимаем единственный слот.
	resp, _ := postJSON(t, ts.URL+"/s/"+holder+"/v1/chat/completions", chatBody("runpilot", ""))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("holder: %d", resp.StatusCode)
	}

	// Ожидающий: слота нет → удержание hold_max_sec=1 с → 503.
	t0 := time.Now()
	resp, body := postJSON(t, ts.URL+"/s/"+waiter+"/v1/chat/completions", chatBody("runpilot", ""))
	elapsed := time.Since(t0)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("статус: %d, тело: %s", resp.StatusCode, body)
	}
	if got := resp.Header.Get("Retry-After"); got != "7" {
		t.Fatalf("Retry-After: %q, хочу 7", got)
	}
	want := `{"error":{"message":"runpilot: no free slot, queue position 1","type":"server_error","code":"runpilot_no_slot"}}`
	if body != want {
		t.Fatalf("тело удержания:\nполучено: %s\nхочу:     %s", body, want)
	}
	if elapsed < 900*time.Millisecond {
		t.Fatalf("ответ за %v — раньше окончания удержания (1 с)", elapsed)
	}
	// Запись остаётся в очереди: held_request=false, mode=resume.
	e, err := st.QueueGet(waiter)
	if err != nil {
		t.Fatalf("запись очереди не осталась: %v", err)
	}
	if e.HeldRequest {
		t.Fatal("held_request должен стать false после таймаута")
	}
	if e.Mode != model.QueueModeResume {
		t.Fatalf("mode: %q, хочу resume", e.Mode)
	}
}

// --- удержание: слот выдан во время ожидания ---

func TestHoldSlotGranted(t *testing.T) {
	a := fakellm.Start("srv-model")
	defer a.Close()
	t.Setenv("RUNPILOT_TEST_KEY", "k")
	st, gw, ts := testEnv(t, []config.Server{srvCfg("a", 10, 1, a.URL, "RUNPILOT_TEST_KEY")})
	holder := mkSession(t, st, model.SessionIdle)
	waiter := mkSession(t, st, model.SessionIdle)

	resp, _ := postJSON(t, ts.URL+"/s/"+holder+"/v1/chat/completions", chatBody("runpilot", ""))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("holder: %d", resp.StatusCode)
	}

	done := make(chan *http.Response, 1)
	go func() {
		r, _ := http.Post(ts.URL+"/s/"+waiter+"/v1/chat/completions",
			"application/json", strings.NewReader(chatBody("runpilot", "")))
		if r != nil {
			defer r.Body.Close()
			_, _ = io.Copy(io.Discard, r.Body)
		}
		done <- r
	}()
	time.Sleep(200 * time.Millisecond) // waiter в удержании
	gw.Release(holder, "TURN_DONE")
	r := <-done
	if r == nil || r.StatusCode != http.StatusOK {
		t.Fatalf("после освобождения слота: %v, хочу 200", r)
	}
	// Аренда у ожидания (IMPLICIT), запись очереди удалена.
	if _, err := st.QueueGet(waiter); err == nil {
		t.Fatal("запись очереди должна быть удалена при захвате")
	}
}

// --- разрешение аренды: правила 1–3 (раздел 6 ТЗ) ---

func TestResolveRules(t *testing.T) {
	a := fakellm.Start("srv-model")
	defer a.Close()
	t.Setenv("RUNPILOT_TEST_KEY", "k")
	st, gw, ts := testEnv(t, []config.Server{srvCfg("a", 10, 1, a.URL, "RUNPILOT_TEST_KEY")})
	s := mkSession(t, st, model.SessionIdle)

	// Правило 3: неявный захват, сессия IDLE → RUNNING.
	resp, _ := postJSON(t, ts.URL+"/s/"+s+"/v1/chat/completions", chatBody("runpilot", ""))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("захват: %d", resp.StatusCode)
	}
	lease, err := st.LeaseGet(s)
	if err != nil {
		t.Fatal(err)
	}
	if lease.Origin != model.LeaseOriginImplicit || lease.State != model.LeaseActive {
		t.Fatalf("аренда: %s/%s, хочу IMPLICIT/ACTIVE", lease.Origin, lease.State)
	}
	sess, _ := st.GetSession(s)
	if sess.State != model.SessionRunning {
		t.Fatalf("сессия: %s, хочу RUNNING", sess.State)
	}

	// Правило 1: повторный запрос — та же аренда (ACTIVE), новая не
	// создаётся.
	before, _ := st.LeaseListActive()
	resp, _ = postJSON(t, ts.URL+"/s/"+s+"/v1/chat/completions", chatBody("runpilot", ""))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("повтор: %d", resp.StatusCode)
	}
	after, _ := st.LeaseListActive()
	if len(before) != 1 || len(after) != 1 {
		t.Fatalf("аренд: %d → %d, хочу 1 → 1", len(before), len(after))
	}

	// Правило 2: PENDING → старт подтверждён → ACTIVE + RUNNING.
	s2 := mkSession(t, st, model.SessionDispatching)
	_, _ = st.LeaseCreate(model.Lease{
		SID: s2, Server: "a", Slot: 1, State: model.LeasePending,
		Origin: model.LeaseOriginDispatch, GrantedAt: time.Now().UTC(),
	})
	resp, _ = postJSON(t, ts.URL+"/s/"+s2+"/v1/chat/completions", chatBody("runpilot", ""))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PENDING: %d", resp.StatusCode)
	}
	l2, _ := st.LeaseGet(s2)
	if l2.State != model.LeaseActive {
		t.Fatalf("PENDING не стал ACTIVE: %s", l2.State)
	}
	sess2, _ := st.GetSession(s2)
	if sess2.State != model.SessionRunning {
		t.Fatalf("сессия DISPATCHING: %s, хочу RUNNING", sess2.State)
	}
	_ = gw
}

// --- миграция до первого байта: 50 из 50 (раздел 6 ТЗ) ---

func TestMigration50(t *testing.T) {
	a := fakellm.Start("srv-a")
	defer a.Close()
	b := fakellm.Start("srv-b")
	defer b.Close()
	t.Setenv("RUNPILOT_TEST_KEY_A", "ka")
	t.Setenv("RUNPILOT_TEST_KEY_B", "kb")
	st, gw, ts := testEnv(t, []config.Server{
		srvCfg("a", 10, 1, a.URL, "RUNPILOT_TEST_KEY_A"),
		srvCfg("b", 5, 1, b.URL, "RUNPILOT_TEST_KEY_B"),
	})
	s := mkSession(t, st, model.SessionIdle)
	// a: 50 отказов ДО первого байта (503), /health при этом жив —
	// отказ транзиторный, сервер остаётся UP и снова кандидат.
	a.SetFailBefore(50, "503")

	ok := 0
	for i := range 50 {
		resp, _ := postJSON(t, ts.URL+"/s/"+s+"/v1/chat/completions", chatBody("runpilot", ""))
		if resp.StatusCode == http.StatusOK {
			ok++
		}
		// Ход завершён — аренда снята (сценарий Э4); следующий запрос
		// снова захватывает слот и снова упирается в отказ a.
		gw.Release(s, "TURN_DONE")
		_ = i
	}
	if ok != 50 {
		t.Fatalf("миграция успешна %d из 50, хочу 50", ok)
	}
	if got := a.Requests(); got != 50 {
		t.Fatalf("запросов к a: %d, хочу 50 (по одному отказу на ход)", got)
	}
	if got := b.Requests(); got != 50 {
		t.Fatalf("успешных к b: %d, хочу 50", got)
	}
	sess, _ := st.GetSession(s)
	if sess.LastMigratedFrom != "a" {
		t.Fatalf("last_migrated_from: %q, хочу a", sess.LastMigratedFrom)
	}
	// a остался UP (health жив): транзиторный отказ не валит сервер.
	if st2 := gw.Servers().StateOf("a"); st2 != model.ServerUp {
		t.Fatalf("состояние a: %s, хочу UP (health ок)", st2)
	}
}

// --- миграция: мёртвый сервер уходит в DOWN ---

func TestMigrationServerDown(t *testing.T) {
	a := fakellm.Start("srv-a")
	defer a.Close()
	b := fakellm.Start("srv-b")
	defer b.Close()
	a.SetHealthDown(true)
	t.Setenv("RUNPILOT_TEST_KEY_A", "ka")
	t.Setenv("RUNPILOT_TEST_KEY_B", "kb")
	st, _, ts := testEnv(t, []config.Server{
		srvCfg("a", 10, 1, a.URL, "RUNPILOT_TEST_KEY_A"),
		srvCfg("b", 5, 1, b.URL, "RUNPILOT_TEST_KEY_B"),
	})
	s := mkSession(t, st, model.SessionIdle)
	a.SetFailBefore(1, "conn")

	resp, _ := postJSON(t, ts.URL+"/s/"+s+"/v1/chat/completions", chatBody("runpilot", ""))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("миграция: %d", resp.StatusCode)
	}
	// Оба апстрима эхо-ят model из тела — различаем по счётчику: ход
	// прошёл именно на b (a отказал до первого байта).
	if b.Requests() != 1 {
		t.Fatalf("запросов к b: %d, хочу 1 (ход с мигрированного a)", b.Requests())
	}
}

// --- миграция: тело подменяется под НОВЫЙ сервер (раздел 6 ТЗ) ---

// TestMigrationModelSwap — после миграции повторный запрос обязан нести
// model и max_* выбранного (нового) сервера, а не старого.
func TestMigrationModelSwap(t *testing.T) {
	a := fakellm.Start("m-a")
	defer a.Close()
	b := fakellm.Start("m-b")
	defer b.Close()
	t.Setenv("RUNPILOT_TEST_KEY_A", "ka")
	t.Setenv("RUNPILOT_TEST_KEY_B", "kb")
	cfgA := srvCfg("a", 10, 1, a.URL, "RUNPILOT_TEST_KEY_A")
	cfgA.Upstreams.OpenAI.Model = "model-a"
	cfgB := srvCfg("b", 5, 1, b.URL, "RUNPILOT_TEST_KEY_B")
	cfgB.Upstreams.OpenAI.Model = "model-b"
	st, _, ts := testEnv(t, []config.Server{cfgA, cfgB})
	s := mkSession(t, st, model.SessionIdle)
	a.SetFailBefore(1, "503")

	resp, _ := postJSON(t, ts.URL+"/s/"+s+"/v1/chat/completions", chatBody("runpilot", ""))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("миграция: %d", resp.StatusCode)
	}
	if b.Requests() != 1 {
		t.Fatalf("запросов к b: %d, хочу 1 (ход мигрирован)", b.Requests())
	}
	// b обязан получить СВОЮ модель, а не модель a.
	if got := b.LastModel(); got != "model-b" {
		t.Fatalf("model на b: %q, хочу model-b (подмена под новый сервер)", got)
	}
}

// --- SSE: число чанков 1:1 (раздел 6 ТЗ) ---

func TestSSEChunks1to1(t *testing.T) {
	a := fakellm.Start("srv-model")
	defer a.Close()
	a.SetDelays(0, 2*time.Millisecond, 7)
	t.Setenv("RUNPILOT_TEST_KEY", "k")
	st, _, ts := testEnv(t, []config.Server{srvCfg("a", 10, 1, a.URL, "RUNPILOT_TEST_KEY")})
	s := mkSession(t, st, model.SessionIdle)

	count := func(resp *http.Response) (int, int) {
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		n := 0
		for _, line := range strings.Split(string(raw), "\n") {
			if strings.HasPrefix(line, "data: {") {
				n++
			}
		}
		return n, resp.StatusCode
	}

	// Прямой вызов.
	dr, err := http.Post(a.URL+"/v1/chat/completions", "application/json",
		strings.NewReader(chatBody("runpilot", `"stream":true`)))
	if err != nil {
		t.Fatalf("прямой: %v", err)
	}
	direct, _ := count(dr)
	// Через шлюз (тело читаем сами: пост-хелпер его не должен съесть).
	resp := postOpen(t, ts.URL+"/s/"+s+"/v1/chat/completions",
		chatBody("runpilot", `"stream":true`))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("шлюз: %d", resp.StatusCode)
	}
	viaGW, _ := count(resp)
	if direct != 7 || viaGW != 7 {
		t.Fatalf("чанков: прямой=%d шлюз=%d, хочу 7=7", direct, viaGW)
	}
}

// --- 4xx передаются без изменений, миграции нет (раздел 6 ТЗ) ---

func Test4xxPassthrough(t *testing.T) {
	t.Setenv("RUNPILOT_TEST_KEY", "k")
	// Апстрим, который отвечает 400 на генерирующие запросы.
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":{"message":"bad request"}}`)
	}))
	defer bad.Close()
	st, _, ts := testEnv(t, []config.Server{
		srvCfg("a", 10, 1, bad.URL, "RUNPILOT_TEST_KEY"),
	})
	s := mkSession(t, st, model.SessionIdle)

	resp, body := postJSON(t, ts.URL+"/s/"+s+"/v1/chat/completions", chatBody("runpilot", ""))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("4xx: %d, хочу 400", resp.StatusCode)
	}
	if !strings.Contains(body, "bad request") {
		t.Fatalf("тело 4xx не переслано: %s", body)
	}
	// Аренда не освобождена, миграции нет.
	if _, err := st.LeaseGet(s); err != nil {
		t.Fatalf("аренда исчезла после 4xx: %v", err)
	}
}

// --- прочие пути: без аренды → UP с наибольшим priority (раздел 6 ТЗ) ---

func TestPassthroughModels(t *testing.T) {
	a := fakellm.Start("model-a")
	defer a.Close()
	b := fakellm.Start("model-b")
	defer b.Close()
	t.Setenv("RUNPILOT_TEST_KEY_A", "ka")
	t.Setenv("RUNPILOT_TEST_KEY_B", "kb")
	st, _, ts := testEnv(t, []config.Server{
		srvCfg("a", 5, 1, a.URL, "RUNPILOT_TEST_KEY_A"),
		srvCfg("b", 10, 1, b.URL, "RUNPILOT_TEST_KEY_B"),
	})
	s := mkSession(t, st, model.SessionIdle)

	// Без аренды: v1/models уходит на UP с наибольшим priority (b).
	resp, body := postJSON(t, ts.URL+"/s/"+s+"/v1/models", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("v1/models: %d", resp.StatusCode)
	}
	if !strings.Contains(body, "model-b") {
		t.Fatalf("маршрут не b (max priority): %s", body)
	}
	if _, err := st.LeaseGet(s); err == nil {
		t.Fatal("прочие пути не создают аренды")
	}
}

// --- запрет usage/529 в коде шлюза (раздел 6 ТЗ) ---

func TestNoUsageNo529(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") ||
			strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		raw, err := os.ReadFile(e.Name())
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "usage") {
			t.Fatalf("%s: запрещённый разбор usage", e.Name())
		}
		if strings.Contains(string(raw), "529") {
			t.Fatalf("%s: запрещённый код 529", e.Name())
		}
	}
}

// --- pin / prefer (раздел 6 ТЗ) ---

func TestPinPrefer(t *testing.T) {
	a := fakellm.Start("srv-a")
	defer a.Close()
	b := fakellm.Start("srv-b")
	defer b.Close()
	t.Setenv("RUNPILOT_TEST_KEY_A", "ka")
	t.Setenv("RUNPILOT_TEST_KEY_B", "kb")
	st, gw, ts := testEnv(t, []config.Server{
		srvCfg("a", 5, 1, a.URL, "RUNPILOT_TEST_KEY_A"),
		srvCfg("b", 10, 1, b.URL, "RUNPILOT_TEST_KEY_B"),
	})

	// prefer a: a ставится первым, поэтому даже при меньшем приоритете
	// (a=5, b=10) аренда уходит на a, пока у a есть слот.
	s := mkSession(t, st, model.SessionIdle)
	_, _ = st.DB().Exec(`UPDATE session SET constraint_kind='prefer', constraint_server='a' WHERE sid=?`, s)
	resp, _ := postJSON(t, ts.URL+"/s/"+s+"/v1/chat/completions", chatBody("runpilot", ""))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("prefer: %d", resp.StatusCode)
	}
	l, _ := st.LeaseGet(s)
	if l.Server != "a" {
		t.Fatalf("prefer a: аренда на %s, хочу a (prefer ставит первым)", l.Server)
	}
	gw.Release(s, "TURN_DONE")

	// pin: только b, даже если b «занят» другим — удержание.
	s2 := mkSession(t, st, model.SessionIdle)
	_, _ = st.DB().Exec(`UPDATE session SET constraint_kind='pin', constraint_server='b' WHERE sid=?`, s2)
	holder := mkSession(t, st, model.SessionIdle)
	_, _ = st.DB().Exec(`UPDATE session SET constraint_kind='pin', constraint_server='b' WHERE sid=?`, holder)
	resp, _ = postJSON(t, ts.URL+"/s/"+holder+"/v1/chat/completions", chatBody("runpilot", ""))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pin holder: %d", resp.StatusCode)
	}
	// s2: pin на b, b занят → удержание → 503.
	resp, _ = postJSON(t, ts.URL+"/s/"+s2+"/v1/chat/completions", chatBody("runpilot", ""))
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("pin занят: %d, хочу 503", resp.StatusCode)
	}
}
