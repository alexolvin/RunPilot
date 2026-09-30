package web

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"runpilot/internal/clock"
	"runpilot/internal/config"
	"runpilot/internal/store"
)

// W1 CONTROL: все маршруты веба без cookie → 401; все изменяющие без CSRF →
// 403. Вход по операторскому токену даёт cookie + csrf.

const testToken = "runpilot-test-token-0123456789"

// testWeb — веб-слушатель на tmp-БД с фэйковыми маршрутами /api/v1/*.
func testWeb(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	cfg := config.Defaults()
	cfg.Web.LoginRatePerMin = 1000
	path := filepath.Join(t.TempDir(), "runpilot.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	clk := clock.NewReal()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	dummy := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	srv := New(cfg, st, dummy, clk, log)
	srv.SetToken(testToken)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, st
}

// Все маршруты (GET и изменяющие) без cookie → 401.
func TestWebAllRoutes401WithoutCookie(t *testing.T) {
	ts, _ := testWeb(t)
	routes := []struct {
		method, path string
	}{
		{"GET", "/"},
		{"GET", "/web/session"},
		{"GET", "/web/sessions"},
		{"GET", "/api/v1/state"},
		{"GET", "/api/v1/sessions"},
		{"POST", "/api/v1/run"},
		{"POST", "/api/v1/pause"},
		{"DELETE", "/api/v1/sessions/x"},
		{"GET", "/some/other/path"},
	}
	for _, r := range routes {
		req, _ := http.NewRequest(r.method, ts.URL+r.path, strings.NewReader("{}"))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", r.method, r.path, err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s без cookie = %d, хочу 401", r.method, r.path, resp.StatusCode)
		}
	}
}

// W9 (доп-3): /enroll/<token>/… публичен на веб-слушателе — curl -fsSL …
// | bash с чистой машины не имеет cookie веба, токен в пути и есть
// аутентификация (14.2). Запрос должен дойти до api-маршрутов, а не
// получить 401 «нужен вход».
func TestWebEnrollPublicWithoutCookie(t *testing.T) {
	cfg := config.Defaults()
	cfg.Web.LoginRatePerMin = 1000
	path := filepath.Join(t.TempDir(), "runpilot.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	reached := false
	enroll := http.NewServeMux()
	enroll.HandleFunc("/enroll/{token}/{rest...}", func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.Header().Set("Content-Type", "text/x-shellscript")
		_, _ = w.Write([]byte("#!/usr/bin/env bash\n"))
	})
	dummy := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	srv := New(cfg, st, dummy, clock.NewReal(), log)
	srv.SetEnrollHandler(enroll)
	srv.SetToken(testToken)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL + "/enroll/sometoken/install.sh")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !reached {
		t.Fatalf("GET /enroll/… без cookie = %d (reached=%v), хочу 200 от api-маршрутов", resp.StatusCode, reached)
	}
	// /api/v1/ без cookie по-прежнему 401 (не открылось наружу).
	resp2, err := http.Get(ts.URL + "/api/v1/state")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Errorf("GET /api/v1/state без cookie = %d, хочу 401", resp2.StatusCode)
	}
}

// TZ O4/11.1: браузер без действующей сессии (Accept: text/html) → 302 на
// /web/login?next=… (после входа — возврат на запрошенную страницу);
// скриптовые клиенты (без text/html в Accept) → 401 (W1 CONTROL);
// с действующей сессией → 200 SPA-оболочка.
func TestWebShellLoginRedirect(t *testing.T) {
	ts, _ := testWeb(t)
	noRedirect := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	get := func(accept, cookie string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest("GET", ts.URL+"/", nil)
		if accept != "" {
			req.Header.Set("Accept", accept)
		}
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: CookieName, Value: cookie})
		}
		resp, err := noRedirect.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		return resp
	}
	// Браузер (адресная строка) без сессии → 302 /web/login?next=/
	resp := get("text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8", "")
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("браузер без сессии: %d, хочу 302", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/web/login?next=%2F" {
		t.Errorf("Location: %q, хочу %q", loc, "/web/login?next=%2F")
	}
	// Скриптовый клиент (без text/html в Accept) → 401 (W1 CONTROL).
	if resp = get("*/*", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("скрипт без сессии: %d, хочу 401", resp.StatusCode)
	}
	// Недействительная cookie (отозвана/чужая) → тоже 302 для браузера.
	if resp = get("text/html", "not-a-real-session"); resp.StatusCode != http.StatusFound {
		t.Errorf("недействительная cookie, браузер: %d, хочу 302", resp.StatusCode)
	}
	// Действующая сессия → 200, text/html.
	cookie, _ := doLogin(t, ts, testToken)
	resp = get("text/html", cookie)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("с сессией: %d, хочу 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type: %q, хочу text/html", ct)
	}
}

