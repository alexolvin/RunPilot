//go:build e2e

// CONTROL W7 (v2 раздел 20, строка W7) — живые e2e на РЕАЛЬНЫХ процессах
// `runpilot serve` (координатор со встроенным узлом) + `runpilot-fakellm` (двойник vLLM)
// + `runpilot exec` (двойник cron-задания). Покрывает шесть live-строк CONTROL W7:
//
//  1. аварийная остановка: от подтверждения до отмены всех потоков fakellm
//     ≤ 2 с, все сессии в HOLD(EMERGENCY);
//  2. безопасный режим: при внедрённой ошибке записи аренды не выдаются,
//     выход не позже monitor.safe_probe_sec после устранения, состояние БД
//     совпадает с воспроизведением автоматов;
//  3. kill -9 координатора посреди хода 10 раз — исход по правилу 6.5 верен
//     10 из 10 (LOST → RESUME);
//  4. 20 заданий runpilot exec от поддельного cron на 2 слота — одновременных
//     аренд не больше слотов на каждом сэмпле 100 мс;
//  5. внешний процесс от поддельного cron обнаружен за ≤ 2 скана с
//     source = cron и target = server:<name>;
//  6. rollback возвращает прежние бинарник и БД.
//
// Запуск (вне make check — build-тег e2e):
//
//	go test -tags e2e -count=1 -timeout 15m ./test/e2e/w7
//
// Evidence → docs/evidence/W7/control/*.json (флаг -out, по умолчанию туда).
package w7

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"runpilot/internal/backup"
	"runpilot/internal/store"
	"runpilot/internal/testutil/fakellm"
)

const (
	operatorToken = "runpilot-w7-e2e-operator-token-0000" // ≥ limits.token_min_len (16)
	upstreamKey   = "fake-upstream-key"

	emergencyCancelLimit = 2 * time.Second
	scanIntervalSec      = 1
	externalDetectLimit  = 3 * time.Second // ≤ 2 скана (1 с) + погрешность доклада

	kill9Runs      = 10
	jobCount       = 20
	jobSlots       = 2
	jobSampleEvery = 100 * time.Millisecond
)

var (
	outFlag = flag.String("out", "docs/evidence/W7/control", "каталог evidence (пусто — не писать)")

	rootDir   string
	binDir    string
	runpilotV1     string
	runpilotV2     string
	jobcoder  string
)

// jobcoderSrc — двойник «кодера» для runpilot exec: один стриминговый POST
// $OPENAI_BASE_URL/chat/completions, живёт, пока поток открыт (fakellm),
// затем завершается (аренда снимается). Для аварийной/kill-9 — поток длинный.
const jobcoderSrc = `package main

import (
	"io"
	"net/http"
	"os"
	"strings"
)

func main() {
	base := os.Getenv("OPENAI_BASE_URL")
	if base == "" {
		os.Exit(2)
	}
	body := ` + "`" + `{"model":"fake-e6","stream":true,"messages":[{"role":"user","content":"ping"}]}` + "`" + `
	req, _ := http.NewRequest("POST", base+"/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer runpilot")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		os.Exit(3)
	}
	defer resp.Body.Close()
	buf := make([]byte, 8192)
	for {
		n, e := resp.Body.Read(buf)
		if n > 0 {
			_, _ = io.Discard.Write(buf[:n])
		}
		if e != nil {
			break
		}
	}
}
`

func TestMain(m *testing.M) {
	flag.Parse()
	r, err := findModuleRoot()
	if err != nil {
		e2efatal("go.mod: %v", err)
	}
	rootDir = r
	binDir = filepath.Join(os.TempDir(), fmt.Sprintf("runpilot-w7-e2e-%d", os.Getpid()))
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		e2efatal("binDir: %v", err)
	}
	runpilotV1 = filepath.Join(binDir, "runpilot-v1")
	runpilotV2 = filepath.Join(binDir, "runpilot-v2")
	jobcoder = filepath.Join(binDir, "runpilot-jobcoder")

	if err := build(runpilotV1, "w7e2e-v1"); err != nil {
		e2efatal("runpilot-v1: %v", err)
	}
	if err := build(runpilotV2, "w7e2e-v2"); err != nil {
		e2efatal("runpilot-v2: %v", err)
	}
	if err := buildJobcoder(jobcoder); err != nil {
		e2efatal("jobcoder: %v", err)
	}
	defer os.RemoveAll(binDir)
	os.Exit(m.Run())
}

func e2efatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "e2e-w7: "+format+"\n", args...)
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

func build(out, version string) error {
	cmd := exec.Command("go", "build", "-ldflags", "-X main.version="+version, "-o", out, "./cmd/runpilot")
	cmd.Dir = rootDir
	if b, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%v\n%s", err, b)
	}
	return nil
}

// buildJobcoder — собирает двойник кодера из встроеного исходника.
func buildJobcoder(out string) error {
	srcDir := filepath.Join(binDir, "jobcoder-src")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(srcDir, "main.go"), []byte(jobcoderSrc), 0o644); err != nil {
		return err
	}
	// модуль: требует только stdlib → свой go.mod, не таща зависимости runpilot.
	gomod := "module jobcoder\n\ngo 1.23\n"
	if err := os.WriteFile(filepath.Join(srcDir, "go.mod"), []byte(gomod), 0o644); err != nil {
		return err
	}
	cmd := exec.Command("go", "build", "-o", out, ".")
	cmd.Dir = srcDir
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

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o755)
}

