//go:build e2e

// CONTROL W8 (v2 раздел 20, строка W8) — живой e2e на РЕАЛЬНОМ процессе
// `runpilot serve` (координатор со встроенным узлом) + реальный tmux (сессия с
// bash-панелью + @runpilot_sid). Терминал в браузере (13.4 ТЗ) проверяется raw-
// WebSocket-клиентом на /web/ws/term/{sid} (cookie после /web/login).
//
// Покрывает строки CONTROL W8:
//
//  1. 50 циклов открыть/закрыть: `tmux list-clients` возвращается к
//     исходному, процессов `tmux attach` от узла — 0;
//  2. размер окна tmux до и после одинаков;
//  3. эхо нажатия на стенде ≤ 50 мс p95;
//  4. превышение web.terminals_per_node_max → отказ с кодом (503);
//  5. закрытие по бездействию на виртуальных часах — unit-тест
//     internal/api.TestTermIdleReapVirtualClock (здесь только живая часть).
//
// Запуск (вне make check — build-тег e2e):
//
//	go test -tags e2e -count=1 -timeout 15m ./test/e2e/w8
//
// Evidence → docs/evidence/W8/control/*.json (флаг -out).
package w8

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

const (
	operatorToken = "runpilot-w8-e2e-operator-token-0000" // ≥ limits.token_min_len (16)

	termSID = "W8TERM0001" // @runpilot_sid панели (Crockford base32, 10 символов)
	tmuxSess = "w8t"       // имя tmux-сессии с bash-панелью

	echoSamples    = 40 // замеров эхо для p95
	echoP95Limit   = 50 * time.Millisecond
	cycleCount     = 50 // циклов открыть/закрыть
	termMaxPerNode = 2  // web.terminals_per_node_max в конфиге стенда
)

var (
	outFlag = flag.String("out", "docs/evidence/W8/control", "каталог evidence (пусто — не писать)")

	rootDir  string
	runpilotBin   string
)

func TestMain(m *testing.M) {
	flag.Parse()
	r, err := findModuleRoot()
	if err != nil {
		e2efatal("go.mod: %v", err)
	}
	rootDir = r
	runpilotBin = filepath.Join(os.TempDir(), fmt.Sprintf("runpilot-w8-e2e-%d", os.Getpid()))
	if err := buildRunpilot(runpilotBin); err != nil {
		e2efatal("runpilot: %v", err)
	}
	defer os.Remove(runpilotBin)
	os.Exit(m.Run())
}

func e2efatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "e2e: "+format+"\n", args...)
	os.Exit(2)
}

func findModuleRoot() (string, error) {
	d, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d, nil
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "", fmt.Errorf("go.mod не найден из %s", d)
		}
		d = parent
	}
}

func buildRunpilot(out string) error {
	cmd := exec.Command("go", "build", "-o", out, "./cmd/runpilot")
	cmd.Dir = rootDir
	if b, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%v\n%s", err, b)
	}
	return nil
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("freePort: %v", err)
	}
	p := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return p
}

// ---- машина: координатор (runpilot serve, встроенный узел) + tmux-сокет ----

type proc struct {
	cmd  *exec.Cmd
	logf *os.File
}

type machine struct {
	home    string
	apiURL  string
	webURL  string
	socket  string
	cfgPath string
}