// doLogin — POST /web/login; возвращает cookie и csrf_token.
func doLogin(t *testing.T, ts *httptest.Server, token string) (cookie string, csrf string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"token": token})
	resp, err := http.Post(ts.URL+"/web/login", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("login: %d %s", resp.StatusCode, raw)
	}
	var out struct {
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	c := resp.Cookies()
	for _, ck := range c {
		if ck.Name == CookieName {
			cookie = ck.Value
		}
	}
	if cookie == "" {
		t.Fatalf("login не вернул cookie %s", CookieName)
	}
	return cookie, out.CSRFToken
}

// Неверный токен → 401, cookie не выдаётся.
func TestWebLoginWrongToken(t *testing.T) {
	ts, _ := testWeb(t)
	body, _ := json.Marshal(map[string]string{"token": "wrong"})
	resp, err := http.Post(ts.URL+"/web/login", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("неверный токен: %d, хочу 401", resp.StatusCode)
	}
}

// Изменяющий запрос с cookie, но без X-RUNPILOT-CSRF → 403.
func TestWebMutation403WithoutCSRF(t *testing.T) {
	ts, _ := testWeb(t)
	cookie, _ := doLogin(t, ts, testToken)
	for _, path := range []string{"/api/v1/run", "/api/v1/pause", "/api/v1/resume"} {
		req, _ := http.NewRequest("POST", ts.URL+path, strings.NewReader("{}"))
		req.AddCookie(&http.Cookie{Name: CookieName, Value: cookie})
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("POST %s без CSRF = %d, хочу 403", path, resp.StatusCode)
		}
	}
}

// Изменяющий запрос с cookie И верным CSRF проходит к маршруту (200).
func TestWebMutationWithCSRF(t *testing.T) {
	ts, _ := testWeb(t)
	cookie, csrf := doLogin(t, ts, testToken)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/run", strings.NewReader("{}"))
	req.AddCookie(&http.Cookie{Name: CookieName, Value: cookie})
	req.Header.Set(CSRFHeader, csrf)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/v1/run с CSRF = %d, хочу 200 (фэйковый маршрут)", resp.StatusCode)
	}
}

// GET-маршруты с cookie проходят (200 на фэйковом маршруте).
func TestWebGetWithCookie(t *testing.T) {
	ts, _ := testWeb(t)
	cookie, _ := doLogin(t, ts, testToken)
	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/state", nil)
	req.AddCookie(&http.Cookie{Name: CookieName, Value: cookie})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/v1/state с cookie = %d, хочу 200", resp.StatusCode)
	}
}

// /web/session возвращает csrf + actor + версию.
func TestWebSessionEndpoint(t *testing.T) {
	ts, _ := testWeb(t)
	cookie, csrf := doLogin(t, ts, testToken)
	req, _ := http.NewRequest("GET", ts.URL+"/web/session", nil)
	req.AddCookie(&http.Cookie{Name: CookieName, Value: cookie})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/web/session: %d", resp.StatusCode)
	}
	var out struct {
		CSRFToken string `json:"csrf_token"`
		Actor     string `json:"actor"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.CSRFToken != csrf {
		t.Errorf("csrf в /web/session != csrf из login: %q vs %q", out.CSRFToken, csrf)
	}
	if out.Actor == "" {
		t.Errorf("пустой actor")
	}
}

// Ограничение частоты входа: > login_rate_per_min попыток → 429.
func TestWebLoginRateLimit(t *testing.T) {
	cfg := config.Defaults()
	cfg.Web.LoginRatePerMin = 3
	path := filepath.Join(t.TempDir(), "runpilot.db")
	st, _ := store.Open(path)
	defer func() { _ = st.Close() }()
	clk := clock.NewReal()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	dummy := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	srv := New(cfg, st, dummy, clk, log)
	srv.SetToken(testToken)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	var last int
	for i := 0; i < 10; i++ {
		body, _ := json.Marshal(map[string]string{"token": testToken})
		resp, err := http.Post(ts.URL+"/web/login", "application/json", strings.NewReader(string(body)))
		if err != nil {
			t.Fatal(err)
		}
		last = resp.StatusCode
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("после >3 попыток за минуту: %d, хочу 429", last)
	}
}