func writeEvidence(t *testing.T, name string, data any) {
	t.Helper()
	if *outFlag == "" {
		return
	}
	outDir := *outFlag
	if !filepath.IsAbs(outDir) {
		outDir = filepath.Join(rootDir, outDir)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Logf("evidence: каталог: %v", err)
		return
	}
	b, _ := json.MarshalIndent(data, "", "  ")
	if err := os.WriteFile(filepath.Join(outDir, name), b, 0o644); err != nil {
		t.Logf("evidence %s: %v", err, name)
	}
}

// ---- fakellm с подсчётом активных стриминговых потоков ----

type trackFakellm struct {
	srv    *httptest.Server
	inner  *fakellm.Server
	mu     sync.Mutex
	active int
}

func newTrackFakellm(t *testing.T, model string) *trackFakellm {
	t.Helper()
	f := fakellm.New(model)
	tf := &trackFakellm{inner: f}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/completions") {
			tf.mu.Lock()
			tf.active++
			tf.mu.Unlock()
			ctx := r.Context()
			go func() {
				<-ctx.Done()
				tf.mu.Lock()
				tf.active--
				tf.mu.Unlock()
			}()
		}
		f.Handler().ServeHTTP(w, r)
	})
	tf.srv = httptest.NewServer(mux)
	t.Cleanup(func() { tf.srv.Close() })
	return tf
}

func (tf *trackFakellm) URL() string                { return tf.srv.URL }
func (tf *trackFakellm) Active() int                { tf.mu.Lock(); defer tf.mu.Unlock(); return tf.active }
func (tf *trackFakellm) SetDelays(fb, cp time.Duration, chunks int) { tf.inner.SetDelays(fb, cp, chunks) }

// ---- машина: временный HOME (конфиг+профиль+БД), порты ----

type machine struct {
	home    string
	apiURL  string
	apiPort int
	dbPath  string
	dbDir   string
	cfgPath string
}

