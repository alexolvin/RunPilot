package gateway

// W15 этап 1: шлюз пропускает медиа (image_url/video_url) без изменений.
// Критерии G1–G5.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"runpilot/internal/clock"
	"runpilot/internal/config"
	"runpilot/internal/model"
	"runpilot/internal/store"
	"runpilot/internal/testutil/fakellm"
)

// mediaBody — тело генерирующего запроса: текст + 3 image_url + 2 video_url
// (одно видео — data URI ~9,9 МБ) + неизвестные поля на верхнем уровне и
// внутри сообщения. stream — режим; videoB64Len — длина base64 видео.
func mediaBody(stream bool, videoB64Len int) string {
	big := strings.Repeat("QUJD", videoB64Len/4)
	body := map[string]any{
		"model":      "runpilot",
		"max_tokens": 5000,
		"stream":     stream,
		"custom_top": "xyz-w15",
		"messages": []any{
			map[string]any{
				"role":       "user",
				"custom_msg": 42,
				"content": []any{
					map[string]any{"type": "text", "text": "hi"},
					map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,AAA"}},
					map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,BBB"}},
					map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,CCC"}},
					map[string]any{"type": "video_url", "video_url": map[string]any{"url": "data:video/mp4;base64,DDD"}},
					map[string]any{"type": "video_url", "video_url": map[string]any{"url": "data:video/mp4;base64," + big}},
				},
			},
		},
	}
	b, _ := json.Marshal(body)
	return string(b)
}

// jsonDeepEqualExcept — глубокое равенство двух JSON, кроме полей except.
func jsonDeepEqualExcept(t *testing.T, got, want []byte, except ...string) {
	t.Helper()
	var mg, mw map[string]json.RawMessage
	if err := json.Unmarshal(got, &mg); err != nil {
		t.Fatalf("got: не JSON: %v", err)
	}
	if err := json.Unmarshal(want, &mw); err != nil {
		t.Fatalf("want: не JSON: %v", err)
	}
	for _, k := range except {
		delete(mg, k)
		delete(mw, k)
	}
	if !reflect.DeepEqual(mg, mw) {
		t.Fatalf("тело не прошло побайто:\ngot:  %s\nwant: %s",
			truncBody(got), truncBody(want))
	}
}

func truncBody(b []byte) string {
	const n = 400
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…(" + fmt.Sprint(len(b)) + " байт)"
}

// exceptFields — три поля, которые шлюз подменяет (W15 этап 1).
var exceptFields = []string{"model", "max_tokens", "max_completion_tokens"}

// mediaEnv — как testEnv, но max_body_mb = 128 (для медиа-тела ~10 МБ).
func mediaEnv(t *testing.T, servers []config.Server) (*store.Store, *Gateway, *httptest.Server) {
	t.Helper()
	cfg := config.Defaults()
	cfg.Servers = servers
	cfg.Gateway.HoldMaxSec = 1
	cfg.Gateway.RetryAfterSec = 7
	cfg.Gateway.MaxInflight = 2
	cfg.Gateway.MaxBodyMB = 128
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
	t.Cleanup(func() { ts.Close(); _ = st.Close() })
	return st, gw, ts
}

// G1 (прямой путь): non-stream + stream, тело проходит побайто.
func TestG1MediaPassthroughDirect(t *testing.T) {
	a := fakellm.Start("srv-model")
	defer a.Close()
	t.Setenv("RUNPILOT_TEST_KEY", "k")
	st, _, ts := mediaEnv(t, []config.Server{srvCfg("a", 10, 1, a.URL, "RUNPILOT_TEST_KEY")})
	sess := mkSession(t, st, model.SessionIdle)

	for _, stream := range []bool{false, true} {
		body := mediaBody(stream, 9900*1024)
		resp, _ := postJSON(t, ts.URL+"/s/"+sess+"/v1/chat/completions", body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("stream=%v: статус %d", stream, resp.StatusCode)
		}
		jsonDeepEqualExcept(t, a.LastBody(), []byte(body), exceptFields...)
	}
}