func newMachine(t *testing.T) *machine {
	t.Helper()
	home := t.TempDir()
	apiPort, webPort := freePort(t), freePort(t)
	socket := "runpilotw8" // изолированный сокет координатора/узла

	for _, d := range []string{home, filepath.Join(home, "data")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Профиль qwen: команда = bash (живая оболочка для эха); кодер не нужен —
	// проверяется транспорт PTY, а не детекция.
	profDir := filepath.Join(home, ".config", "runpilot", "profiles")
	if err := os.MkdirAll(profDir, 0o755); err != nil {
		t.Fatal(err)
	}
	prof := "name: qwen\ncommand: /bin/bash\nversion_cmd: echo 0.24.4\n" +
		"submit_keys: Enter\ncancel_keys: Escape\nresume_text: continue\n" +
		"cmdline_regex: '(^|/)bash(\\s|$)'\nclassify_lines: 3\ntab_width: 8\n"
	if err := os.WriteFile(filepath.Join(profDir, "qwen.yaml"), []byte(prof), 0o644); err != nil {
		t.Fatal(err)
	}

	apiURL := "http://127.0.0.1:" + strconv.Itoa(apiPort)
	webURL := "http://127.0.0.1:" + strconv.Itoa(webPort)
	cfg := fmt.Sprintf(`coordinator:
  bind: 127.0.0.1
  allow_cidrs: ["127.0.0.0/8"]
  api_port: %d
  gateway_port: %d
  token_env: RUNPILOT_TOKEN
  db_path: %q
  dist_dir: %q
  embedded_node: true
  heartbeat_sec: 1
  shutdown_grace_sec: 3
  bind_retry_sec: 1
web:
  bind: 127.0.0.1
  port: %d
  public_url: %q
  terminals_per_node_max: %d
  terminal_idle_min: 30
  session_ttl_days: 7
  login_rate_per_min: 1000
node:
  scan_interval_sec: 1
  tmux_sockets: [%q]
  project_roots_default: [%q]
client:
  coordinator: %q
  token_env: RUNPILOT_TOKEN
monitor:
  health_interval_sec: 1
  metrics_interval_sec: 1
`, apiPort, apiPort+1, filepath.Join(home, "data", "runpilot.db"),
		filepath.Join(home, "dist"), webPort, webURL, termMaxPerNode, socket, home, apiURL)

	cfgDir := filepath.Join(home, ".config", "runpilot")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(cfgDir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	m := &machine{home: home, apiURL: apiURL, webURL: webURL, socket: socket, cfgPath: cfgPath}
	return m
}

func startProc(t *testing.T, name, bin string, args []string, env []string) *proc {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), env...)
	logf, err := os.CreateTemp("", "runpilot-w8-"+name+"-*.log")
	if err == nil {
		cmd.Stdout = logf
		cmd.Stderr = logf
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("%s: запуск: %v", name, err)
	}
	p := &proc{cmd: cmd, logf: logf}
	t.Cleanup(func() {
		killProc(p)
		if logf != nil {
			_ = logf.Close()
			_ = os.Remove(logf.Name())
		}
	})
	return p
}

