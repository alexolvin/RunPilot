package web

import (
	"crypto/subtle"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/url"
	"strings"
	"time"

	"runpilot/internal/store"
)

// serveStaticHTML — отдаёт встроенный web/<name> (text/html), либо fallback.
func (s *Server) serveStaticHTML(name, fallback string) ([]byte, bool) {
	if s.static != nil {
		if data, err := fs.ReadFile(s.static, name); err == nil {
			return data, true
		}
	}
	return []byte(fallback), false
}

// handleLoginEntry — GET /web/login: страница входа (единственный HTML-маршрут
// без cookie — через неё получают сессию). Служит web/login.html; без встроенного
// фронтенда — минимальный встроенный HTML (фолбэк).
func (s *Server) handleLoginEntry(w http.ResponseWriter, r *http.Request) {
	data, _ := s.serveStaticHTML("login.html", fallbackLoginPageHTML)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// handleLogin — POST /web/login {token}: аутентификация операторским токеном.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !s.rateAllow() {
		s.webError(w, http.StatusTooManyRequests, "RATE_LIMITED",
			"слишком много попыток входа")
		return
	}
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.webError(w, http.StatusBadRequest, "BAD_REQUEST", "тело: нужен токен")
		return
	}
	if !s.checkToken(body.Token) {
		s.webError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Неверный токен")
		return
	}
	id, err := randomToken(randTokenBytes)
	if err != nil {
		s.webError(w, http.StatusInternalServerError, "TOKEN", "создание сессии")
		return
	}
	csrf, err := randomToken(randTokenBytes)
	if err != nil {
		s.webError(w, http.StatusInternalServerError, "CSRF", "создание сессии")
		return
	}
	now := s.clk.Now()
	ws := store.WebSession{
		IDHash:    sha256hex(id),
		CSRFToken: csrf,
		CreatedAt: now,
		LastSeen:  now,
		UserAgent: r.UserAgent(),
		TSLogin:   now,
	}
	if err := s.st.CreateWebSession(ws); err != nil {
		s.webError(w, http.StatusInternalServerError, "STORE", "создание сессии")
		return
	}
	ttl := time.Duration(s.cfg.Web.SessionTTLDays) * hoursPerDay * time.Hour
	setSessionCookie(w, id, ttl, s.secure)

	actor := r.Header.Get(HeaderTailscaleUser)
	if actor == "" {
		actor = actorFromHash(ws.IDHash)
	}
	s.log.Info("web: вход", "actor", actor, "ua", r.UserAgent())
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"csrf_token": csrf, "actor": actor, "version": s.version,
	})
}

// handleLogout — POST /web/logout: отзыв текущей сессии.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	id := sessionID(r)
	if err := s.st.RevokeWebSession(id, s.clk.Now()); err != nil && err != store.ErrNotFound {
		s.webError(w, http.StatusInternalServerError, "STORE", err.Error())
		return
	}
	clearSessionCookie(w, s.secure)
	s.log.Info("web: выход", "actor", actor(r))
	w.WriteHeader(http.StatusNoContent)
}

// handleSession — GET /web/session: данные текущей сессии + CSRF-токен.
func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	ws, err := s.st.GetWebSession(sessionID(r))
	if err != nil {
		s.unauthorized(w, r)
		return
	}
	ttl := time.Duration(s.cfg.Web.SessionTTLDays) * hoursPerDay * time.Hour
	expires := ws.TSLogin.Add(ttl).UTC().Format(time.RFC3339)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"csrf_token": ws.CSRFToken,
		"actor":      actor(r),
		"version":    s.version,
		"expires_at": expires,
	})
}