// G1 (путь через удержание): non-stream + stream, слот выдаёт планировщик.
func TestG1MediaPassthroughHold(t *testing.T) {
	a := fakellm.Start("srv-model")
	defer a.Close()
	t.Setenv("RUNPILOT_TEST_KEY", "k")
	st, gw, sch, _, ts := vclockEnv(t, []config.Server{srvCfg("a", 10, 1, a.URL, "RUNPILOT_TEST_KEY")})

	for _, stream := range []bool{false, true} {
		holder := vcSession(t, st, model.SessionIdle)
		waiter := vcSession(t, st, model.SessionIdle)
		// Holder занимает единственный слот.
		resp, _ := postJSON(t, ts.URL+"/s/"+holder+"/v1/chat/completions", chatBody("runpilot", ""))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("holder: %d", resp.StatusCode)
		}
		body := mediaBody(stream, 9900*1024)
		done := make(chan *http.Response, 1)
		go func() {
			r, _ := http.Post(ts.URL+"/s/"+waiter+"/v1/chat/completions",
				"application/json", strings.NewReader(body))
			if r != nil {
				defer r.Body.Close()
				_, _ = io.Copy(io.Discard, r.Body)
			}
			done <- r
		}()
		time.Sleep(200 * time.Millisecond) // waiter в удержании
		gw.Release(holder, "TURN_DONE")
		sch.Tick() // планировщик выдаёт waiter
		r := <-done
		if r == nil || r.StatusCode != http.StatusOK {
			t.Fatalf("stream=%v: после выдачи слота: %v", stream, r)
		}
		jsonDeepEqualExcept(t, a.LastBody(), []byte(body), exceptFields...)
		// Освободить слот waiter'а для следующей итерации.
		gw.Release(waiter, "TURN_DONE")
		sch.Tick()
	}
}

// G2: тело max_body_mb − 1 КиБ проходит; + 1 КиБ → 413 с телом побайтно.
func TestG2BodyLimit(t *testing.T) {
	a := fakellm.Start("srv-model")
	defer a.Close()
	t.Setenv("RUNPILOT_TEST_KEY", "k")
	st, _, ts := testEnv(t, []config.Server{srvCfg("a", 10, 1, a.URL, "RUNPILOT_TEST_KEY")})
	sess := mkSession(t, st, model.SessionIdle)
	// testEnv: max_body_mb = 1 (1048576 байт).
	limit := int64(1) * 1024 * 1024

	// max_body_mb − 1 КиБ проходит.
	small := bytes.Repeat([]byte("x"), int(limit)-1024)
	resp, _ := postJSON(t, ts.URL+"/s/"+sess+"/v1/chat/completions",
		fmt.Sprintf(`{"model":"runpilot","pad":%q}`, string(small)))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("max−1KiB: статус %d, хочу 200", resp.StatusCode)
	}

	// max_body_mb + 1 КиБ → 413, тело побайтно.
	big := bytes.Repeat([]byte("x"), int(limit)+1024)
	resp, body := postJSON(t, ts.URL+"/s/"+sess+"/v1/chat/completions",
		fmt.Sprintf(`{"model":"runpilot","pad":%q}`, string(big)))
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("max+1KiB: статус %d, хочу 413, тело: %s", resp.StatusCode, body)
	}
	// nMiB = ceil((limit+1KiB)/MiB) = 2 (1049600/1048576 = 1.0009 → 2).
	want := `{"error":{"message":"runpilot: request body 2 MiB exceeds gateway.max_body_mb=1","type":"invalid_request_error","code":"runpilot_body_too_large"}}`
	if body != want {
		t.Fatalf("тело 413:\nполучено: %s\nхочу:     %s", body, want)
	}
}

// G3: переполнение max_buffered_mb → 503 runpilot_gateway_busy; после
// освобождения памяти следующий запрос проходит.
func TestG3BufferedOverflow(t *testing.T) {
	a := fakellm.Start("srv-model")
	defer a.Close()
	t.Setenv("RUNPILOT_TEST_KEY", "k")
	cfg := config.Defaults()
	cfg.Servers = []config.Server{srvCfg("a", 10, 1, a.URL, "RUNPILOT_TEST_KEY")}
	cfg.Gateway.HoldMaxSec = 3600
	cfg.Gateway.RetryAfterSec = 7
	cfg.Gateway.MaxBodyMB = 4     // тело 600 КиБ < 4 МиБ
	cfg.Gateway.MaxBufferedMB = 1 // буфер 1 МиБ: два тела 600 КиБ не влезают
	cfg.Gateway.MaxInflight = 8
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
	t.Cleanup(func() { ts.Close(); _ = st.Close() })
	sess := mkSession(t, st, model.SessionIdle)

	body := bytes.Repeat([]byte("x"), 600*1024)
	payload := fmt.Sprintf(`{"model":"runpilot","pad":%q}`, string(body))

	// Первый запрос — медленный (firstByteDelay), держит 600 КиБ в буфере.
	a.SetDelays(time.Second, 0, 1)
	done1 := make(chan int, 1)
	go func() {
		r, _ := http.Post(ts.URL+"/s/"+sess+"/v1/chat/completions",
			"application/json", strings.NewReader(payload))
		if r != nil {
			defer r.Body.Close()
			_, _ = io.Copy(io.Discard, r.Body)
			done1 <- r.StatusCode
		}
	}()
	time.Sleep(300 * time.Millisecond) // первый запрос в полёте (600 КиБ в буфере)

	// Второй запрос (600 КиБ) → переполнение (600+600 > 1024) → 503.
	resp, body2 := postJSON(t, ts.URL+"/s/"+sess+"/v1/chat/completions", payload)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("переполнение: статус %d, хочу 503, тело: %s", resp.StatusCode, body2)
	}
	if !strings.Contains(body2, "runpilot_gateway_busy") {
		t.Fatalf("тело 503 без runpilot_gateway_busy: %s", body2)
	}
	if got := resp.Header.Get("Retry-After"); got != "7" {
		t.Fatalf("Retry-After: %q, хочу 7", got)
	}

	// После завершения первого запроса (буфер освобождён) → проходит.
	<-done1
	a.SetDelays(0, 0, 1)
	resp, _ = postJSON(t, ts.URL+"/s/"+sess+"/v1/chat/completions", payload)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("после освобождения: статус %d, хочу 200", resp.StatusCode)
	}
}