func killProc(p *proc) {
	if p == nil || p.cmd.Process == nil {
		return
	}
	_ = p.cmd.Process.Kill()
	done := make(chan struct{})
	go func() { _ = p.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
}

func (m *machine) env() []string {
	return []string{"HOME=" + m.home, "RUNPILOT_TOKEN=" + operatorToken}
}

func (m *machine) startCoordinator(t *testing.T) *proc {
	return startProc(t, "coord", runpilotBin, []string{"serve", "--config", m.cfgPath}, m.env())
}

func readLog(t *testing.T, p *proc) string {
	t.Helper()
	if p == nil || p.logf == nil {
		return ""
	}
	_ = p.logf.Sync()
	b, _ := os.ReadFile(p.logf.Name())
	return string(b)
}

// waitForAPI — координатор отвечает на /api/v1/state.
func waitForAPI(t *testing.T, m *machine, coord *proc) {
	t.Helper()
	deadline := time.Now().Add(40 * time.Second)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequest("GET", m.apiURL+"/api/v1/state", nil)
		req.Header.Set("Authorization", "Bearer "+operatorToken)
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("координатор не ответил за 40 с: %s", readLog(t, coord))
}

// ---- tmux: сессия с bash-панелью + @runpilot_sid ----

func tmuxCmd(socket string, args ...string) *exec.Cmd {
	full := append([]string{"-L", socket}, args...)
	return exec.Command("tmux", full...)
}

func tmuxOut(t *testing.T, socket string, args ...string) (string, error) {
	t.Helper()
	b, err := tmuxCmd(socket, args...).CombinedOutput()
	return string(b), err
}

// setupPane — сессия w8t (bash 80x24) + @runpilot_sid, ждать, пока координатор
// увидел панель (узел отчитался снимком с SID).
func setupPane(t *testing.T, m *machine, coord *proc) {
	t.Helper()
	// Чистый сокет.
	_, _ = tmuxOut(t, m.socket, "kill-server")
	out, err := tmuxOut(t, m.socket, "new-session", "-d", "-s", tmuxSess,
		"-x", "80", "-y", "24", "/bin/bash")
	if err != nil {
		t.Fatalf("new-session: %v (%s)", err, out)
	}
	t.Cleanup(func() { _, _ = tmuxOut(t, m.socket, "kill-server") })
	// @runpilot_sid на панели (управляемая панель).
	if out, err := tmuxOut(t, m.socket, "set-option", "-p", "-t", tmuxSess+":0.0",
		"@runpilot_sid", termSID); err != nil {
		t.Fatalf("set @runpilot_sid: %v (%s)", err, out)
	}
	// Ждём, пока узел отчитался панелью с SID (сканер 1×/цикл).
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if paneVisible(t, m) {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("координатор не увидел панель %s за 20 с: %s", termSID, readLog(t, coord))
}

type stateView struct {
	Panes []struct {
		Pane struct {
			SID         string `json:"sid"`
			TmuxSession string `json:"tmux_session"`
			Socket      string `json:"socket"`
		} `json:"Pane"`
		Host string `json:"Host"`
	} `json:"panes"`
}

func paneVisible(t *testing.T, m *machine) bool {
	t.Helper()
	req, _ := http.NewRequest("GET", m.apiURL+"/api/v1/state", nil)
	req.Header.Set("Authorization", "Bearer "+operatorToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	var st stateView
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return false
	}
	for _, p := range st.Panes {
		if p.Pane.SID == termSID && p.Pane.TmuxSession == tmuxSess {
			return true
		}
	}
	return false
}

// ---- веб-вход (cookie) ----

func login(t *testing.T, m *machine) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"token": operatorToken})
	req, _ := http.NewRequest("POST", m.webURL+"/web/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login: %d", resp.StatusCode)
	}
	for _, c := range resp.Cookies() {
		if c.Name == "runpilot_session" {
			return c.Value
		}
	}
	t.Fatalf("login: нет cookie runpilot_session")
	return ""
}

// ---- терминал: raw WS клиент ----

// openTerminal — dial WS /web/ws/term/{sid} с cookie; читает {"type":"open"}
// и возвращает conn + номер PTY-канала.
func openTerminal(t *testing.T, m *machine, cookie string) (*websocket.Conn, int) {
	t.Helper()
	hdr := http.Header{}
	hdr.Set("Cookie", "runpilot_session="+cookie)
	wsURL := "ws" + m.webURL[len("http"):] + "/web/ws/term/" + termSID
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, hdr)
	if err != nil {
		t.Fatalf("dial терминала: %v", err)
	}
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	mt, data, err := conn.ReadMessage()
	if err != nil {
		_ = conn.Close()
		t.Fatalf("чтение open: %v", err)
	}
	if mt != websocket.TextMessage {
		_ = conn.Close()
		t.Fatalf("ожидал JSON open, получил тип %d", mt)
	}
	var msg struct {
		Type string `json:"type"`
		Chan int    `json:"chan"`
		Code string `json:"code"`
	}
	if err := json.Unmarshal(data, &msg); err != nil {
		_ = conn.Close()
		t.Fatalf("разбор open: %v (%s)", err, data)
	}
	if msg.Type != "open" {
		_ = conn.Close()
		t.Fatalf("open: type=%q code=%q, хочу open", msg.Type, msg.Code)
	}
	return conn, msg.Chan
}

// windowSize — «ширина высота» окна сессии.
func windowSize(t *testing.T, socket, session string) (int, int) {
	t.Helper()
	out, err := tmuxOut(t, socket, "display-message", "-p", "-t", session,
		"-F", "#{window_width} #{window_height}")
	if err != nil {
		t.Fatalf("display-message: %v (%s)", err, out)
	}
	f := strings.Fields(strings.TrimSpace(string(out)))
	if len(f) < 2 {
		t.Fatalf("размер окна: пустой ответ %q", out)
	}
	w, _ := strconv.Atoi(f[0])
	h, _ := strconv.Atoi(f[1])
	return w, h
}

