// Package web — веб-слушатель координатора (v2 раздел 2.1, 16, 15.1).
//
// Веб слушает только петлевой адрес (tailscale serve — единственный путь
// наружу). Монтирует ТЕ ЖЕ обработчики /api/v1/*, что и API-слушатель
// (api.Server.Routes), с аутентификацией по cookie вместо Bearer и
// CSRF-защитой изменяющих запросов. Отдельной логики данных для веба нет.
package web

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"runpilot/internal/clock"
	"runpilot/internal/config"
	"runpilot/internal/store"
)

func init() {
	// MIME для встроенного фронтенда (модули и шрифты): без text/javascript
	// браузер не загрузит <script type="module"> (консольная ошибка, R4).
	mime.AddExtensionType(".mjs", "text/javascript")
	mime.AddExtensionType(".module.js", "text/javascript")
	mime.AddExtensionType(".webmanifest", "application/manifest+json")
	mime.AddExtensionType(".woff2", "font/woff2")
}

// Константы протокола веба (v2 раздел 16).
const (
	// CookieName — cookie сессии браузера.
	CookieName = "runpilot_session"
	// CSRFHeader — заголовок с CSRF-токеном изменяющих запросов.
	CSRFHeader = "X-RUNPILOT-CSRF"
	// HeaderTailscaleUser — заголовок tailscale serve с логином оператора.
	HeaderTailscaleUser = "Tailscale-User-Login"
)

// csp — Content-Security-Policy веба (v2 раздел 16): внешних ресурсов нет.
// script-src-elem: 'self' для внешних <script src>, + hash для ЕДИНСТВЕННОГО
// инлайн-скрипта — importmap. Тело importmap ОДИНАКОВО в index.html и
// login.html (одна строка, без пробелов вокруг JSON) — один hash покрывает оба.
// Если тело importmap меняется (в ОБОИХ файлах) — пересчитать hash:
//   printf '%s' '<тело>' | openssl dgst -sha256 -binary | openssl base64 -A
// Регрессионная проверка «hash == тело importmap» — TestCSPImportMapHash.
const csp = "default-src 'self'; connect-src 'self'; img-src 'self' data:; " +
	"font-src 'self'; script-src-elem 'self' 'sha256-KmgfA2MRZFHH1i6FTDy9bKsDsG/BZ8yJ94fff5obgeQ='; " +
	"frame-ancestors 'none'"

// Server — веб-слушатель.
type Server struct {
	cfg     *config.Config
	st      *store.Store
	routes  http.Handler // api.Server.Routes()
	clk     clock.Clock
	log     *slog.Logger
	token   string
	secure  bool
	version string
	// termHandler — мост терминала в браузере (13.4 ТЗ): /web/ws/term/{sid}.
	// nil — терминал недоступен (404). Ставится SetTerminalHandler из serve.
	termHandler func(w http.ResponseWriter, r *http.Request, sid, actor string)
	// enroll — api.EnrollHandler: /enroll/<token>/… публично (14.2),
	// nil — подключение узла с веб-слушателя недоступно (404).
	enroll http.Handler
	// static — встроенный фронтенд web/ (укоренён в web/); отдаёт стили, app/,
	// vendor/, fonts/, icons/ публично, login.html без cookie, index.html — с cookie.
	static fs.FS

	// rateLimiter — попытки входа (web.login_rate_per_min на сервис).
	rateMu  sync.Mutex
	rateAt  []time.Time
	rateCap int
}

// New собирает веб-слушатель. routes — обработчики /api/v1/* (без auth).
func New(cfg *config.Config, st *store.Store, routes http.Handler, clk clock.Clock, log *slog.Logger) *Server {
	secure := strings.HasPrefix(cfg.Web.PublicURL, "https")
	return &Server{
		cfg:     cfg,
		st:      st,
		routes:  routes,
		clk:     clk,
		log:     log,
		token:   "", // устанавливается SetToken из serve (env)
		secure:  secure,
		rateCap: cfg.Web.LoginRatePerMin,
	}
}

// SetToken — операторский токен (значение token_env из env).
func (s *Server) SetToken(t string) { s.token = t }

// SetVersion — версия координатора (для /web/session).
func (s *Server) SetVersion(v string) { s.version = v }

// SetTerminalHandler — мост терминала в браузере (13.4 ТЗ): GET
// /web/ws/term/{sid} (WS-апгрейд) → fn(w, r, sid, actor). cookieAuth идёт
// перед вызовом (как для /api/v1/*).
func (s *Server) SetTerminalHandler(fn func(w http.ResponseWriter, r *http.Request, sid, actor string)) {
	s.termHandler = fn
}

