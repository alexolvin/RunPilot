package api

// W6 CONTROL-строки (раздел 20 ТЗ v2): dist по токену узла (CONTROL 4),
// удаление узла/сервера в обоих режимах (CONTROL 7), подключение узла
// (enroll), новая сессия из веба (CONTROL 5/6).

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"runpilot/internal/model"
	"runpilot/internal/proto"
)

// fakeConn — NodeConn для spawn-тестов: ловит команду, синхронно отвечает.
type fakeConn struct {
	hub     *Hub
	capture *proto.Msg
	result  string
	detail  string
}

func (c *fakeConn) WriteJSON(v any) error {
	m := v.(proto.Msg)
	if c.capture != nil {
		*c.capture = m
	}
	rep := proto.New(proto.KindReply)
	rep.CmdID = m.CmdID
	rep.Result = c.result
	rep.Detail = c.detail
	c.hub.DeliverReply(rep)
	return nil
}

func (c *fakeConn) WriteControl(int, []byte, time.Time) error { return nil }

func addFakeNode(t *testing.T, hub *Hub, host, result, detail string) *proto.Msg {
	t.Helper()
	cap := &proto.Msg{}
	hub.Register(&NodeInfo{Host: host, Conn: &fakeConn{hub: hub, capture: cap, result: result, detail: detail}})
	t.Cleanup(func() { hub.Unregister(host) })
	return cap
}

// --- CONTROL 4: дистрибутив только по токену узла; отозванный → 401 ---

func TestDistNodeTokenControl4(t *testing.T) {
	ts, st, _ := testServer(t)

	// enroll → токен.
	token := mustEnroll(t, ts)
	if token == "" {
		t.Fatal("enroll не вернул токен")
	}

	// Без токена → 401.
	if code := distStatus(t, ts.URL+"/api/v1/dist/runpilot", ""); code != http.StatusUnauthorized {
		t.Errorf("dist без токена = %d, хочу 401", code)
	}
	// С токеном → 200.
	if code := distStatus(t, ts.URL+"/api/v1/dist/runpilot", token); code != http.StatusOK {
		t.Errorf("dist с токеном = %d, хочу 200", code)
	}
	// Отзыв → 401 (CONTROL 4).
	if err := st.RevokeNodeTokenByValue(token, time.Now()); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if code := distStatus(t, ts.URL+"/api/v1/dist/runpilot", token); code != http.StatusUnauthorized {
		t.Errorf("dist с отозванным токеном = %d, хочу 401", code)
	}
}