// handleSessionsList — GET /web/sessions: все активные сессии («Доступ»).
func (s *Server) handleSessionsList(w http.ResponseWriter, r *http.Request) {
	all, err := s.st.ListWebSessions()
	if err != nil {
		s.webError(w, http.StatusInternalServerError, "STORE", err.Error())
		return
	}
	current := sessionID(r)
	rows := make([]map[string]any, 0, len(all))
	for _, ws := range all {
		actor := actorFromHash(ws.IDHash)
		rows = append(rows, map[string]any{
			"id":         ws.IDHash[:actorHashLen],
			"actor":      actor,
			"created_at": ws.CreatedAt.UTC().Format(time.RFC3339),
			"last_seen":  ws.LastSeen.UTC().Format(time.RFC3339),
			"user_agent": ws.UserAgent,
			"current":    ws.IDHash == current,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"sessions": rows})
}

// browserDocRequest — запрос документа браузером (адресная строка, ссылка):
// Accept содержит text/html. fetch/API-клиенты присылают */* или конкретный
// тип — для них остаётся 401 (W1 CONTROL).
func browserDocRequest(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}

// handleShell — SPA-оболочка: только с действующей сессией (ТЗ 11.1).
// Без сессии: браузер (Accept: text/html) → 302 на /web/login?next=… — после
// входа возврат на запрошенную страницу (O4); скриптовые клиенты → 401
// (W1 CONTROL). Служит web/index.html; без встроенного фронтенда —
// минимальный HTML (фолбэк).
func (s *Server) handleShell(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.lookupSession(r); !ok {
		if browserDocRequest(r) {
			http.Redirect(w, r, loginPath+"?next="+url.QueryEscape(r.URL.Path), http.StatusFound)
			return
		}
		s.unauthorized(w, r)
		return
	}
	data, _ := s.serveStaticHTML("index.html", fallbackShellPageHTML)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// checkToken — сравнение с операторским токеном (constant-time).
func (s *Server) checkToken(candidate string) bool {
	if s.token == "" || candidate == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(candidate), []byte(s.token)) == 1
}

// rateAllow — попыток входа <= web.login_rate_per_min в минуту (на сервис).
func (s *Server) rateAllow() bool {
	s.rateMu.Lock()
	defer s.rateMu.Unlock()
	now := s.clk.Now()
	oneMinAgo := now.Add(-time.Minute)
	kept := s.rateAt[:0]
	for _, t := range s.rateAt {
		if t.After(oneMinAgo) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= s.rateCap {
		s.rateAt = kept
		return false
	}
	s.rateAt = append(kept, now)
	return true
}

// setSessionCookie — cookie runpilot_session (HttpOnly, Secure*, SameSite=Strict).
func setSessionCookie(w http.ResponseWriter, id string, ttl time.Duration, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    id,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		Expires:  time.Now().UTC().Add(ttl),
		MaxAge:   int(ttl / time.Second),
	})
}

func clearSessionCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: "", Path: "/", HttpOnly: true,
		Secure: secure, SameSite: http.SameSiteStrictMode,
		Expires: time.Unix(0, 0), MaxAge: -1,
	})
}

// fallbackLoginPageHTML — страница входа (W1: минимальная, без внешних ресурсов).
// Используется, только если встроенный фронтенд web/ недоступен.
const fallbackLoginPageHTML = `<!doctype html>
<html lang="ru">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>RunPilot — вход</title>
<style>
:root{--bg:#F8FAFC;--surface:#FFFFFF;--border:#E2E8F0;--text:#0F172A;--muted:#64748B;--accent:#2563EB;--danger:#DC2626}
body{margin:0;font-family:Inter,system-ui,sans-serif;background:var(--bg);color:var(--text);display:flex;align-items:center;justify-content:center;min-height:100vh}
.card{background:var(--surface);border:1px solid var(--border);border-radius:12px;padding:28px;width:340px;box-shadow:0 1px 2px rgba(0,0,0,.04)}
h1{font-size:20px;margin:0 0 4px}
p.sub{color:var(--muted);font-size:13px;margin:0 0 18px}
label{display:block;font-size:13px;margin-bottom:6px}
input{width:100%;box-sizing:border-box;padding:9px 10px;border:1px solid var(--border);border-radius:8px;font-size:14px}
button{margin-top:14px;width:100%;padding:10px;background:var(--accent);color:#fff;border:0;border-radius:8px;font-size:14px;cursor:pointer}
.err{color:var(--danger);font-size:13px;margin-top:10px;min-height:1em}
</style>
</head>
<body>
<form class="card" id="f">
<h1>RunPilot</h1>
<p class="sub">Вход для оператора</p>
<label for="t">Токен оператора</label>
<input id="t" name="token" type="password" autocomplete="off" required>
<button type="submit">Войти</button>
<div class="err" id="err"></div>
</form>
<script>
document.getElementById('f').addEventListener('submit', async (e) => {
  e.preventDefault();
  const err = document.getElementById('err');
  err.textContent = '';
  const r = await fetch('/web/login', {
    method: 'POST',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify({token: document.getElementById('t').value})
  });
  if (r.status === 401) { err.textContent = 'Неверный токен'; return; }
  if (r.status === 429) { err.textContent = 'Слишком много попыток'; return; }
  if (!r.ok) { err.textContent = 'Ошибка ' + r.status; return; }
  const d = await r.json();
  window.csrf = d.csrf_token;
  // O4: возврат на запрошенную страницу (?next): только тот же origin.
  const next = new URLSearchParams(location.search).get('next') || '/';
  location.href = (next.startsWith('/') && !next.startsWith('//')) || next.startsWith('#/')
    ? next : '/';
});
</script>
</body>
</html>`

// fallbackShellPageHTML — SPA-оболочка (W1: минимальная). Используется, только
// если встроенный фронтенд web/ недоступен (реальное приложение — web/index.html).
const fallbackShellPageHTML = `<!doctype html>
<html lang="ru">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>RunPilot</title>
</head>
<body>
<div id="app">RunPilot — оболочка загружается (приложение — этап W3).</div>
<script>
(async () => {
  const r = await fetch('/web/session');
  if (!r.ok) { location.href = '/web/login'; return; }
  const d = await r.json();
  document.getElementById('app').textContent =
    'RunPilot — вы вошли как ' + d.actor + ' (v' + d.version + ')';
})().catch(() => { location.href = '/web/login'; });
</script>
</body>
</html>`