// SetEnrollHandler — маршруты подключения узла (14.2): /enroll/<token>/…
// публично (токен в пути — аутентификация; curl с чистой машины без cookie).
func (s *Server) SetEnrollHandler(h http.Handler) { s.enroll = h }

// SetStaticFS — встроенный фронтенд web/ (runpilotweb.FS). Без него страницы
// входа/оболочки откатываются на встроенный минимальный HTML.
func (s *Server) SetStaticFS(fsys fs.FS) { s.static = fsys }

// Handler — mux веба. Публичное (без cookie): страница входа + статика
// фронтенда (стили, app, vendor, fonts, icons) + /enroll/* (подключение
// узла: токен в пути — аутентификация, curl с чистой машины не имеет
// cookie веба). Защищённое (cookie): /, /web/session, /web/sessions,
// /web/logout и /api/v1/* (cookie + CSRF). Все остальные пути без
// cookie → 401 (W1 CONTROL).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	// Статика фронтенда — публично (CSP 'self'; данные защищаются на API).
	// s.static укоренён в web/, поэтому /styles/x → web/styles/x напрямую.
	if s.static != nil {
		fileSrv := http.FileServer(http.FS(s.static))
		for _, p := range staticPrefixes {
			mux.Handle("GET "+p, fileSrv)
		}
		mux.Handle("GET /sw.js", s.staticFile("sw.js", "application/javascript"))
		mux.Handle("GET /manifest.webmanifest", s.staticFile("manifest.webmanifest", "application/manifest+json"))
		mux.Handle("GET /favicon.ico", s.staticFile("favicon.ico", "image/x-icon"))
		mux.Handle("GET /icon.svg", s.staticFile("icon.svg", "image/svg+xml"))
		mux.Handle("GET /icon-32.png", s.staticFile("icon-32.png", "image/png"))
		mux.Handle("GET /icon-180.png", s.staticFile("icon-180.png", "image/png"))
	}
	// Страница входа — единственная HTML-страница без cookie.
	mux.HandleFunc("GET /web/login", s.handleLoginEntry)
	mux.HandleFunc("POST /web/login", s.handleLogin)
	// Защищённое.
	mux.Handle("POST /web/logout", s.cookieAuth(http.HandlerFunc(s.handleLogout)))
	mux.Handle("GET /web/session", s.cookieAuth(http.HandlerFunc(s.handleSession)))
	mux.Handle("GET /web/sessions", s.cookieAuth(http.HandlerFunc(s.handleSessionsList)))
	// Терминал в браузере (13.4 ТЗ): WS-апгрейд, cookieAuth + проверка Origin.
	mux.Handle("/web/ws/term/", s.cookieAuth(http.HandlerFunc(s.handleTerminal)))
	// Подключение узла (14.2): install.sh/runpilot/claim — токен в пути, cookie
	// веба не требуется (иначе curl -fsSL … | bash с чистой машины → 401).
	// Отдельный api.EnrollHandler: Routes() /enroll не содержит.
	if s.enroll != nil {
		mux.Handle("/enroll/", s.enroll)
	}
	mux.Handle("/api/v1/", s.apiHandler())
	// SPA-оболочка: / только с cookie (без cookie → 401).
	mux.HandleFunc("/", s.handleShell)
	return s.security(s.logRoute(mux))
}

// staticPrefixes — публичные поддерева веб-статистики (без cookie).
var staticPrefixes = []string{
	"/styles/", "/app/", "/vendor/", "/fonts/", "/icons/",
}

// staticFile — отдаёт один встроенный файл (если есть) с явным Content-Type.
func (s *Server) staticFile(name, contentType string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.static == nil {
			s.unauthorized(w, r)
			return
		}
		data, err := fs.ReadFile(s.static, name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", contentType)
		_, _ = w.Write(data)
	})
}

// apiHandler — cookie-аутентификация (сначала), затем CSRF для изменяющих
// методов. Порядок: без cookie → 401 (для всех), с cookie без CSRF → 403.
func (s *Server) apiHandler() http.Handler {
	return s.cookieAuth(s.csrf(s.routes))
}

// actor — Tailscale-User-Login, иначе первые символы id сессии (v2 16).
func actor(r *http.Request) string {
	if u := r.Header.Get(HeaderTailscaleUser); u != "" {
		return u
	}
	if v, ok := r.Context().Value(actorKey{}).(string); ok {
		return v
	}
	return ""
}

// sessionID — id сессии (id_hash) из контекста (ставит cookieAuth).
func sessionID(r *http.Request) string {
	v, _ := r.Context().Value(sessionIDKey{}).(string)
	return v
}