// G4: тело размером max_body_mb, отправленное со скоростью 2 МиБ/с, проходит.
// (ReadTimeout = 0: медленная загрузка не прерывается.)
func TestG4SlowBody(t *testing.T) {
	a := fakellm.Start("srv-model")
	defer a.Close()
	t.Setenv("RUNPILOT_TEST_KEY", "k")
	st, _, ts := testEnv(t, []config.Server{srvCfg("a", 10, 1, a.URL, "RUNPILOT_TEST_KEY")})
	sess := mkSession(t, st, model.SessionIdle)
	// max_body_mb = 1 МиБ (testEnv). Тело ровно max_body_mb, 2 МиБ/с ≈ 0.5 с.
	total := 1024 * 1024
	prefix := `{"model":"runpilot","pad":"`
	suffix := `"}`
	payload := prefix + string(bytes.Repeat([]byte("x"), total-len(prefix)-len(suffix))) + suffix
	if len(payload) != total {
		t.Fatalf("размер тела %d, хочу %d", len(payload), total)
	}
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/s/"+sess+"/v1/chat/completions",
		&throttledReader{data: []byte(payload), chunk: 64 * 1024, delay: 30 * time.Millisecond})
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("медленное тело: статус %d, хочу 200", resp.StatusCode)
	}
}

// throttledReader — reader, отдающий данные по chunk байт с паузой delay
// (эмуляция скорости загрузки).
type throttledReader struct {
	data  []byte
	pos   int
	chunk int
	delay time.Duration
}

func (r *throttledReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n := r.chunk
	if n > len(r.data)-r.pos {
		n = len(r.data) - r.pos
	}
	if n > len(p) {
		n = len(p)
	}
	copy(p, r.data[r.pos:r.pos+n])
	r.pos += n
	if r.delay > 0 {
		time.Sleep(r.delay)
	}
	return n, nil
}

// G5: после тестов с медиа строка ";base64," в БД встречается 0 раз
// (тело запроса не хранится; в строке request — только body_bytes).
func TestG5NoBase64InDB(t *testing.T) {
	a := fakellm.Start("srv-model")
	defer a.Close()
	t.Setenv("RUNPILOT_TEST_KEY", "k")
	st, _, ts := mediaEnv(t, []config.Server{srvCfg("a", 10, 1, a.URL, "RUNPILOT_TEST_KEY")})
	sess := mkSession(t, st, model.SessionIdle)
	body := mediaBody(false, 9900*1024)
	resp, _ := postJSON(t, ts.URL+"/s/"+sess+"/v1/chat/completions", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("статус %d", resp.StatusCode)
	}
	// Дать асинхронной записи request-строки завершиться.
	time.Sleep(200 * time.Millisecond)
	var count int
	if err := st.DB().QueryRow(`SELECT count(*) FROM request WHERE body_bytes > 0`).Scan(&count); err != nil {
		t.Fatalf("запрос к request: %v", err)
	}
	if count == 0 {
		t.Fatal("нет request-строк с body_bytes > 0")
	}
	// Тело (с base64) не хранится: сканируем все текстовые столбцы.
	rows, err := st.DB().Query(`SELECT id, sid, server, path, error FROM request`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var sid, server, path, errStr string
		if err := rows.Scan(&id, &sid, &server, &path, &errStr); err != nil {
			t.Fatal(err)
		}
		for _, s := range []string{sid, server, path, errStr} {
			if strings.Contains(s, ";base64,") {
				t.Fatalf("строка %q содержит ;base64, (id=%d)", s, id)
			}
		}
	}
}