func distStatus(t *testing.T, url, token string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("dist: %v", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

// --- CONTROL 7: удаление узла в обоих режимах ---

func TestNodeDeleteBothModesControl7(t *testing.T) {
	ts, st, _ := testServer(t)

	// Режим now: токен отзывается сразу.
	tok1 := mustEnroll(t, ts)
	if err := st.BindTokenHost(tok1, "host-now", time.Now()); err != nil {
		t.Fatal(err)
	}
	if code, _ := doJSON(t, http.MethodDelete, ts.URL+"/api/v1/nodes/host-now?mode=now", nil); code != http.StatusOK {
		t.Fatalf("delete now = %d, хочу 200", code)
	}
	if _, ok := st.VerifyNodeToken(tok1); ok {
		t.Error("reжим now: токен должен быть отозван")
	}

	// Режим after_turns: токен жив, пометка на удаление.
	tok2 := mustEnroll(t, ts)
	if err := st.BindTokenHost(tok2, "host-after", time.Now()); err != nil {
		t.Fatal(err)
	}
	if code, _ := doJSON(t, http.MethodDelete, ts.URL+"/api/v1/nodes/host-after?mode=after_turns", nil); code != http.StatusAccepted {
		t.Fatalf("delete after_turns = %d, хочу 202", code)
	}
	if _, ok := st.VerifyNodeToken(tok2); !ok {
		t.Error("reжим after_turns: токен пока должен быть активен")
	}
}

// --- CONTROL 7: удаление сервера в обоих режимах ---

func TestServerDeleteBothModesControl7(t *testing.T) {
	ts, st, _ := testServer(t)

	// now: без активных ходов (sched nil) → сразу.
	_, _ = doJSON(t, http.MethodPost, ts.URL+"/api/v1/servers", map[string]any{
		"name": "srv-now", "slots": 1, "health_url": "http://127.0.0.1:1/health"})
	if code, _ := doJSON(t, http.MethodDelete, ts.URL+"/api/v1/servers/srv-now?mode=now", nil); code != http.StatusOK {
		t.Fatalf("server delete now = %d, хочу 200", code)
	}
	if _, err := st.GetServer("srv-now"); err == nil {
		t.Error("server now: сервер должен быть удалён")
	}

	// after_turns: остаётся до завершения удаления.
	_, _ = doJSON(t, http.MethodPost, ts.URL+"/api/v1/servers", map[string]any{
		"name": "srv-after", "slots": 1, "health_url": "http://127.0.0.1:1/health"})
	if code, _ := doJSON(t, http.MethodDelete, ts.URL+"/api/v1/servers/srv-after?mode=after_turns", nil); code != http.StatusAccepted {
		t.Fatalf("server delete after_turns = %d, хочу 202", code)
	}
	if _, err := st.GetServer("srv-after"); err != nil {
		t.Error("server after_turns: сервер пока должен оставаться")
	}
}

// --- Подключение узла (enroll, CONTROL 1: одна команда) ---

func TestEnrollFlow(t *testing.T) {
	ts, st, _ := testServer(t)
	tok := mustEnroll(t, ts)

	// install.sh отдаётся.
	resp, err := http.Get(ts.URL + "/enroll/" + tok + "/install.sh")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(raw), "runpilot") {
		t.Errorf("install.sh: status=%d, содержимое не похоже на скрипт", resp.StatusCode)
	}
	// Конфиг узла: coordinator = API-слушатель (bind:api_port по Defaults()),
	// НЕ базис запроса — канал узла GET /api/v1/node на базисе запроса
	// (web/https) не смонтирован → узел вешался бы на «bad handshake».
	if !strings.Contains(string(raw), "coordinator: http://127.0.0.1:8788") {
		t.Errorf("install.sh: в конфиге узла coordinator должен быть API-адрес (bind:api_port), а не базис запроса")
	}
	// Бинарник отдаётся (200).
	if code := httpCode(t, ts.URL+"/enroll/"+tok+"/runpilot"); code != http.StatusOK {
		t.Errorf("enroll runpilot = %d, хочу 200", code)
	}
	// claim привязывает токен к host.
	body, _ := json.Marshal(map[string]string{"host": "node-01"})
	resp2, err := http.Post(ts.URL+"/enroll/"+tok+"/claim", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("claim = %d, хочу 200", resp2.StatusCode)
	}
	if nt, err := st.GetNodeToken("node-01"); err != nil || nt.Token != tok {
		t.Errorf("claim не привязал токен к host (err=%v)", err)
	}
	// Чужой/отозванный токен → 401.
	if code := httpCode(t, ts.URL+"/enroll/badtoken/install.sh"); code != http.StatusUnauthorized {
		t.Errorf("bad token install.sh = %d, хочу 401", code)
	}
}

// W9 доп-3f: install.sh не держит терминал (раньше «exec runpilot node» заменял
// шелл оператора — tmux-сеанс «зависал» на логах узла): узел ставится
// службой systemd, скрипт завершается.
func TestEnrollInstallNoForegroundExec(t *testing.T) {
	ts, _, _ := testServer(t)
	tok := mustEnroll(t, ts)
	resp, err := http.Get(ts.URL + "/enroll/" + tok + "/install.sh")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	s := string(raw)
	if strings.Contains(s, "exec ") {
		t.Errorf("install.sh содержит exec (держит терминал): узел должен работать службой")
	}
	for _, want := range []string{
		`service install node`,
		`tmux-setup`,
		`enable-linger`, // 14.1: нет linger → подсказка + exit 3
		`X-RUNPILOT-Sha256`,  // 14.1: сверка хеша бинарника
	} {
		if !strings.Contains(s, want) {
			t.Errorf("install.sh не содержит %q", want)
		}
	}
}

// W9 доп-3c: повторная регистрация узла (новый токен, тот же host) не падает
// в UNIQUE(node_token.host) → 500 + ложный SAFE_MODE. Старый токен отзывается.
func TestEnrollReClaimSameHost(t *testing.T) {
	ts, st, _ := testServer(t)
	host := "node-c"

	tok1 := mustEnroll(t, ts)
	body, _ := json.Marshal(map[string]string{"host": host})
	resp1, err := http.Post(ts.URL+"/enroll/"+tok1+"/claim", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp1.Body.Close()
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("первый claim = %d, хочу 200", resp1.StatusCode)
	}
	if nt, err := st.GetNodeToken(host); err != nil || nt.Token != tok1 {
		t.Fatalf("claim1 не привязал: %v %v", nt, err)
	}

	// Re-enroll: новый токен на тот же host. ДО фикса — 500 + SAFE_MODE.
	tok2 := mustEnroll(t, ts)
	resp2, err := http.Post(ts.URL+"/enroll/"+tok2+"/claim", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	raw2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("re-claim = %d (тело %s), хочу 200", resp2.StatusCode, raw2)
	}
	if nt, err := st.GetNodeToken(host); err != nil || nt.Token != tok2 {
		t.Fatalf("re-claim не переключил токен: %v %v", nt, err)
	}
	// Старый токен отозван.
	if _, ok := st.VerifyNodeToken(tok1); ok {
		t.Error("старый токен должен быть отозван")
	}
}

// --- CONTROL 5/6: новая сессия из веба (spawn) ---

func TestSpawnControl5(t *testing.T) {
	ts, _, hub := testServer(t)
	cap := addFakeNode(t, hub, "n1", proto.ResSpawned, "%0")

	resp, err := httpPostJSON(t, ts.URL+"/api/v1/sessions/spawn", map[string]any{
		"name": "sess-web", "dir": "/tmp/proj"})
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("spawn = %d, хочу 201", resp.StatusCode)
	}
	// Узлу ушёл spawn с dir/name/sid.
	if cap.Name != "sess-web" || cap.Dir != "/tmp/proj" || cap.SID == "" {
		t.Errorf("spawn msg = name=%q dir=%q sid=%q", cap.Name, cap.Dir, cap.SID)
	}
	// Панель с sid зарегистрирована (CONTROL 5: @runpilot_sid ≤5s).
	if _, ok := hub.PaneBySID(cap.SID); !ok {
		t.Error("spawn: панель с sid не зарегистрирована в hub")
	}
}