// lookupSession — cookie runpilot_session → web_session (действительна, не отозвана,
// не истекла). Единая проверка для cookieAuth и SPA-оболочки (handleShell).
func (s *Server) lookupSession(r *http.Request) (store.WebSession, bool) {
	var ws store.WebSession
	cookie, err := r.Cookie(CookieName)
	if err != nil || cookie.Value == "" {
		return ws, false
	}
	ws, err = s.st.GetWebSession(sha256hex(cookie.Value))
	if err != nil {
		return ws, false
	}
	ttl := time.Duration(s.cfg.Web.SessionTTLDays) * hoursPerDay * time.Hour
	if s.clk.Now().After(ws.TSLogin.Add(ttl)) {
		return ws, false
	}
	return ws, true
}

// cookieAuth — проверка cookie runpilot_session → web_session. Недействительна → 401.
func (s *Server) cookieAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, ok := s.lookupSession(r)
		if !ok {
			s.unauthorized(w, r)
			return
		}
		idHash := ws.IDHash
		actor := r.Header.Get(HeaderTailscaleUser)
		if actor == "" {
			actor = actorFromHash(idHash)
		}
		ctx := r.Context()
		ctx = context.WithValue(ctx, actorKey{}, actor)
		ctx = context.WithValue(ctx, sessionIDKey{}, idHash)
		// Продление (best-effort).
		_ = s.st.TouchWebSession(idHash, s.clk.Now())
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// csrf — изменяющие методы (POST/PATCH/PUT/DELETE) требуют заголовок
// X-RUNPILOT-CSRF, совпадающий с csrf_token сессии. Иначе → 403.
func (s *Server) csrf(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isMutation(r.Method) {
			idHash := sessionID(r)
			ws, err := s.st.GetWebSession(idHash)
			if err != nil || subtle.ConstantTimeCompare([]byte(ws.CSRFToken), []byte(r.Header.Get(CSRFHeader))) != 1 {
				s.forbidden(w, r)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func isMutation(m string) bool {
	switch m {
	case http.MethodPost, http.MethodPatch, http.MethodPut, http.MethodDelete:
		return true
	}
	return false
}

// security — заголовки ответа веба (v2 раздел 16).
func (s *Server) security(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

// logRoute — лог изменяющих запросов (аудит строится по actor, раздел 16).
func (s *Server) logRoute(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isMutation(r.Method) {
			s.log.Info("web: изменяющий запрос", "path", r.URL.Path,
				"actor", actor(r), "method", r.Method)
		}
		next.ServeHTTP(w, r)
	})
}

// handleTerminal — мост терминала в браузере (13.4 ТЗ): GET
// /web/ws/term/{sid} (WS-апгрейд). cookieAuth уже прошёл (обёртка в Handler);
// далее проверка Origin против web.public_url и вызов моста.
func (s *Server) handleTerminal(w http.ResponseWriter, r *http.Request) {
	if s.termHandler == nil {
		s.webError(w, http.StatusNotFound, "TERM_UNAVAILABLE", "терминал недоступен")
		return
	}
	if !s.originAllowed(r) {
		s.forbidden(w, r)
		return
	}
	sid := strings.TrimPrefix(r.URL.Path, "/web/ws/term/")
	s.termHandler(w, r, sid, actor(r))
}

// originAllowed — Origin запроса совпадает с origin web.public_url. Без Origin
// (не браузер / raw-клиент) или без public_url — допускается.
func (s *Server) originAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" || s.cfg.Web.PublicURL == "" {
		return true
	}
	pub, err := url.Parse(s.cfg.Web.PublicURL)
	if err != nil {
		return true
	}
	o, err := url.Parse(origin)
	if err != nil {
		return false
	}
	// Сравнение origin = scheme://host (без литерала «://» — строит url.URL).
	pubOrigin := (&url.URL{Scheme: pub.Scheme, Host: pub.Host}).String()
	oOrigin := (&url.URL{Scheme: o.Scheme, Host: o.Host}).String()
	return pubOrigin == oOrigin
}

func (s *Server) unauthorized(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	json.NewEncoder(w).Encode(map[string]string{
		"code": "UNAUTHORIZED", "message": "нужен вход",
	})
}

func (s *Server) forbidden(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	json.NewEncoder(w).Encode(map[string]string{
		"code": "CSRF_REQUIRED", "message": "нет корректного " + CSRFHeader,
	})
}

func sha256hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// actorFromHash — первые символы id_hash (при отсутствии Tailscale-User).
func actorFromHash(idHash string) string {
	if len(idHash) < actorHashLen {
		return idHash
	}
	return idHash[:actorHashLen]
}

// randomToken — hex-строка из n случайных байт.
func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (s *Server) webError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"code": code, "message": msg})
}

// Ключи контекста (актор и id сессии, ставит cookieAuth).
type (
	actorKey     struct{}
	sessionIDKey struct{}
)