// listClients — число клиентов tmux-сессии (attach-процессы).
func listClients(t *testing.T, socket, session string) int {
	t.Helper()
	out, err := tmuxOut(t, socket, "list-clients", "-t", session)
	if err != nil {
		return -1
	}
	n := 0
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if l != "" {
			n++
		}
	}
	return n
}

// attachProcs — число процессов `tmux ... attach-session` на сокете (узла).
func attachProcs(t *testing.T, socket string) int {
	t.Helper()
	out, err := exec.Command("ps", "-A", "-o", "args=").Output()
	if err != nil {
		return -1
	}
	n := 0
	for _, l := range strings.Split(string(out), "\n") {
		if strings.Contains(l, "attach-session") && strings.Contains(l, "-L "+socket) {
			n++
		}
	}
	return n
}

// waitForZeroAttach — ждать, пока не останется attach-процессов на сокете.
func waitForZeroAttach(t *testing.T, socket string, limit time.Duration) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if attachProcs(t, socket) == 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("attach-процессов на сокете %s = %d, хочу 0", socket, attachProcs(t, socket))
}

// waitForClients — ждать, пока list-clients станет want (attach подключается
// асинхронно после pty_open, поэтому немедленная проверка гоночна).
func waitForClients(t *testing.T, socket, session string, want int, limit time.Duration) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if listClients(t, socket, session) == want {
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatalf("list-clients = %d, хочу %d за %v", listClients(t, socket, session), want, limit)
}

