// Package fakeorch — тестовый двойник сайта оркестратора (ТЗ Relay §17).
// Локальное веб-приложение с DOM под профиль fakeorch: поле ввода, кнопка,
// поле файлов с чипами, потоковый ответ, лента сообщений.
package fakeorch

import (
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
)

// answer — ответ оркестратора: блоки в порядке документа.
type answer struct {
	Blocks []Block `json:"blocks"`
}

// Block — текст или code.
type Block struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

// RecvFile — файл вложения (для побайтовой сверки).
type RecvFile struct {
	Name  string `json:"name"`
	Bytes []byte `json:"bytes_b64"`
}

// RecvMsg — принятое сообщение.
type RecvMsg struct {
	Chat  string     `json:"chat"`
	Text  string     `json:"text"`
	Files []RecvFile `json:"files"`
}

// Cfg — управляемое состояние двойника (/_set).
type Cfg struct {
	LoginRequired bool    `json:"login_required"`
	AnswerDelayMS int     `json:"answer_delay_ms"`
	FileDelayMS   int     `json:"file_delay_ms"`
	Template      string  `json:"template"`
	Raw           *answer `json:"raw"`
	RateLimit     bool    `json:"rate_limit"`
	Error         bool    `json:"error"`
	LostSend      bool    `json:"lost_send"`
	RedirectChat  string  `json:"redirect_chat"`
	AbortGen      bool    `json:"abort_gen"`
}

type server struct {
	mu     sync.Mutex
	chat   map[string][]msg
	cfg    Cfg
	recv   []RecvMsg
	tokens map[string]bool // cookie -> true
}

type msg struct {
	Role   string  `json:"role"` // user | assistant
	Blocks []Block `json:"blocks"`
}

// New — новый двойник.
func New() *Server {
	return &Server{inner: &server{
		chat:   map[string][]msg{},
		tokens: map[string]bool{},
	}}
}

// Server — обёртка (публичный тип).
type Server struct{ inner *server }

// Handler — HTTP-обработчик.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/chat/", s.inner.chatPage)
	mux.HandleFunc("/_set", s.inner.set)
	mux.HandleFunc("/_received", s.inner.received)
	mux.HandleFunc("/_config", s.inner.config)
	mux.HandleFunc("/_login", s.inner.login)
	mux.HandleFunc("/send", s.inner.send)
	mux.HandleFunc("/reset", s.inner.reset)
	return mux
}

// set — установка состояния (/_set).
func (s *server) set(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	var c Cfg
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	s.cfg = c
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	io.WriteString(w, `{"ok":true}`)
}

// config — текущее состояние (для страницы: задержки, логин).
func (s *server) config(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	c := s.cfg
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(c)
}

// received — принятые сообщения и файлы (побайтовая сверка).
func (s *server) received(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	out := s.recv
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// reset — очистка лент и принятых.
func (s *server) reset(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.chat = map[string][]msg{}
	s.recv = nil
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	io.WriteString(w, `{"ok":true}`)
}

// login — вход (cookie).
func (s *server) login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	_ = r.ParseForm()
	tok := r.FormValue("token")
	if tok == "" {
		tok = "op"
	}
	http.SetCookie(w, &http.Cookie{Name: "fakeorch", Value: tok, Path: "/"})
	s.mu.Lock()
	s.tokens[tok] = true
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	io.WriteString(w, `{"ok":true}`)
}

func (s *server) loggedIn(r *http.Request) bool {
	c, err := r.Cookie("fakeorch")
	if err != nil || c.Value == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tokens[c.Value]
}

// chatPage — страница чата /chat/<id>.
func (s *server) chatPage(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/chat/")
	if id == "" {
		http.NotFound(w, r)
		return
	}
	s.mu.Lock()
	redirect := s.cfg.RedirectChat
	loginReq := s.cfg.LoginRequired
	s.mu.Unlock()

	if redirect != "" && redirect != id {
		http.Redirect(w, r, "/chat/"+redirect, http.StatusFound)
		return
	}
	if loginReq && !s.loggedIn(r) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, pageLogin)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	io.WriteString(w, strings.ReplaceAll(pageChat, "__ID__", id))
}

// send — отправка сообщения + файлов (multipart).
func (s *server) send(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseMultipartForm(maxUpload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	chat := r.FormValue("chat")
	text := r.FormValue("text")
	var files []RecvFile
	for _, fh := range r.MultipartForm.File["files"] {
		f, err := fh.Open()
		if err != nil {
			continue
		}
		b, err := io.ReadAll(f)
		f.Close()
		if err != nil {
			continue
		}
		files = append(files, RecvFile{Name: fh.Filename, Bytes: b})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })

	s.mu.Lock()
	lost := s.cfg.LostSend
	if !lost {
		s.recv = append(s.recv, RecvMsg{Chat: chat, Text: text, Files: files})
	}
	s.mu.Unlock()

	if lost {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"lost":true}`)
		return
	}
	out := struct {
		Lost   bool    `json:"lost"`
		Answer answer  `json:"answer"`
	}{Lost: false, Answer: s.answer()}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// answer — текущий ответ по шаблону или raw.
func (s *server) answer() answer {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cfg.Raw != nil {
		return *s.cfg.Raw
	}
	if a, ok := templates[s.cfg.Template]; ok {
		return a
	}
	return templates["text_only"]
}