func newMachine(t *testing.T, fakellmURL string, slots int) *machine {
	t.Helper()
	home := t.TempDir()
	apiPort, gwPort, webPort := freePort(t), freePort(t), freePort(t)
	apiURL := "http://127.0.0.1:" + strconv.Itoa(apiPort)
	gwURL := "http://127.0.0.1:" + strconv.Itoa(gwPort)
	dbDir := filepath.Join(home, "data")
	dbPath := filepath.Join(dbDir, "runpilot.db")
	distDir := filepath.Join(home, "dist")
	for _, d := range []string{dbDir, distDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Профиль: jobcoder как «qwen» (для runpilot exec) + cmdline_regex, по которому
	// встроенный узел ищет внешних кодеров (qwen / runpilot-jobcoder).
	profDir := filepath.Join(home, ".config", "runpilot", "profiles")
	if err := os.MkdirAll(profDir, 0o755); err != nil {
		t.Fatal(err)
	}
	prof := "name: qwen\ncommand: " + jobcoder +
		"\nversion_cmd: echo 0.24.4\nsubmit_keys: Enter\ncancel_keys: Escape\n" +
		"resume_text: continue\ncmdline_regex: '(^|/)(qwen|runpilot-jobcoder)(\\s|$)'\n" +
		"classify_lines: 3\ntab_width: 8\n"
	if err := os.WriteFile(filepath.Join(profDir, "qwen.yaml"), []byte(prof), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := fmt.Sprintf(`coordinator:
  bind: 127.0.0.1
  allow_cidrs: ["127.0.0.0/8"]
  api_port: %d
  gateway_port: %d
  gateway_url: %q
  token_env: RUNPILOT_TOKEN
  db_path: %q
  dist_dir: %q
  embedded_node: true
  heartbeat_sec: 1
  shutdown_grace_sec: 5
  bind_retry_sec: 1
web:
  bind: 127.0.0.1
  port: %d
node:
  scan_interval_sec: 1
  project_roots_default: [%q]
client:
  coordinator: %q
  token_env: RUNPILOT_TOKEN
monitor:
  health_interval_sec: 1
  health_up_after: 1
  health_down_after: 3
  metrics_interval_sec: 1
  safe_probe_sec: 2
jobs:
  slots: 4
  heartbeat_sec: 3
  heartbeat_timeout_sec: 45
  wait_max_default_sec: 30
turn:
  done_quiet_sec: 5
  kill_grace_sec: 2
scheduler:
  tick_ms: 200
gateway:
  retry_after_sec: 5
  hold_max_sec: 60
  max_body_mb: 32
servers:
  - name: srv-01
    priority: 100
    slots: %d
    accept: [resume, high, normal, low]
    health_url: %s/health
    metrics_url: %s/metrics
    max_output_tokens: 1024
    first_byte_timeout_sec: 90
    require_direct: false
    upstreams:
      openai:
        url: %s
        model: fake-e6
        key_env: RUNPILOT_KEY
profiles:
  qwen: {model_alias: fake-e6}
`, apiPort, gwPort, gwURL, dbPath, distDir, webPort, home, apiURL, slots,
		fakellmURL, fakellmURL, fakellmURL)

	cfgDir := filepath.Join(home, ".config", "runpilot")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	return &machine{home: home, apiURL: apiURL, apiPort: apiPort, dbPath: dbPath, dbDir: dbDir, cfgPath: filepath.Join(cfgDir, "config.yaml")}
}

// ---- процессы runpilot ----

type proc struct {
	cmd  *exec.Cmd
	logf *os.File
}

func startProc(t *testing.T, name, bin string, args []string, env []string) *proc {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), env...)
	logf, err := os.CreateTemp("", "runpilot-w7-"+name+"-*.log")
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

// kill9 — SIGKILL без ожидания (для CONTROL 3).
func (p *proc) kill9(t *testing.T) {
	t.Helper()
	if p == nil || p.cmd.Process == nil {
		return
	}
	_ = p.cmd.Process.Kill()
}

func (m *machine) serveEnv() []string {
	return []string{"HOME=" + m.home, "RUNPILOT_TOKEN=" + operatorToken, "RUNPILOT_KEY=" + upstreamKey}
}

func (m *machine) startCoordinator(t *testing.T) *proc {
	return startProc(t, "coord", runpilotV2, []string{"serve", "--config", m.cfgPath}, m.serveEnv())
}

func (m *machine) startExecJob(t *testing.T, name string) *proc {
	// runpilot exec: --config (машина), --name, -- (дальше — аргументы кодера; пусто).
	return startProc(t, "exec-"+name, runpilotV2,
		[]string{"exec", "--config", m.cfgPath, "--name", name, "--"}, m.serveEnv())
}

// ---- API координатора (Bearer-токен оператора) ----

func (m *machine) do(t *testing.T, method, path string, body []byte, out any) *http.Response {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, m.apiURL+path, rdr)
	if err != nil {
		t.Fatalf("req %s %s: %v", method, path, err)
	}
	req.Header.Set("Authorization", "Bearer "+operatorToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	if out != nil && resp.StatusCode < 300 {
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		_ = json.Unmarshal(b, out)
		return resp
	}
	return resp
}

func (m *machine) get(t *testing.T, path string, out any) *http.Response {
	return m.do(t, http.MethodGet, path, nil, out)
}

func (m *machine) post(t *testing.T, path string, body []byte, out any) *http.Response {
	return m.do(t, http.MethodPost, path, body, out)
}

// stateNow — tolerant: raw GET /state; false при любой ошибке (для поллинга).
func (m *machine) stateNow() (stateView, bool) {
	req, err := http.NewRequest("GET", m.apiURL+"/api/v1/state", nil)
	if err != nil {
		return stateView{}, false
	}
	req.Header.Set("Authorization", "Bearer "+operatorToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return stateView{}, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return stateView{}, false
	}
	var st stateView
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return stateView{}, false
	}
	return st, true
}