// writeEvidence — evidence JSON (по умолчанию <корень модуля>/docs/evidence/W8/control).
func writeEvidence(t *testing.T, name string, data any) {
	t.Helper()
	if *outFlag == "" {
		return
	}
	dir := *outFlag
	if !filepath.IsAbs(dir) { // go test стартует в каталоге пакета — привязываем к корню
		dir = filepath.Join(rootDir, dir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	b, _ := json.MarshalIndent(data, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// percentile — p-квантиль (0..100) отсортированного ряда.
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(math.Ceil(p/100*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// ---------------------------------------------------------------------------

// CONTROL W8.1 + .2 + .3: 50 циклов открыть/закрыть (list-clients к исходному,
// 0 attach-процессов), размер окна стабилен, эхо ≤ 50 мс p95.
func TestW8TerminalCyclesAndEcho(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("нет tmux")
	}
	m := newMachine(t)
	coord := m.startCoordinator(t)
	waitForAPI(t, m, coord)
	setupPane(t, m, coord)
	cookie := login(t, m)

	w0, h0 := windowSize(t, m.socket, tmuxSess)
	baseClients := listClients(t, m.socket, tmuxSess)
	if baseClients != 0 {
		t.Fatalf("list-clients до = %d, хочу 0", baseClients)
	}

	// --- Эхо (30+ замеров в одной сессии) ---
	ec, _ := openTerminal(t, m, cookie)
	var latencies []float64
	for i := 0; i < echoSamples; i++ {
		marker := fmt.Sprintf("w8echo%04d", i)
		ec.SetWriteDeadline(time.Now().Add(2 * time.Second))
		t0 := time.Now()
		if err := ec.WriteMessage(websocket.BinaryMessage,
			[]byte(marker+"\n")); err != nil {
			t.Fatalf("запись эхо %d: %v", i, err)
		}
		buf := ""
		got := false
		deadline := time.Now().Add(1 * time.Second)
		for !got && time.Now().Before(deadline) {
			ec.SetReadDeadline(deadline)
			_, data, err := ec.ReadMessage()
			if err != nil {
				break
			}
			buf += string(data)
			if strings.Contains(buf, marker) {
				got = true
			}
		}
		if !got {
			t.Fatalf("эхо %d (marker %s) не пришло за 1 с; буфер: %q", i, marker, buf)
		}
		latencies = append(latencies, time.Since(t0).Seconds()*1000) // мс (sub-ms точность)
	}
	_ = ec.Close() // → координатор pty_close → SIGTERM attach
	waitForZeroAttach(t, m.socket, 5*time.Second)
	if c := listClients(t, m.socket, tmuxSess); c != baseClients {
		t.Fatalf("list-clients после эхо = %d, было %d", c, baseClients)
	}
	sortLat := append([]float64(nil), latencies...)
	sortFloats(sortLat)
	p95 := percentile(sortLat, 95)
	if p95 > float64(echoP95Limit.Microseconds())/1000 {
		t.Fatalf("эхо p95 = %.1f мс > %v", p95, echoP95Limit)
	}
	t.Logf("эхо: n=%d p95=%.1f мс (лимит %v)", echoSamples, p95, echoP95Limit)

	// --- 50 циклов открыть/закрыть ---
	for i := 0; i < cycleCount; i++ {
		conn, _ := openTerminal(t, m, cookie)
		waitForClients(t, m.socket, tmuxSess, baseClients+1, 3*time.Second)
		_ = conn.Close()
		waitForZeroAttach(t, m.socket, 5*time.Second)
		// list-clients вернулся к исходному.
		deadline := time.Now().Add(5 * time.Second)
		for {
			n := listClients(t, m.socket, tmuxSess)
			if n == baseClients {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("цикл %d: list-clients = %d, хочу %d (не вернулось)", i, n, baseClients)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	// Размер окна до/после одинаков.
	if w1, h1 := windowSize(t, m.socket, tmuxSess); w1 != w0 || h1 != h0 {
		t.Fatalf("размер окна: до %dx%d, после %dx%d", w0, h0, w1, h1)
	}
	if a := attachProcs(t, m.socket); a != 0 {
		t.Fatalf("attach-процессов после циклов = %d, хочу 0", a)
	}

	writeEvidence(t, "cycles-echo.json", map[string]any{
		"cycles":           cycleCount,
		"window_before":    fmt.Sprintf("%dx%d", w0, h0),
		"window_after":     fmt.Sprintf("%dx%d", w0, h0),
		"window_stable":    true,
		"list_clients_base": baseClients,
		"echo_n":           echoSamples,
		"echo_p95_ms":      math.Round(p95*10) / 10,
		"echo_p95_limit_ms": float64(echoP95Limit.Microseconds()) / 1000,
		"attach_procs_end":  attachProcs(t, m.socket),
	})
}

// CONTROL W8.4: превышение web.terminals_per_node_max → 503 TERMINAL_LIMIT.
func TestW8TerminalLimit(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("нет tmux")
	}
	m := newMachine(t)
	coord := m.startCoordinator(t)
	waitForAPI(t, m, coord)
	setupPane(t, m, coord)
	cookie := login(t, m)

	// termMaxPerNode открытых терминалов узла — допустимо.
	conns := make([]*websocket.Conn, 0, termMaxPerNode)
	for i := 0; i < termMaxPerNode; i++ {
		c, _ := openTerminal(t, m, cookie)
		conns = append(conns, c)
	}
	defer func() { // страховка: закрыть, если тест упадёт до явного close
		for _, c := range conns {
			_ = c.Close()
		}
	}()
	// Превышение → отказ ДО апгрейда (обычный HTTP 503 + код).
	req, _ := http.NewRequest("GET", m.webURL+"/web/ws/term/"+termSID, nil)
	req.Header.Set("Cookie", "runpilot_session="+cookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("запрос превышения: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("превышение лимита: статус = %d, хочу 503", resp.StatusCode)
	}
	var body struct {
		Code string `json:"code"`
	}
	b, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(b, &body)
	if body.Code != "TERMINAL_LIMIT" {
		t.Fatalf("превышение лимита: code = %q (тело %s), хочу TERMINAL_LIMIT", body.Code, b)
	}
	// Закрыть открытые → tmux attach завершится (list-clients/attach → 0).
	for _, c := range conns {
		_ = c.Close()
	}
	waitForZeroAttach(t, m.socket, 5*time.Second)

	writeEvidence(t, "limit.json", map[string]any{
		"terminals_per_node_max": termMaxPerNode,
		"opened_ok":              termMaxPerNode,
		"over_status":            resp.StatusCode,
		"over_code":              body.Code,
	})
}

// sortFloats — сортировка []float64 (без импорта sort ради одного вызова).
func sortFloats(s []float64) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
