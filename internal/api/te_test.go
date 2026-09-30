package api

// TE-<ID> — строки раздела 7 ТЗ (CONTROL W7), которые решаются в
// координаторе/API: внешние процессы (X), конфликты sid (C12).

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"runpilot/internal/clock"
	"runpilot/internal/config"
	"runpilot/internal/model"
	"runpilot/internal/proto"
	"runpilot/internal/scheduler"
	"runpilot/internal/store"

	"github.com/gorilla/websocket"
)

// TestTE_X2 — cron-qwen в обход очереди (вне tmux): обнаружен за 1 скан,
// source=cron, target=server:<name>, первое обнаружение → WARN.
func TestTE_X2(t *testing.T) {
	st, srv, clk := extTestServer(t)
	notified := make(chan string, 4)
	srv.SetNotify(func(text string) { notified <- text })

	clk.Advance(30 * time.Second)
	srv.ApplyNodeMsg("node-01", extMsg(proto.ExternalProc{
		PID: 901, PPID: 900, UID: 1000, StartSec: 30,
		Exe: "qwen", Flags: []string{"--approval-mode"}, Source: "cron",
		OpenAIBaseURL: "http://127.0.0.1:9000/v1",
	}))
	list, err := st.ExternalList()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("обнаружено %d, хочу 1 (≤2 скана): %+v", len(list), list)
	}
	if list[0].Source != "cron" {
		t.Fatalf("source=%q, хочу cron", list[0].Source)
	}
	if list[0].Target != model.ExtTargetServer+"srv-01" {
		t.Fatalf("target=%q, хочу server:srv-01", list[0].Target)
	}
	select {
	case <-notified:
	default:
		t.Fatal("первое обнаружение не прислало WARN")
	}
}

// TestTE_X6 — внешний процесс завершился: нет в новом скане → status=GONE,
// уведомление закрывается.
func TestTE_X6(t *testing.T) {
	st, srv, clk := extTestServer(t)
	clk.Advance(20 * time.Second)
	srv.ApplyNodeMsg("node-01", extMsg(proto.ExternalProc{
		PID: 950, UID: 1000, StartSec: 20, Exe: "qwen", Source: "cron",
		OpenAIBaseURL: "http://127.0.0.1:9000/v1",
	}))
	if list, _ := st.ExternalList(); len(list) != 1 || list[0].Status != model.ExtActive {
		t.Fatalf("предпосылка: %+v, хочу ACTIVE", list)
	}
	// Следующий скан: процесса нет.
	clk.Advance(time.Second)
	srv.ApplyNodeMsg("node-01", extMsg())
	list, _ := st.ExternalList()
	if len(list) != 1 || list[0].Status != model.ExtGone {
		t.Fatalf("после исчезновения: %+v, хочу status=GONE", list)
	}
}