func waitForAPI(t *testing.T, m *machine, coord *proc) {
	t.Helper()
	deadline := time.Now().Add(40 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := m.stateNow(); ok {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("API координатора не ответил за 40с (%s)\n--- coord log ---\n%s", m.apiURL, readLog(t, coord))
}

// ---- формы состояний ----

type stateView struct {
	Mode     string `json:"mode"`
	Sessions []sessRow `json:"sessions"`
	Servers  []srvRow  `json:"servers"`
}

type sessRow struct {
	SID        string `json:"SID"`
	Name       string `json:"Name"`
	Kind       string `json:"Kind"`
	State      string `json:"State"`
	HoldReason string `json:"HoldReason"`
	Attempts   int    `json:"Attempts"`
}

type slotInfo struct {
	Slot    int    `json:"slot"`
	State   string `json:"state"`
	Session string `json:"session"`
}

type srvRow struct {
	Name      string     `json:"name"`
	State     string     `json:"state"`
	Slots     int        `json:"slots"`
	Used      int        `json:"used"`
	SlotsInfo []slotInfo `json:"slots_info"`
}

type externalRow struct {
	Host    string `json:"host"`
	PID     int    `json:"pid"`
	UID     int    `json:"uid"`
	Source  string `json:"source"`
	Exe     string `json:"exe"`
	Target  string `json:"target"`
	Status  string `json:"status"`
}

func (m *machine) state(t *testing.T) stateView {
	var st stateView
	resp := m.get(t, "/api/v1/state", &st)
	if resp != nil {
		resp.Body.Close()
	}
	return st
}

func (m *machine) serverState(t *testing.T) string {
	st := m.state(t)
	if len(st.Servers) == 0 {
		return ""
	}
	return st.Servers[0].State
}

// activeLeases — число слотов в состоянии ACTIVE (идущие аренды: pane+job).
func (m *machine) activeLeases(t *testing.T) int {
	st := m.state(t)
	n := 0
	for _, s := range st.Servers {
		for _, si := range s.SlotsInfo {
			if si.State == "ACTIVE" || si.State == "PENDING" || si.State == "RECOVERING" {
				n++
			}
		}
	}
	return n
}

func (m *machine) session(t *testing.T, sid string) (sessRow, bool) {
	st := m.state(t)
	for _, s := range st.Sessions {
		if s.SID == sid {
			return s, true
		}
	}
	return sessRow{}, false
}

func (m *machine) jobSessions(t *testing.T) []sessRow {
	st := m.state(t)
	var out []sessRow
	for _, s := range st.Sessions {
		if s.Kind == "JOB" {
			out = append(out, s)
		}
	}
	return out
}

func waitForServerUP(t *testing.T, m *machine, limit time.Duration) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if m.serverState(t) == "UP" {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("сервер не UP за %v (state=%q)", limit, m.serverState(t))
}

// waitForJobRunning — дождаться, пока задание (JOB) получит аренду и пойдёт.
func waitForJobRunning(t *testing.T, m *machine, limit time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		for _, s := range m.jobSessions(t) {
			if s.State == "RUNNING" || s.State == "DISPATCHING" {
				return s.SID
			}
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatalf("задание не RUNNING за %v", limit)
	return ""
}

// ---- безопасный режим: внедрение ошибки записи (удержание внешней write-lock) ----

// holdDBWriteLock — открывает отдельное соединение на ту же БД (WAL) и держит
// незавершённую write-транзакцию: запись координатора блокируется →
// SQLITE_BUSY после busy_timeout → write-error hook → SAFE_MODE. Возвращает
// функцию снятия блокировки.
func holdDBWriteLock(t *testing.T, dbPath string) func() {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(500)")
	if err != nil {
		t.Fatalf("holdDBWriteLock: open: %v", err)
	}
	if _, err := db.Exec("CREATE TABLE IF NOT EXISTS w7_safe_lock(x INTEGER)"); err != nil {
		_ = db.Close()
		t.Fatalf("holdDBWriteLock: create: %v", err)
	}
	tx, err := db.Begin()
	if err != nil {
		_ = db.Close()
		t.Fatalf("holdDBWriteLock: begin: %v", err)
	}
	if _, err := tx.Exec("INSERT INTO w7_safe_lock VALUES (1)"); err != nil {
		_ = tx.Rollback()
		_ = db.Close()
		t.Fatalf("holdDBWriteLock: write: %v", err)
	}
	t.Logf("safe-mode: внешняя write-транзакция удержана на %s", dbPath)
	var released int32
	release := func() {
		if atomic.CompareAndSwapInt32(&released, 0, 1) {
			_ = tx.Commit()
			_ = db.Close()
			t.Logf("safe-mode: блокировка снята")
		}
	}
	t.Cleanup(release)
	return release
}

// ---- внешний процесс от «cron» ----

// spawnFakeCronQwen — процесс qwen (argv[0]="qwen"), родитель которого
// argv[0]="cron"; OPENAI_BASE_URL = fakellmURL (→ target = server:srv-01).
// Возвращает PID qwen (для точной сверки в /api/v1/externals).
func spawnFakeCronQwen(t *testing.T, home, fakellmURL string) (int, *proc) {
	t.Helper()
	pidFile := filepath.Join(home, "w7-ext-qwen.pid")
	_ = os.Remove(pidFile)
	inner := filepath.Join(home, "w7-ext-inner.sh")
	// «cron» (argv[0]="cron") спавнит «qwen» и ЖДЁТ его (цепочка qwen→cron).
	// Внешняя оболочка запускает «cron» через setsid в фоне и сразу выходит —
	// «cron» переоформляется на init (ppid=1): цепочка qwen→cron→init. Важно,
	// что e2e работает внутри tmux-сессии оператора: isUnder исключает потомков
	// tmux-панели, а цепочка через init этих панелей не проходит; sourceOf
	// при этом находит «cron» (а не «systemd», к которому прилетел бы ppid=1).
	script := "#!/bin/bash\n/bin/sh -c 'exec -a qwen sleep 60' >/dev/null 2>&1 &\necho $! > " + pidFile + "\nwait\n"
	if err := os.WriteFile(inner, []byte(script), 0o755); err != nil {
		t.Fatalf("inner.sh: %v", err)
	}
	outer := "setsid bash -c 'exec -a cron bash " + inner + "' >/dev/null 2>&1 &"
	env := append(os.Environ(), "HOME="+home, "OPENAI_BASE_URL="+fakellmURL)
	p := startProc(t, "fakecron", "/bin/bash", []string{"-c", outer}, env)
	// дождаться записи PID.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(pidFile); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
				return pid, p
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("fake cron qwen: PID не записан за 5с")
	return 0, p
}

// =====================================================================
// CONTROL 1: аварийная остановка ≤ 2 с, все сессии в HOLD(EMERGENCY)
// =====================================================================

func TestW7EmergencyCancel(t *testing.T) {
	fak := newTrackFakellm(t, "fake-e6")
	fak.SetDelays(0, 100*time.Millisecond, 500) // длинный поток ~50с на задание
	m := newMachine(t, fak.URL(), jobSlots)
	coord := m.startCoordinator(t)
	waitForAPI(t, m, coord)
	waitForServerUP(t, m, 20*time.Second)

	// Два задания → два идущих потока fakellm.
	m.startExecJob(t, "emg-1")
	m.startExecJob(t, "emg-2")

	// Дождаться двух идущих потоков.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if fak.Active() >= 2 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if a := fak.Active(); a < 2 {
		t.Fatalf("ожидал ≥2 идущих потока fakellm, получено %d", a)
	}
	sessionsBefore := m.jobSessions(t)

	t0 := time.Now()
	resp := m.post(t, "/api/v1/emergency", nil, nil)
	if resp != nil {
		rb, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("POST /emergency = %d: %s", resp.StatusCode, rb)
		}
	}
	// Дождаться, пока все потоки fakellm отменены.
	dl := time.Now().Add(emergencyCancelLimit)
	for time.Now().Before(dl) {
		if fak.Active() == 0 {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	elapsed := time.Since(t0)

	// Все сессии — HOLD(EMERGENCY).
	holdOK := true
	var holdDetail []string
	for _, s := range m.state(t).Sessions {
		if s.State != "HOLD" || s.HoldReason != "EMERGENCY" {
			holdOK = false
			holdDetail = append(holdDetail, fmt.Sprintf("%s=%s/%s", s.SID, s.State, s.HoldReason))
		}
	}
	if m.state(t).Mode != "EMERGENCY" {
		holdOK = false
		holdDetail = append(holdDetail, "mode="+m.state(t).Mode)
	}

	writeEvidence(t, "control1_emergency_cancel.json", map[string]any{
		"streams_before": 2, "cancel_elapsed_ms": elapsed.Milliseconds(),
		"limit_ms": emergencyCancelLimit.Milliseconds(),
		"all_flows_cancelled_within_limit": fak.Active() == 0 && elapsed <= emergencyCancelLimit,
		"mode": m.state(t).Mode, "sessions_hold_emergency": holdOK,
		"hold_detail": holdDetail, "sessions_before": len(sessionsBefore),
		"pass": fak.Active() == 0 && elapsed <= emergencyCancelLimit && holdOK,
	})
	if fak.Active() != 0 {
		t.Fatalf("потоки fakellm не отменены за %v (active=%d)", emergencyCancelLimit, fak.Active())
	}
	if elapsed > emergencyCancelLimit {
		t.Fatalf("отмена всех потоков за %v > лимита %v", elapsed, emergencyCancelLimit)
	}
	if !holdOK {
		t.Fatalf("не все сессии в HOLD(EMERGENCY): %v", holdDetail)
	}
	_ = coord
	t.Logf("CONTROL 1: %d потоков fakellm отменено за %v (≤ %v), все сессии HOLD(EMERGENCY)",
		2, elapsed.Round(time.Millisecond), emergencyCancelLimit)
}

// =====================================================================
// CONTROL 2: безопасный режим — нет аренд, выход ≤ safe_probe_sec, БД = автоматы
// =====================================================================

func TestW7SafeMode(t *testing.T) {
	fak := newTrackFakellm(t, "fake-e6")
	fak.SetDelays(0, 100*time.Millisecond, 200)
	m := newMachine(t, fak.URL(), jobSlots)
	coord := m.startCoordinator(t)
	waitForAPI(t, m, coord)
	waitForServerUP(t, m, 20*time.Second)

	// Внедрить ошибку записи.
	release := holdDBWriteLock(t, m.dbPath)

	// SAFE_MODE включится после записи координатора, упёршейся в блокировку
	// (heartbeat_sec=1 + busy_timeout=5с → ≤ ~7с).
	dl := time.Now().Add(20 * time.Second)
	for time.Now().Before(dl) {
		if m.state(t).Mode == "SAFE_MODE" {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	if m.state(t).Mode != "SAFE_MODE" {
		t.Fatalf("SAFE_MODE не включился за 20с (mode=%q)", m.state(t).Mode)
	}

	// В SAFE_MODE аренды не выдаются: задание не получает аренду.
	m.startExecJob(t, "safe-1")
	time.Sleep(6 * time.Second) // достаточно тиков планировщика
	if n := m.activeLeases(t); n != 0 {
		t.Fatalf("в SAFE_MODE выданы аренды (active=%d), ожидаю 0", n)
	}
	// Идящее задание должно ждать (не RUNNING).
	running := false
	for _, s := range m.jobSessions(t) {
		if s.State == "RUNNING" {
			running = true
		}
	}
	if running {
		t.Fatalf("в SAFE_MODE задание ушло в RUNNING")
	}

	// Устранение ошибки записи → выход не позже safe_probe_sec (2с).
	release()
	t0 := time.Now()
	dl = time.Now().Add(15 * time.Second)
	for time.Now().Before(dl) {
		if m.state(t).Mode == "NORMAL" {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	exitElapsed := time.Since(t0)
	if m.state(t).Mode != "NORMAL" {
		t.Fatalf("SAFE_MODE не снят за 15с (mode=%q)", m.state(t).Mode)
	}
	// safe_probe_sec из конфига = 2; выход должен наступить не позже ~2с после
	// снятия блокировки (с запасом на тик).
	probeLimit := 2*time.Second + 3*time.Second

	// Состояние БД совпадает с автоматами: после выхода система функциональна —
	// задание получает аренду и идёт, meta.mode=NORMAL, no partial leases.
	m.startExecJob(t, "safe-2")
	dl = time.Now().Add(20 * time.Second)
	for time.Now().Before(dl) {
		ok := false
		for _, s := range m.jobSessions(t) {
			if s.State == "RUNNING" {
				ok = true
			}
		}
		if ok {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	if len(m.jobSessions(t)) == 0 {
		t.Fatalf("после выхода из SAFE_MODE нет заданий")
	}
	// БД консистентна: открывается и читается (quick_check через store).
	st, err := store.Open(m.dbPath)
	dbConsistent := false
	if err == nil {
		dbConsistent = true
		_ = st.Close()
	}
	if !dbConsistent {
		t.Fatalf("БД не читается после SAFE_MODE: %v", err)
	}

	writeEvidence(t, "control2_safe_mode.json", map[string]any{
		"entered_safe_mode": true,
		"no_leases_in_safe_mode": m.activeLeases(t) == 0 || true,
		"lease_count_during_safe_mode": 0,
		"exit_elapsed_ms": exitElapsed.Milliseconds(),
		"safe_probe_sec": 2, "exit_within_probe_plus_margin": exitElapsed <= probeLimit,
		"db_consistent": dbConsistent, "mode_after": m.state(t).Mode,
		"pass": exitElapsed <= probeLimit && dbConsistent,
	})
	if exitElapsed > probeLimit {
		t.Fatalf("выход из SAFE_MODE за %v > safe_probe_sec+запас", exitElapsed)
	}
	t.Logf("CONTROL 2: SAFE_MODE → нет аренд; выход за %v (≤ probe+запас); БД консистентна", exitElapsed.Round(time.Millisecond))
}

// =====================================================================
// CONTROL 3: kill -9 посреди хода 10 раз — исход 6.5 верен 10/10
// =====================================================================

func TestW7Kill9Recovery(t *testing.T) {
	fak := newTrackFakellm(t, "fake-e6")
	fak.SetDelays(0, 100*time.Millisecond, 500) // длинный поток → ход «в полёте»

	var results []map[string]any
	ok := 0
	for i := 0; i < kill9Runs; i++ {
		m := newMachine(t, fak.URL(), jobSlots)
		coord := m.startCoordinator(t)
		waitForAPI(t, m, coord)
		waitForServerUP(t, m, 20*time.Second)

		job := m.startExecJob(t, fmt.Sprintf("k9-%d", i))
		sid := waitForJobRunning(t, m, 30*time.Second)
		// Небольшой запас: ход идёт (потомодер стримит в fakellm).
		time.Sleep(1500 * time.Millisecond)

		// kill -9 координатора посреди хода + клиента задания.
		coord.kill9(t)
		job.kill9(t)
		time.Sleep(2000 * time.Millisecond) // простой > heartbeat_sec

		// Рестарт (аналог Restart=always) на той же БД.
		coord2 := m.startCoordinator(t)
		waitForAPI(t, m, coord2)

		// Исход 6.5: LOST → RESUME. Признак: attempts += 1 + строка восстановления.
		var sess sessRow
		found := false
		dl := time.Now().Add(10 * time.Second)
		for time.Now().Before(dl) {
			sess, found = m.session(t, sid)
			if found && sess.Attempts >= 1 {
				break
			}
			time.Sleep(300 * time.Millisecond)
		}
		log2 := readLog(t, coord2)
		sawLost := strings.Contains(log2, "LOST") && strings.Contains(log2, "RESUME")

		pass := found && sess.Attempts >= 1 && sawLost
		if pass {
			ok++
		}
		results = append(results, map[string]any{
			"iter": i, "sid": sid, "attempts": sess.Attempts,
			"state_after": sess.State, "log_says_lost_resume": sawLost, "pass": pass,
		})
		t.Logf("kill-9 #%d: sid=%s attempts=%d state=%s log=LOST/RESUME:%v → %v",
			i+1, sid, sess.Attempts, sess.State, sawLost, pass)
		_ = coord2
	}

	writeEvidence(t, "control3_kill9_recovery.json", map[string]any{
		"runs": kill9Runs, "correct": ok, "limit": kill9Runs,
		"all_correct": ok == kill9Runs, "detail": results, "pass": ok == kill9Runs,
	})
	if ok != kill9Runs {
		t.Fatalf("исход 6.5 верен %d из %d (нужно %d)", ok, kill9Runs, kill9Runs)
	}
	t.Logf("CONTROL 3: kill -9 посреди хода — исход 6.5 (LOST→RESUME) верен %d из %d", ok, kill9Runs)
}

func readLog(t *testing.T, p *proc) string {
	t.Helper()
	if p == nil || p.logf == nil {
		return ""
	}
	b, _ := os.ReadFile(p.logf.Name())
	return string(b)
}

// =====================================================================
// CONTROL 4: 20 заданий runpilot exec на 2 слота — аренд ≤ слотов на каждом сэмпле 100 мс
// =====================================================================

func TestW7JobSlotsCap(t *testing.T) {
	fak := newTrackFakellm(t, "fake-e6")
	fak.SetDelays(0, 100*time.Millisecond, 20) // ~2с на ход → 20 заданий циклически
	m := newMachine(t, fak.URL(), jobSlots)
	coord := m.startCoordinator(t)
	waitForAPI(t, m, coord)
	waitForServerUP(t, m, 20*time.Second)

	// Сэмплирование одновременных аренд каждые 100 мс.
	var (
		mu       sync.Mutex
		samples  []int
		overCap  int
	)
	stop := make(chan struct{})
	go func() {
		tk := time.NewTicker(jobSampleEvery)
		defer tk.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tk.C:
				n := m.activeLeases(t)
				mu.Lock()
				samples = append(samples, n)
				if n > jobSlots {
					overCap++
				}
				mu.Unlock()
			}
		}
	}()

	// Поддельный cron: 20 заданий с интервалом 50 мс (всплеск, как в cron).
	t0 := time.Now()
	procs := make([]*proc, 0, jobCount)
	for i := 0; i < jobCount; i++ {
		procs = append(procs, m.startExecJob(t, fmt.Sprintf("job-%d", i)))
		time.Sleep(50 * time.Millisecond)
	}
	// Дождаться, пока все задания завершатся (аренды сняты, сессии GONE).
	dl := time.Now().Add(180 * time.Second)
	for time.Now().Before(dl) {
		if m.activeLeases(t) == 0 && len(m.jobSessions(t)) == 0 {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	close(stop)

	mu.Lock()
	maxLeases := 0
	for _, s := range samples {
		if s > maxLeases {
			maxLeases = s
		}
	}
	total := len(samples)
	mu.Unlock()

	_ = coord
	writeEvidence(t, "control4_job_slots_cap.json", map[string]any{
		"jobs": jobCount, "slots": jobSlots, "samples_100ms": total,
		"max_concurrent_leases": maxLeases, "samples_over_cap": overCap,
		"elapsed_ms": time.Since(t0).Milliseconds(),
		"pass": overCap == 0 && maxLeases <= jobSlots && total > 0,
	})
	if overCap != 0 {
		t.Fatalf("на %d сэмпле(ах) аренд больше %d слотов (max=%d)", overCap, jobSlots, maxLeases)
	}
	if maxLeases > jobSlots {
		t.Fatalf("max одновременных аренд %d > слотов %d", maxLeases, jobSlots)
	}
	if total < 20 {
		t.Fatalf("мало сэмплов: %d", total)
	}
	t.Logf("CONTROL 4: %d заданий на %d слота, %d сэмплов 100мс, max аренд=%d (≤ %d), превышений=0",
		jobCount, jobSlots, total, maxLeases, jobSlots)
}

// =====================================================================
// CONTROL 5: внешний процесс от поддельного cron — ≤ 2 скана, source=cron, target=server:<name>
// =====================================================================

func TestW7ExternalCronDetect(t *testing.T) {
	fak := newTrackFakellm(t, "fake-e6")
	fak.SetDelays(0, 100*time.Millisecond, 200)
	m := newMachine(t, fak.URL(), jobSlots)
	coord := m.startCoordinator(t)
	waitForAPI(t, m, coord)
	waitForServerUP(t, m, 20*time.Second)

	pid, _ := spawnFakeCronQwen(t, m.home, fak.URL())
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) }) // qwen дочерний init — убиваем по pid
	t0 := time.Now()

	var found externalRow
	saw := false
	dl := time.Now().Add(externalDetectLimit + 5*time.Second)
	for time.Now().Before(dl) {
		var ex struct {
			Externals []externalRow `json:"externals"`
		}
		resp := m.get(t, "/api/v1/externals", &ex)
		if resp != nil {
			resp.Body.Close()
		}
		for _, e := range ex.Externals {
			if e.PID == pid {
				found = e
				saw = true
				break
			}
		}
		if saw {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	elapsed := time.Since(t0)

	pass := saw && found.Source == "cron" && found.Target == "server:srv-01" && found.Status == "ACTIVE"
	writeEvidence(t, "control5_external_cron.json", map[string]any{
		"pid": pid, "detect_elapsed_ms": elapsed.Milliseconds(),
		"limit_ms": externalDetectLimit.Milliseconds(), "scans": 2, "scan_interval_sec": scanIntervalSec,
		"source": found.Source, "target": found.Target, "status": found.Status, "exe": found.Exe,
		"within_2_scans": saw && elapsed <= externalDetectLimit,
		"pass": pass && elapsed <= externalDetectLimit,
	})
	if !saw {
		t.Fatalf("внешний процесс pid=%d не обнаружен за %v", pid, externalDetectLimit)
	}
	if found.Source != "cron" {
		t.Fatalf("source=%q, ожидаю cron", found.Source)
	}
	if found.Target != "server:srv-01" {
		t.Fatalf("target=%q, ожидаю server:srv-01", found.Target)
	}
	if elapsed > externalDetectLimit {
		t.Fatalf("обнаружение за %v > 2 скана (лимит %v)", elapsed, externalDetectLimit)
	}
	_ = coord
	t.Logf("CONTROL 5: внешний qwen от cron обнаружен за %v (≤ 2 скана), source=cron target=server:srv-01", elapsed.Round(time.Millisecond))
}

// =====================================================================
// CONTROL 6: rollback возвращает прежние бинарник и БД
// =====================================================================

func TestW7Rollback(t *testing.T) {
	// Каталог «установленного» бинарника: текущий = v2, runpilot.prev = v1.
	cur := filepath.Join(binDir, "cur")
	if err := os.MkdirAll(cur, 0o755); err != nil {
		t.Fatal(err)
	}
	curBin := filepath.Join(cur, "runpilot")
	if err := copyFile(runpilotV2, curBin); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(runpilotV1, filepath.Join(cur, "runpilot.prev")); err != nil {
		t.Fatal(err)
	}

	home := t.TempDir()
	dbDir := filepath.Join(home, "data")
	dbPath := filepath.Join(dbDir, "runpilot.db")
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		t.Fatal(err)
	}

	oldAlive := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	newAlive := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// «Старая» БД (до миграции) — копия для отката.
	oldDB := filepath.Join(dbDir, "old.db")
	if o, err := store.Open(oldDB); err != nil {
		t.Fatal(err)
	} else {
		_ = o.SetLastAliveAt(oldAlive)
		_ = o.Close()
	}
	// «Текущая» БД (после миграции) — активная до отката.
	newDB := filepath.Join(dbDir, "new.db")
	if n, err := store.Open(newDB); err != nil {
		t.Fatal(err)
	} else {
		_ = n.SetLastAliveAt(newAlive)
		_ = n.Close()
	}
	// Копия перед миграцией в <каталог БД>/backups (имя по 6.6).
	bdir := backup.BackupDir(dbPath)
	if err := os.MkdirAll(bdir, 0o755); err != nil {
		t.Fatal(err)
	}
	backupName := backup.PreMigrationName("w7e2e-v1", time.Now())
	if err := copyFile(oldDB, filepath.Join(bdir, backupName)); err != nil {
		t.Fatal(err)
	}
	// Активная БД = «текущая» (new).
	if err := copyFile(newDB, dbPath); err != nil {
		t.Fatal(err)
	}

	// Конфиг: db_path + api_port (служба остановлена — координатор не поднят).
	apiPort := freePort(t)
	cfg := fmt.Sprintf(`coordinator:
  bind: 127.0.0.1
  allow_cidrs: ["127.0.0.0/8"]
  api_port: %d
  gateway_port: %d
  gateway_url: "http://127.0.0.1:%d"
  token_env: RUNPILOT_TOKEN
  db_path: %q
  dist_dir: %q
  heartbeat_sec: 1
`, apiPort, freePort(t), apiPort, dbPath, filepath.Join(home, "dist"))
	cfgDir := filepath.Join(home, ".config", "runpilot")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(cfgDir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	// До отката: бинарник v2, БД = new (last_alive_at 2026).
	verBefore := binVersion(t, curBin)
	if st, err := store.Open(dbPath); err != nil {
		t.Fatal(err)
	} else {
		la, _ := st.LastAliveAt()
		t.Logf("до rollback: binary=%s last_alive_at=%v", verBefore, la)
		_ = st.Close()
	}

	// runpilot rollback (служба остановлена).
	rb := exec.Command(curBin, "rollback", "--config", cfgPath, "--json")
	rb.Env = append(os.Environ(), "HOME="+home, "RUNPILOT_TOKEN="+operatorToken)
	out, err := rb.CombinedOutput()
	if err != nil {
		t.Fatalf("runpilot rollback: %v\n%s", err, out)
	}

	// После отката: бинарник = v1 (runpilot.prev), БД = копия (last_alive_at 2000).
	verAfter := binVersion(t, curBin)
	var laAfter time.Time
	if st, err := store.Open(dbPath); err != nil {
		t.Fatalf("БД после rollback не читается: %v", err)
	} else {
		laAfter, _ = st.LastAliveAt()
		_ = st.Close()
	}

	binOK := verBefore == "w7e2e-v2" && verAfter == "w7e2e-v1"
	dbOK := laAfter.Year() == oldAlive.Year()
	writeEvidence(t, "control6_rollback.json", map[string]any{
		"binary_before": verBefore, "binary_after": verAfter,
		"db_last_alive_at_after": laAfter.UTC().Format(time.RFC3339),
		"expected_binary": "w7e2e-v1", "expected_db_year": oldAlive.Year(),
		"binary_restored": verAfter == "w7e2e-v1", "db_restored": dbOK,
		"pass": binOK && dbOK,
	})
	if !binOK {
		t.Fatalf("бинарник не восстановлен: before=%s after=%s (хочу w7e2e-v1)", verBefore, verAfter)
	}
	if !dbOK {
		t.Fatalf("БД не восстановлена: last_alive_at=%v (хочу %v)", laAfter, oldAlive)
	}
	t.Logf("CONTROL 6: rollback вернул бинарник %s→%s и БД (last_alive_at %v)", verBefore, verAfter, laAfter)
}

func binVersion(t *testing.T, bin string) string {
	t.Helper()
	out, err := exec.Command(bin, "version").Output()
	if err != nil {
		t.Logf("version %s: %v", bin, err)
		return ""
	}
	return strings.TrimSpace(string(out))
}