// CONTROL 6: DIR_FORBIDDEN → 400, сессия не создаётся.
func TestSpawnDirForbiddenControl6(t *testing.T) {
	ts, st, hub := testServer(t)
	addFakeNode(t, hub, "n1", proto.ResDirForbidden, "/etc")

	resp, err := httpPostJSON(t, ts.URL+"/api/v1/sessions/spawn", map[string]any{
		"name": "sess-fb", "dir": "/etc"})
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("spawn DIR_FORBIDDEN = %d, хочу 400", resp.StatusCode)
	}
	if live, err := st.FindLiveByName("sess-fb"); err == nil && live.SID != "" {
		t.Error("spawn DIR_FORBIDDEN: сессия не должна быть создана")
	}
}

// W9: spawn пишет событие создания (SSE-лента) — вебо узнаёт о сессии без
// перезагрузки. До фикса список обновлялся только при RESYNC/релоаде.
func TestSpawnEmitsCreatedEvent(t *testing.T) {
	ts, st, hub := testServer(t)
	cap := addFakeNode(t, hub, "n1", proto.ResSpawned, "%0")

	resp, err := httpPostJSON(t, ts.URL+"/api/v1/sessions/spawn", map[string]any{
		"name": "sess-ev", "dir": "/tmp/proj"})
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("spawn = %d, хочу 201", resp.StatusCode)
	}
	if cap.SID == "" {
		t.Fatal("spawn: пустой sid")
	}
	// EventRecord асинхронен — ждём фиксации.
	waitForMaxID(t, st, 1)
	evs, err := st.EventList(0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, e := range evs {
		if e.SID != cap.SID || e.Kind != model.KindSessionState {
			continue
		}
		var pl struct {
			From string `json:"from"`
			To   string `json:"to"`
		}
		if err := json.Unmarshal(e.Payload, &pl); err != nil {
			t.Fatal(err)
		}
		if pl.From == "" && pl.To == string(model.SessionIdle) {
			found = true
		}
	}
	if !found {
		t.Errorf("spawn: нет события SESSION_STATE ''→IDLE для %s (лента: %d)", cap.SID, len(evs))
	}
}

// W9: runpilot run тоже пишет событие создания (та же причина — вебо не видело
// сессии, созданные CLI, до перезагрузки).
func TestRunEmitsCreatedEvent(t *testing.T) {
	ts, st, _ := testServer(t)
	body, _ := json.Marshal(map[string]any{
		"name": "sess-run-ev", "host": "n1", "host_ip": "127.0.0.1", "profile": "qwen",
	})
	resp, err := httpPostJSON(t, ts.URL+"/api/v1/run", json.RawMessage(body))
	if err != nil {
		t.Fatal(err)
	}
	var rr struct {
		SID string `json:"sid"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rr); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("run = %d, хочу 201", resp.StatusCode)
	}
	if rr.SID == "" {
		t.Fatal("run: пустой sid")
	}
	waitForMaxID(t, st, 1)
	evs, err := st.EventList(0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, e := range evs {
		if e.SID != rr.SID || e.Kind != model.KindSessionState {
			continue
		}
		var pl struct {
			From string `json:"from"`
			To   string `json:"to"`
		}
		if err := json.Unmarshal(e.Payload, &pl); err != nil {
			t.Fatal(err)
		}
		if pl.From == "" && pl.To == string(model.SessionIdle) {
			found = true
		}
	}
	if !found {
		t.Errorf("run: нет события SESSION_STATE ''→IDLE для %s (лента: %d)", rr.SID, len(evs))
	}
}

// --- helpers ---

func mustEnroll(t *testing.T, ts *httptest.Server) string {
	t.Helper()
	raw, err := httpPost(t, ts.URL+"/api/v1/nodes/enroll")
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Token string `json:"token"`
	}
	json.Unmarshal(raw, &out)
	return out.Token
}

func httpPost(t *testing.T, url string) ([]byte, error) {
	t.Helper()
	resp, err := http.Post(url, "application/json", bytes.NewReader([]byte("{}")))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func httpPostJSON(t *testing.T, url string, body any) (*http.Response, error) {
	t.Helper()
	data, _ := json.Marshal(body)
	return http.Post(url, "application/json", bytes.NewReader(data))
}

func httpCode(t *testing.T, url string) int {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode
}