// TestTE_C12 — два процесса используют один sid: внешний процесс с
// target=gateway:<sid> существующей сессии → EXTERNAL_PROCESS (SID_SHARED)
// + CRIT-уведомление.
func TestTE_C12(t *testing.T) {
	st, srv, clk := extTestServer(t)
	// Существующая сессия с известным sid.
	now := clk.Now()
	if err := st.CreateSession(store.SessionRecord{
		SID: "ABCDEF123456", Name: "t", Host: "node-01", HostIP: "127.0.0.1",
		TmuxSession: "t", PaneID: "%1", Profile: "qwen",
		State: model.SessionIdle, StateChangedAt: now, Class: model.ClassNormal,
		ConstraintKind: "none", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	var crits []string
	srv.SetNotify(func(text string) {
		if text == "" {
			return
		}
		crits = append(crits, text)
	})

	clk.Advance(15 * time.Second)
	srv.ApplyNodeMsg("node-01", extMsg(proto.ExternalProc{
		PID: 970, UID: 1000, StartSec: 15, Exe: "qwen", Source: "cron",
		OpenAIBaseURL: "http://127.0.0.1:8787/s/ABCDEF123456/v1",
	}))
	// Событие EXTERNAL_PROCESS с подтипом SID_SHARED.
	evs, err := st.EventList(0, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range evs {
		if e.Kind == model.KindExternalProc && contains(e.Payload, `"subtype":"SID_SHARED"`) {
			found = true
		}
	}
	if !found {
		t.Fatal("нет события EXTERNAL_PROCESS с подтипом SID_SHARED")
	}
	// CRIT-уведомление.
	crit := false
	for _, c := range crits {
		if strings.Contains(c, "SID_SHARED") {
			crit = true
		}
	}
	if !crit {
		t.Fatalf("нет CRIT-уведомления SID_SHARED: %v", crits)
	}
}

// TestTE_K9 — превышен лимит SSE-клиентов (web.sse_clients_max): новому
// подключению — 503 (регистрация отклонена).
func TestTE_K9(t *testing.T) {
	_, srv, _ := extTestServer(t)
	srv.cfg.Web.SSEClientsMax = 1
	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, ok := srv.registerSSE(cancel); !ok {
		t.Fatal("первый SSE-клиент отклонён")
	}
	_, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	if _, ok := srv.registerSSE(cancel2); ok {
		t.Fatal("второй SSE-клиент принят при лимите 1 (должен 503)")
	}
}

// TestTE_N4 — узел устаревшей версии: координатор шлёт ему update (само-
// обновление, раздел 14) с версией координатора.
func TestTE_N4(t *testing.T) {
	_, srv, _ := extTestServer(t)
	srv.SetVersion("v9-coord") // версия координатора; у узла — старая
	cap := addFakeNode(t, srv.hub, "n1", proto.ResSent, "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.triggerNodeUpdate(ctx, "n1"); err != nil {
		t.Fatalf("triggerNodeUpdate: %v", err)
	}
	if cap.Type != proto.KindUpdate {
		t.Fatalf("узелу не отправлено update: type=%q", cap.Type)
	}
	if cap.Version != "v9-coord" {
		t.Fatalf("update.version=%q, хочу v9-coord (самообновление узла)", cap.Version)
	}
	// W9: URL дистрибутива — API координатора (bind:api_port), а не
	// gateway_url (шлюз не раздаёт /api/v1/dist/runpilot → узел получал 404).
	if want := "http://127.0.0.1:8788/api/v1/dist/runpilot"; cap.URL != want {
		t.Errorf("update.url=%q, хочу %q (API, не шлюз)", cap.URL, want)
	}
}

// TestTE_N8 — токен узла отозван: 401 при подключении (дистрибутив).
func TestTE_N8(t *testing.T) {
	ts, st, _ := testServer(t)
	token := mustEnroll(t, ts)
	if code := distStatus(t, ts.URL+"/api/v1/dist/runpilot", token); code != http.StatusOK {
		t.Fatalf("до отзыва: %d, хочу 200", code)
	}
	if err := st.RevokeNodeTokenByValue(token, time.Now()); err != nil {
		t.Fatal(err)
	}
	if code := distStatus(t, ts.URL+"/api/v1/dist/runpilot", token); code != http.StatusUnauthorized {
		t.Fatalf("после отзыва: %d, хочу 401", code)
	}
}

// TestTE_N7 — конфликт имени хоста: hello с именем существующего узла и чужим
// токеном (токен привязан к другому host) → соединение закрыто HOST_CONFLICT
// (код policy violation), имя хоста не перехватывается.
func TestTE_N7(t *testing.T) {
	const opTok = "op-tok-n7"
	t.Setenv("RUNPILOT_TOKEN", opTok)
	st, err := store.Open(filepath.Join(t.TempDir(), "runpilot.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	cfg := config.Defaults()
	clk := clock.NewReal()
	hub := NewHub(clk)
	srv := NewServer(cfg, st, hub, clk, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// Существующий узел с именем h1 (реестр hub).
	addFakeNode(t, hub, "h1", proto.ResSent, "")

	// Чужой токен: привязан к host "th", а не к "h1".
	foreign := store.NewNodeToken("th", time.Now())
	if err := st.CreateNodeToken(foreign); err != nil {
		t.Fatal(err)
	}

	// WS-подключение с чужим токеном; hello утверждает существующий h1.
	hdr := http.Header{"Authorization": {"Bearer " + foreign.Token}}
	conn, _, err := websocket.DefaultDialer.Dial(wsURL(ts.URL), hdr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	m := proto.New(proto.KindHello)
	m.Host, m.HostIP = "h1", "127.0.0.1"
	if err := conn.WriteJSON(m); err != nil {
		t.Fatal(err)
	}
	_, _, rerr := conn.ReadMessage()
	var cerr *websocket.CloseError
	if !errors.As(rerr, &cerr) {
		t.Fatalf("ожидал close, а получил: %v", rerr)
	}
	if cerr.Code != websocket.ClosePolicyViolation {
		t.Fatalf("код закрытия = %d, хочу %d (HOST_CONFLICT): %q",
			cerr.Code, websocket.ClosePolicyViolation, cerr.Text)
	}
	// h1 не перехвачен: прежний узел ещё в реестре.
	if _, ok := hub.Registered("h1"); !ok {
		t.Fatal("h1 вывалился из реестра при конфликте")
	}
}

// TestTE_C10 — с панели снят @runpilot_sid: панель есть, опции нет. Ранее
// управляемая панель (SID) в новом скане без SID → SID ведётся в GONE
// (PaneGone), панель — UNMANAGED. Панель, не имевшая SID, не помечается.
func TestTE_C10(t *testing.T) {
	clk := clock.NewVirtual(time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC))
	hub := NewHub(clk)
	// Управляемая панель: SID=S1, paneID=%10.
	hub.SetPanes("host", []proto.Pane{{PaneID: "%10", SID: "S1", State: "IDLE"}})
	if _, ok := hub.PaneBySID("S1"); !ok {
		t.Fatal("предпосылка: панель управляемая (SID связан)")
	}
	// Снимок: у %10 сняли @runpilot_sid (SID пуст) — панель есть, опции нет.
	gone := hub.MarkUnmanaged("host", []proto.Pane{{PaneID: "%10", State: "IDLE"}})
	if len(gone) != 1 || gone[0] != "S1" {
		t.Fatalf("MarkUnmanaged: %v, хочу [S1] (сессию вести в GONE)", gone)
	}
	if _, ok := hub.PaneBySID("S1"); ok {
		t.Fatal("SID ещё связан с панелью после снятия @runpilot_sid")
	}
	// Панель, не имевшая SID с самого начала (UNMANAGED) — не помечается.
	hub.SetPanes("host", []proto.Pane{{PaneID: "%11", State: "IDLE"}})
	if g := hub.MarkUnmanaged("host", []proto.Pane{{PaneID: "%11", State: "IDLE"}}); len(g) != 0 {
		t.Fatalf("панель без SID не должна помечаться как снятая: %v", g)
	}
}

// TestTE_O6 — неверная настройка: проверка 4.5 → 400 с ошибкой у поля.
func TestTE_O6(t *testing.T) {
	ts, _, _ := testServer(t)
	// Неизвестный ключ → 400 (UNKNOWN_KEY).
	if code, body := doJSON(t, http.MethodPatch, ts.URL+"/api/v1/settings", map[string]any{"bogus.key": 1}); code != http.StatusBadRequest {
		t.Fatalf("неизвестный ключ: %d, хочу 400: %v", code, body)
	}
	// Невалидный тип (int-поле = строка) → 400 (TYPE_MISMATCH).
	if code, body := doJSON(t, http.MethodPatch, ts.URL+"/api/v1/settings", map[string]any{"turn.done_quiet_sec": "не число"}); code != http.StatusBadRequest {
		t.Fatalf("неверный тип: %d, хочу 400: %v", code, body)
	}
}

// TestTE_O4 — истекла сессия входа: запрос без Bearer-токена (или с неверным)
// → 401 (веб-интерфейс переходит на вход и возвращает на ту же страницу);
// с верным токеном → 200.
func TestTE_O4(t *testing.T) {
	const tok = "sekret-web"
	t.Setenv("RUNPILOT_TOKEN", tok)
	st, err := store.Open(filepath.Join(t.TempDir(), "runpilot.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	cfg := config.Defaults()
	clk := clock.NewReal()
	srv := NewServer(cfg, st, NewHub(clk), clk, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	getStatus := func(auth string) int {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/settings", nil)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := getStatus(""); code != http.StatusUnauthorized {
		t.Fatalf("без токена: %d, хочу 401", code)
	}
	if code := getStatus("Bearer wrong"); code != http.StatusUnauthorized {
		t.Fatalf("с неверным токеном: %d, хочу 401", code)
	}
	if code := getStatus("Bearer " + tok); code != http.StatusOK {
		t.Fatalf("с верным токеном: %d, хочу 200", code)
	}
}

// TestTE_O3 — браузер потерял связь: переподключение SSE с Last-Event-ID.
// В пределах буфера (web.sse_replay) — досылка пропущенных; отстал дальше
// буфера — RESYNC (id = maxID), клиент заново берёт GET /api/v1/state.
func TestTE_O3(t *testing.T) {
	st, srv, _ := extTestServer(t)
	// 10 событий в ленте (EventRecord асинхронный — ждём фиксации maxID=10).
	now := srv.clk.Now()
	for i := 0; i < 10; i++ {
		if err := st.EventRecord(model.Event{Kind: model.KindSessionState, SID: "s1", TS: now}); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if maxID, _ := st.EventMaxID(); maxID == 10 {
			break
		}
		if time.Now().After(deadline) {
			m, _ := st.EventMaxID()
			t.Fatalf("maxID=%d, хочу 10 (10 событий не зафиксированы)", m)
		}
		time.Sleep(10 * time.Millisecond)
	}
	srv.cfg.Web.SSEReplay = 2 // маленький буфер: 10-2=8

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	readSSE := func(lastID string) string {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/v1/events", nil)
		req.Header.Set("Last-Event-ID", lastID)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("SSE: %d", resp.StatusCode)
		}
		var sb strings.Builder
		buf := make([]byte, 8192)
		for {
			n, rerr := resp.Body.Read(buf)
			if n > 0 {
				sb.Write(buf[:n])
			}
			got := sb.String()
			if strings.Contains(got, "RESYNC") ||
				(strings.Contains(got, "id: 9") && strings.Contains(got, "id: 10")) {
				break
			}
			if rerr != nil {
				break
			}
		}
		return sb.String()
	}

	// A) Отстал дальше буфера (Last-Event-ID=1 < 10-2=8) → RESYNC (id=10).
	if a := readSSE("1"); !strings.Contains(a, `"kind":"RESYNC"`) {
		t.Fatalf("A: нет RESYNC при отставании дальше буфера: %q", a)
	}
	// B) В пределах буфера (Last-Event-ID=8 >= 8) → досылка 9,10, без RESYNC.
	b := readSSE("8")
	if strings.Contains(b, `"kind":"RESYNC"`) {
		t.Fatalf("B: неожиданный RESYNC в пределах буфера: %q", b)
	}
	if !strings.Contains(b, "id: 9") || !strings.Contains(b, "id: 10") {
		t.Fatalf("B: нет досыла пропущенных (id 9 и 10): %q", b)
	}
}

// TestTE_O7 — возврат прежних настроек: откат ревизии (4.6) восстанавливает
// значение, с которого сделана ревизия.
func TestTE_O7(t *testing.T) {
	ts, st, _ := testServer(t)
	if code, _ := doJSON(t, http.MethodPatch, ts.URL+"/api/v1/settings", map[string]any{"turn.done_quiet_sec": 3}); code != http.StatusOK {
		t.Fatalf("rev1: %d", code)
	}
	if code, _ := doJSON(t, http.MethodPatch, ts.URL+"/api/v1/settings", map[string]any{"turn.done_quiet_sec": 8}); code != http.StatusOK {
		t.Fatalf("rev2: %d", code)
	}
	// Ищем ревизию, установившую значение 3.
	resp, err := http.Get(ts.URL + "/api/v1/settings/revisions")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var revs []struct {
		ID   int64  `json:"id"`
		Diff string `json:"diff"`
	}
	if err := json.Unmarshal(raw, &revs); err != nil {
		t.Fatal(err)
	}
	var target int64
	for _, r := range revs {
		if strings.Contains(r.Diff, `"new":3`) {
			target = r.ID
		}
	}
	if target == 0 {
		t.Fatal("нет ревизии со значением 3")
	}
	if code, _ := doJSON(t, http.MethodPost, ts.URL+"/api/v1/settings/revert", map[string]any{"id": target}); code != http.StatusOK {
		t.Fatalf("revert: %d", code)
	}
	cfg := config.Defaults()
	if err := st.LoadWorkingInto(cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Turn.DoneQuietSec != 3 {
		t.Fatalf("после revert: done_quiet_sec=%d, хочу 3", cfg.Turn.DoneQuietSec)
	}
}

// contains — подстрока в bytearray.
func contains(b []byte, sub string) bool {
	return strings.Contains(string(b), sub)
}

// TestTE_O1 — два окна меняют один объект: If-Match не совпал с текущей
// версией → 409 VERSION_CONFLICT (веб подтягивает свежие данные); If-Match
// совпал → изменение применено.
func TestTE_O1(t *testing.T) {
	ts, _, _ := testServer(t)
	getETag := func() string {
		resp, err := http.Get(ts.URL + "/api/v1/settings")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return strings.Trim(resp.Header.Get("ETag"), `"`)
	}
	patch := func(ifMatch string, v float64) int {
		body, _ := json.Marshal(map[string]any{"turn.done_quiet_sec": v})
		req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/settings",
			strings.NewReader(string(body)))
		if ifMatch != "" {
			req.Header.Set("If-Match", ifMatch)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	v0 := getETag()
	if v0 == "" {
		t.Fatal("нет ETag в GET /settings")
	}
	// «Другое окно» меняет объект: версия растёт.
	if code := patch("", 5); code != http.StatusOK {
		t.Fatalf("PATCH (без If-Match): %d, хочу 200", code)
	}
	v1 := getETag()
	if v1 == v0 {
		t.Fatalf("версия не изменилась после PATCH: %s → %s", v0, v1)
	}
	// Запрос «первого окна» со старым If-Match → 409.
	if code := patch(v0, 6); code != http.StatusConflict {
		t.Fatalf("старый If-Match: %d, хочу 409 VERSION_CONFLICT", code)
	}
	// Актуальный If-Match → 200.
	if code := patch(v1, 6); code != http.StatusOK {
		t.Fatalf("актуальный If-Match: %d, хочу 200", code)
	}
}

// TestTE_O2 — повторное нажатие или повтор сети (Idempotency-Key): повтор
// запроса с тем же ключом возвращает ответ первого запроса, эффект
// применяется один раз (новая ревизия НЕ создаётся, значение — от первого).
func TestTE_O2(t *testing.T) {
	ts, st, _ := testServer(t)
	patch := func(key string, v float64) int {
		body, _ := json.Marshal(map[string]any{"turn.done_quiet_sec": v})
		req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/settings",
			strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := patch("k1", 5); code != http.StatusOK {
		t.Fatalf("первый запрос: %d, хочу 200", code)
	}
	r1, err := st.LatestSettingsRevisionID()
	if err != nil {
		t.Fatal(err)
	}
	// Повтор с тем же ключом, но другим значением (повтор сети): тот же ответ.
	if code := patch("k1", 9); code != http.StatusOK {
		t.Fatalf("повтор: %d, хочу 200", code)
	}
	r2, _ := st.LatestSettingsRevisionID()
	if r2 != r1 {
		t.Fatalf("повтор создал новую ревизию: %d → %d (эффект должен один раз)", r1, r2)
	}
	cfg := config.Defaults()
	if err := st.LoadWorkingInto(cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Turn.DoneQuietSec != 5 {
		t.Fatalf("рабочее значение: %d, хочу 5 (ответ первого запроса, а не 9)", cfg.Turn.DoneQuietSec)
	}
}

// TestTE_O9 — слишком длинный текст задания (больше web.paste_max_kb):
// композер получает 413 с фактическим размером и лимитом.
func TestTE_O9(t *testing.T) {
	st, srv, clk := extTestServer(t)
	srv.cfg.Web.PasteMaxKB = 1 // 1 КБ — маленький лимит для теста
	now := clk.Now()
	const sid = "TEO9SID00001"
	if err := st.CreateSession(store.SessionRecord{
		SID: sid, Name: "t9", Host: "n", HostIP: "127.0.0.1",
		TmuxSession: "t", PaneID: "%1", Profile: "qwen",
		State: model.SessionIdle, StateChangedAt: now, Class: model.ClassNormal,
		ConstraintKind: "none", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	long := strings.Repeat("x", 2048) // 2 КБ > 1 КБ
	body, _ := json.Marshal(map[string]any{"text": long, "enqueue": false})
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/sessions/"+sid+"/paste",
		strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("статус: %d, хочу 413: %s", resp.StatusCode, raw)
	}
	if !strings.Contains(string(raw), `"code":"PASTE_TOO_LONG"`) || !strings.Contains(string(raw), `"bytes":2048`) {
		t.Fatalf("тело 413 без фактического размера: %s", raw)
	}
}

// TestTE_N9 — на узле нет tmux/qwen или мало места на диске: node_health →
// проблемы узла (для него неактивны функции с причиной) + [WARN]; идущие
// сессии продолжают. Здоровый узел — без проблем.
func TestTE_N9(t *testing.T) {
	st, srv, _ := extTestServer(t)
	_ = st
	addFakeNode(t, srv.hub, "node-01", proto.ResSent, "")
	// Узел с проблемами: нет tmux, нет qwen, мало места (512 < 1024).
	srv.ApplyNodeMsg("node-01", proto.Msg{
		Type: proto.KindNodeHealth, Proto: proto.Version,
		TmuxVersion: "", QwenPath: "", QwenVersion: "",
		DiskFreeMB: 512,
	})
	v, ok := srv.hub.NodeHealth("node-01")
	if !ok {
		t.Fatal("нет node_health узла")
	}
	has := func(sub string) bool {
		for _, p := range v.Problems {
			if p == sub {
				return true
			}
		}
		return false
	}
	for _, want := range []string{"no_tmux", "no_qwen", "disk_low"} {
		if !has(want) {
			t.Fatalf("нет проблемы %q: %+v", want, v.Problems)
		}
	}
	// Здоровый узел — без проблем.
	srv.ApplyNodeMsg("node-01", proto.Msg{
		Type: proto.KindNodeHealth, Proto: proto.Version,
		TmuxVersion: "tmux 3.4", QwenPath: "/usr/local/bin/qwen", QwenVersion: "0.24.5",
		DiskFreeMB: 4096,
	})
	v2, _ := srv.hub.NodeHealth("node-01")
	if len(v2.Problems) != 0 {
		t.Fatalf("здоровый узел с проблемами: %+v", v2.Problems)
	}
}

// TestTE_N10 — часы узла расходятся: node_health.node_time против часов
// координатора → значение расхождения (ms) на странице узла.
func TestTE_N10(t *testing.T) {
	_, srv, _ := extTestServer(t)
	addFakeNode(t, srv.hub, "node-01", proto.ResSent, "")
	nodeNow := srv.clk.Now().UnixMilli()
	srv.ApplyNodeMsg("node-01", proto.Msg{
		Type: proto.KindNodeHealth, Proto: proto.Version,
		TmuxVersion: "tmux 3.4", QwenPath: "/usr/local/bin/qwen",
		NodeTimeMS: nodeNow + 30000, // узел на 30 с впереди
	})
	v, ok := srv.hub.NodeHealth("node-01")
	if !ok {
		t.Fatal("нет node_health узла")
	}
	if v.TimeSkewMS != 30000 {
		t.Fatalf("time_skew_ms=%d, хочу 30000", v.TimeSkewMS)
	}
}

// sessionUntested — флаг untested сессии в GET /api/v1/state.
func sessionUntested(t *testing.T, ts *httptest.Server, sid string) bool {
	t.Helper()
	resp, err := http.Get(ts.URL + "/api/v1/state")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var sv struct {
		Sessions []struct {
			SID      string `json:"SID"`
			Untested bool   `json:"untested"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(raw, &sv); err != nil {
		t.Fatal(err)
	}
	for _, s := range sv.Sessions {
		if s.SID == sid {
			return s.Untested
		}
	}
	t.Fatalf("нет сессии %s в state", sid)
	return false
}

// TestTE_C6 — сменилась версия Qwen Code: версия без фикстур → сессия
// untested + [WARN] «не проверен» (edge-trigger: один раз на версию);
// версия с фикстурами — без WARN и без untested.
func TestTE_C6(t *testing.T) {
	_, srv, _ := extTestServer(t)
	var warns []string
	srv.SetNotify(func(text string) { warns = append(warns, text) })
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	postRun := func(name, ver string) string {
		body, _ := json.Marshal(map[string]any{
			"name": name, "host": "n", "host_ip": "127.0.0.1",
			"profile": "qwen", "agent_version": ver,
		})
		resp, err := http.Post(ts.URL+"/api/v1/run", "application/json", strings.NewReader(string(body)))
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("run: %d: %s", resp.StatusCode, raw)
		}
		var out struct {
			SID string `json:"sid"`
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
		return out.SID
	}

	// Непроверенная версия → WARN + untested.
	sidBad := postRun("s-bad", "9.9.9")
	warned := false
	for _, w := range warns {
		if strings.Contains(w, "9.9.9") && strings.Contains(w, "не проверен") {
			warned = true
		}
	}
	if !warned {
		t.Fatalf("нет [WARN] для 9.9.9: %v", warns)
	}
	if !sessionUntested(t, ts, sidBad) {
		t.Fatal("сессия с 9.9.9 не помечена untested в state")
	}

	// Проверенная версия (0.24.4) → без WARN, без untested.
	before := len(warns)
	sidOK := postRun("s-ok", "0.24.4")
	if len(warns) != before {
		t.Fatalf("WARN для проверенной 0.24.4: %v", warns[before:])
	}
	if sessionUntested(t, ts, sidOK) {
		t.Fatal("сессия с 0.24.4 помечена untested (не должно)")
	}

	// Edge-trigger: повтор той же непроверенной версии — без нового WARN.
	before = len(warns)
	postRun("s-bad2", "9.9.9")
	if len(warns) != before {
		t.Fatalf("повторный WARN для 9.9.9 (edge-trigger нарушен): %v", warns[before:])
	}
}

// o5TestServer — координатор с планировщиком (виртуальные часы) для O5.
func o5TestServer(t *testing.T) (*store.Store, *Server, *clock.Virtual) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "runpilot.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	cfg := config.Defaults()
	clk := clock.NewVirtual(time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC))
	hub := NewHub(clk)
	srv := NewServer(cfg, st, hub, clk, slog.New(slog.NewTextHandler(io.Discard, nil)))
	sch := scheduler.New(scheduler.Options{
		Cfg: cfg, Store: st, Clk: clk,
		Servers: &apiFakeServers{}, Panes: &apiFakePanes{},
		Dispatch: func(context.Context, string, proto.Msg) (proto.Msg, error) {
			return proto.Msg{}, errors.New("off")
		},
		ResumeText: "continue", NoAsyncDispatch: true,
	})
	srv.SetScheduler(sch)
	return st, srv, clk
}

// queueOrder — SIDs записей очереди в порядке очереди.
func queueOrder(t *testing.T, st *store.Store) []string {
	t.Helper()
	es, err := st.QueueList()
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(es))
	for _, e := range es {
		out = append(out, e.SID)
	}
	return out
}

// sameList — равны ли списки поэлементно (порядок важен).
func sameList(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestTE_O5 — «Очистить очередь»: отмена в течение web.undo_sec возвращает
// очередь с исходным порядком (J12); после окна отмена невозможна (410).
func TestTE_O5(t *testing.T) {
	st, srv, clk := o5TestServer(t)
	ctx := context.Background()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	mkSess := func(sid, name string) {
		if err := st.CreateSession(store.SessionRecord{
			SID: sid, Name: name, Host: "n", HostIP: "127.0.0.1",
			TmuxSession: name, PaneID: "%1", Profile: "qwen",
			State: model.SessionIdle, StateChangedAt: clk.Now(),
			Class: model.ClassNormal, ConstraintKind: "none", CreatedAt: clk.Now(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	const a, b, c = "O5A0000001", "O5B0000001", "O5C0000001"
	mkSess(a, "sa")
	mkSess(b, "sb")
	mkSess(c, "sc")

	// Очередь в порядке a→b→c (разный enqueued_at).
	for _, sid := range []string{a, b, c} {
		if err := srv.sched.Enqueue(sid, model.QueueEntry{SID: sid}, false); err != nil {
			t.Fatal(err)
		}
		clk.Advance(time.Second)
	}
	if q := queueOrder(t, st); !sameList(q, []string{a, b, c}) {
		t.Fatalf("очередь до очистки: %v, хочу [%s %s %s]", q, a, b, c)
	}

	// «Очистить очередь» (O5).
	ex := srv.newExecutor()
	if out, err := ex.ClearQueue(ctx); err != nil || out.Code != "OK" {
		t.Fatalf("ClearQueue: out=%+v err=%v", out, err)
	}
	if q := queueOrder(t, st); len(q) != 0 {
		t.Fatalf("после очистки очередь не пуста: %v", q)
	}

	// «Отменить» в пределах web.undo_sec → исходный порядок (J12).
	resp, err := http.Post(ts.URL+"/api/v1/queue/undo", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("undo: %d, хочу 200", resp.StatusCode)
	}
	resp.Body.Close()
	if q := queueOrder(t, st); !sameList(q, []string{a, b, c}) {
		t.Fatalf("после undo порядок: %v, хочу [%s %s %s]", q, a, b, c)
	}

	// Повторная очистка + пропуск окна web.undo_sec → undo 410.
	if out, _ := ex.ClearQueue(ctx); out.Code != "OK" {
		t.Fatalf("ClearQueue 2: %+v", out)
	}
	clk.Advance(time.Duration(srv.cfg.Web.UndoSec+1) * time.Second)
	resp2, err := http.Post(ts.URL+"/api/v1/queue/undo", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	if resp2.StatusCode != http.StatusGone {
		t.Fatalf("undo после окна: %d, хочу 410", resp2.StatusCode)
	}
	resp2.Body.Close()
}

// TestTE_U3 — обновился Qwen Code (= C6): узел сообщает новую версию кодера в
// снимке панели (version_cmd после перезапуска) → сессия обновляется,
// помечается untested + [WARN] «не проверен».
func TestTE_U3(t *testing.T) {
	st, srv, _ := extTestServer(t)
	var warns []string
	srv.SetNotify(func(text string) { warns = append(warns, text) })
	now := srv.clk.Now()
	const sid = "U3SID000001"
	if err := st.CreateSession(store.SessionRecord{
		SID: sid, Name: "t", Host: "n", HostIP: "127.0.0.1",
		TmuxSession: "t", PaneID: "%1", Profile: "qwen",
		AgentVersion: "0.24.4", State: model.SessionIdle, StateChangedAt: now,
		Class: model.ClassNormal, ConstraintKind: "none", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	// Узел перезапустил кодер; version_cmd → 9.9.9 (снимок панели).
	srv.ApplyNodeMsg("node-01", proto.Msg{
		Type: proto.KindPanes, Proto: proto.Version,
		Panes: []proto.Pane{{PaneID: "%1", SID: sid, State: "IDLE", AgentVersion: "9.9.9"}},
	})
	// Сессия обновлена до 9.9.9.
	rec, err := st.GetSession(sid)
	if err != nil {
		t.Fatal(err)
	}
	if rec.AgentVersion != "9.9.9" {
		t.Fatalf("agent_version=%q, хочу 9.9.9 (обновился кодер)", rec.AgentVersion)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	if !sessionUntested(t, ts, sid) {
		t.Fatal("сессия с 9.9.9 не помечена untested в state")
	}
	warned := false
	for _, w := range warns {
		if strings.Contains(w, "9.9.9") && strings.Contains(w, "не проверен") {
			warned = true
		}
	}
	if !warned {
		t.Fatalf("нет [WARN] при смене версии на 9.9.9: %v", warns)
	}
}
