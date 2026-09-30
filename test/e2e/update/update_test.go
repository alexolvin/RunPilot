//go:build e2e

// CONTROL W6 (раздел 20 ТЗ v2, строка 1414) — e2e на РЕАЛЬНЫХ процессах
// `runpilot serve` (координатор) + `runpilot node` (узел), tmux и mockcoder как qwen:
//
//   CONTROL 1: чистая машина (tmux + mockcoder как qwen): одна команда
//              (запуск runpilot node) → узел онлайн ≤ 60 с;
//   CONTROL 2: новая версия координатора → узел обновлён (self-update:
//              download + SHA-256 + selftest + swap + перезапуск) ≤ 120 с,
//              список сессий до и после совпадает;
//   CONTROL 3: бинарник с проваленным selftest не устанавливается, узел
//              остаётся на старой версии.
//
// Запуск (вне make check — build-тег e2e, нужны tmux и go):
//
//	go test -tags e2e -count=1 ./test/e2e/update
//
// Evidence → docs/evidence/W6/control/*.json (флаг -out, по умолчанию туда).
package update

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	v1 = "e2e-v1"
	v2 = "e2e-v2"

	onlineLimit = 60 * time.Second  // CONTROL 1
	updateLimit = 120 * time.Second // CONTROL 2

	operatorToken = "runpilot-e2e-operator-token-0123456789" // ≥ token_min_len (16)
)

var (
	outFlag = flag.String("out", "docs/evidence/W6/control", "каталог evidence (пусто — не писать)")

	rootDir   string
	binDir    string
	runpilotV1     string
	runpilotV2     string
	mockcoder string
)

func TestMain(m *testing.M) {
	flag.Parse()
	r, err := findModuleRoot()
	if err != nil {
		e2efatal("go.mod: %v", err)
	}
	rootDir = r
	binDir = filepath.Join(os.TempDir(), fmt.Sprintf("runpilot-update-e2e-%d", os.Getpid()))
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		e2efatal("binDir: %v", err)
	}
	runpilotV1 = filepath.Join(binDir, "runpilot-v1")
	runpilotV2 = filepath.Join(binDir, "runpilot-v2")
	mockcoder = filepath.Join(binDir, "runpilot-mockcoder")

	if err := build(runpilotV1, v1); err != nil {
		e2efatal("runpilot-v1: %v", err)
	}
	if err := build(runpilotV2, v2); err != nil {
		e2efatal("runpilot-v2: %v", err)
	}
	if err := buildSimple(mockcoder, "./cmd/runpilot-mockcoder"); err != nil {
		e2efatal("mockcoder: %v", err)
	}
	defer os.RemoveAll(binDir)
	os.Exit(m.Run())
}

func e2efatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "e2e-update: "+format+"\n", args...)
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
	cmd := exec.Command("go", "build",
		"-ldflags", "-X main.version="+version, "-o", out, "./cmd/runpilot")
	cmd.Dir = rootDir
	if b, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%v\n%s", err, b)
	}
	return nil
}

func buildSimple(out, pkg string) error {
	cmd := exec.Command("go", "build", "-o", out, pkg)
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

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o755)
}

func writeEvidence(name string, data any) {
	if *outFlag == "" {
		return
	}
	outDir := *outFlag
	if !filepath.IsAbs(outDir) {
		outDir = filepath.Join(rootDir, outDir)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return
	}
	b, _ := json.MarshalIndent(data, "", "  ")
	_ = os.WriteFile(filepath.Join(outDir, name), b, 0o644)
}

// machine — «чистая машина»: временный HOME (конфиг+профиль+БД), свободные
// порты, tmux-сокет, dist-каталог, рабочая директория (project_roots) и
// nodeBin — копия бинарника узла (self-update меняет именно его, os.Executable).
type machine struct {
	home    string
	apiURL  string
	apiPort int
	gwPort  int
	webPort int
	dbPath  string
	distDir string
	workdir string
	socket  string
	nodeBin string
}

func newMachine(t *testing.T, distFile string) *machine {
	t.Helper()
	home := t.TempDir()
	apiPort, gwPort, webPort := freePort(t), freePort(t), freePort(t)
	// workdir под ~ узла (HOME=home): после миграции project_roots_default
	// уходит в БД, узел использует дефолт ["~"] — каталог обязан быть под home.
	workdir := filepath.Join(home, "work")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatal(err)
	}
	m := &machine{
		home:    home,
		apiURL:  "http://127.0.0.1:" + fmt.Sprint(apiPort),
		apiPort: apiPort, gwPort: gwPort, webPort: webPort,
		dbPath:  filepath.Join(home, "runpilot.db"),
		distDir: filepath.Join(home, "dist"),
		workdir: workdir,
		socket:  fmt.Sprintf("runpilote2e%d", apiPort),
		nodeBin: filepath.Join(home, "runpilot-node"),
	}
	if err := os.MkdirAll(m.distDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(distFile, filepath.Join(m.distDir, "runpilot")); err != nil {
		t.Fatal(err)
	}
	// Профиль qwen: mockcoder как кодер (cmdline совпадает со сканером узла).
	profDir := filepath.Join(home, ".config", "runpilot", "profiles")
	if err := os.MkdirAll(profDir, 0o755); err != nil {
		t.Fatal(err)
	}
	prof := "name: qwen\ncommand: " + mockcoder + "\ncmdline_regex: runpilot-mockcoder\nclassify_lines: 3\ntab_width: 8\n"
	if err := os.WriteFile(filepath.Join(profDir, "qwen.yaml"), []byte(prof), 0o644); err != nil {
		t.Fatal(err)
	}
	// Координатор: петлевой, своя БД/dist; gateway_url → API (dist на API-порту).
	cfg := fmt.Sprintf(`coordinator:
  bind: 127.0.0.1
  api_port: %d
  gateway_port: %d
  gateway_url: %q
  dist_dir: %q
  db_path: %q
  embedded_node: false
  token_env: RUNPILOT_TOKEN
web:
  bind: 127.0.0.1
  port: %d
node:
  tmux_sockets: [%q]
  project_roots_default: [%q]
  scan_interval_sec: 1
client:
  coordinator: %q
  token_env: RUNPILOT_NODE_TOKEN
enroll:
  selftest_timeout_sec: 20
`, apiPort, gwPort, m.apiURL, m.distDir, m.dbPath, webPort, m.socket, workdir, m.apiURL)
	cfgDir := filepath.Join(home, ".config", "runpilot")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	return m
}

// setNodeBin — копия бинарника узла (v1/v2); self-update меняет m.nodeBin.
func (m *machine) setNodeBin(t *testing.T, src string) {
	t.Helper()
	if err := copyFile(src, m.nodeBin); err != nil {
		t.Fatalf("nodeBin: %v", err)
	}
}

// --- процессы runpilot ---

type proc struct {
	cmd  *exec.Cmd
	logf *os.File
}

func startProc(t *testing.T, name, bin string, args []string, env []string) *proc {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), env...)
	logf, err := os.CreateTemp("", "runpilot-e2e-"+name+"-*.log")
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

func (m *machine) homeEnv() []string { return []string{"HOME=" + m.home} }

func (m *machine) cfgPath() string {
	return filepath.Join(m.home, ".config", "runpilot", "config.yaml")
}

// startCoordinator — координатор (всегда runpilotV2: «новая версия»).
func (m *machine) startCoordinator(t *testing.T) *proc {
	return startProc(t, "coord", runpilotV2,
		[]string{"serve", "--config", m.cfgPath()},
		append(m.homeEnv(), "RUNPILOT_TOKEN="+operatorToken))
}

// startNode — узел (m.nodeBin) с токеном RUNPILOT_NODE_TOKEN.
func (m *machine) startNode(t *testing.T, tok string) *proc {
	return startProc(t, "node", m.nodeBin,
		[]string{"node", "--config", m.cfgPath()},
		append(m.homeEnv(), "RUNPILOT_NODE_TOKEN="+tok))
}

// --- API координатора (петлевой /api/v1, Bearer-токен оператора) ---

// do — запрос к API с Bearer-токеном (раздел 14: /api/v1 за токен оператора).
func (m *machine) do(t *testing.T, method, path string, body []byte) *http.Response {
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
		return nil
	}
	return resp
}

func waitAPI(t *testing.T, m *machine) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if resp := m.do(t, http.MethodGet, "/api/v1/nodes", nil); resp != nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("API координатора не ответил за 30с (%s)", m.apiURL)
}

func enrollToken(t *testing.T, m *machine) string {
	t.Helper()
	resp := m.do(t, http.MethodPost, "/api/v1/nodes/enroll", []byte("{}"))
	if resp == nil {
		t.Fatal("enroll: нет ответа")
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		t.Fatalf("enroll = %d: %s", resp.StatusCode, b)
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(b, &out); err != nil || out.Token == "" {
		t.Fatalf("enroll без токена: %s", b)
	}
	return out.Token
}

// nodeVersion — версия единственного узла из /api/v1/nodes ("" если нет).
func nodeVersion(t *testing.T, m *machine) string {
	t.Helper()
	resp := m.do(t, http.MethodGet, "/api/v1/nodes", nil)
	if resp == nil {
		return ""
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var nodes []struct {
		RUNPILOTVersion string `json:"RUNPILOTVersion"`
	}
	if err := json.Unmarshal(b, &nodes); err != nil || len(nodes) == 0 {
		return ""
	}
	return nodes[0].RUNPILOTVersion
}

// sessionSIDs — sid-ы сессий из /api/v1/sessions?all=true (все записи, не
// только «живые»: CONTROL 2 проверяет, что записи сессий пережили update).
func sessionSIDs(t *testing.T, m *machine) []string {
	t.Helper()
	resp := m.do(t, http.MethodGet, "/api/v1/sessions?all=true", nil)
	if resp == nil {
		return nil
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var ss []struct {
		SID string `json:"sid"`
	}
	if err := json.Unmarshal(b, &ss); err != nil {
		return nil
	}
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		out = append(out, s.SID)
	}
	return out
}

// spawnSession — новая сессия из веба ( CONTROL 5/6 ): tmux-панель с mockcoder.
func spawnSession(t *testing.T, m *machine, name string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"name": name, "dir": m.workdir})
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		resp := m.do(t, http.MethodPost, "/api/v1/sessions/spawn", body)
		if resp != nil {
			rb, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusCreated {
				return
			}
			if resp.StatusCode == http.StatusServiceUnavailable || resp.StatusCode == http.StatusBadGateway {
				time.Sleep(500 * time.Millisecond)
				continue
			}
			t.Fatalf("spawn %s = %d: %s", name, resp.StatusCode, rb)
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("spawn %s не прошёл за 30с", name)
}

// waitForOnline — узел онлайн ( CONTROL 1 ).
func waitForOnline(t *testing.T, m *machine, limit time.Duration) time.Duration {
	t.Helper()
	t0 := time.Now()
	deadline := t0.Add(limit)
	for time.Now().Before(deadline) {
		if nodeVersion(t, m) != "" {
			return time.Since(t0)
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("узел не онлайн за %v (CONTROL 1)", limit)
	return 0
}

// waitSessions — ждать N сессий, вернуть их sid-ы.
func waitSessions(t *testing.T, m *machine, n int) []string {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if s := sessionSIDs(t, m); len(s) == n {
			return s
		}
		time.Sleep(400 * time.Millisecond)
	}
	t.Fatalf("не дождались %d сессий (CONTROL)", n)
	return nil
}

// binVersion — <bin> version (новый процесс из файла).
func binVersion(t *testing.T, bin string) string {
	t.Helper()
	out, err := exec.Command(bin, "version").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// writeBadSelftestBin — бинарник, который FAILS <bin> selftest (exit 1).
func writeBadSelftestBin(path string) error {
	script := "#!/bin/sh\n# e2e: бинарник с проваленным selftest\nexit 1\n"
	return os.WriteFile(path, []byte(script), 0o755)
}

// --- CONTROL 1: чистая машина → узел онлайн ≤ 60 с ---
// Координатор и узел одной версии (чистая установка): самообновления нет.

func TestControl1NodeOnline(t *testing.T) {
	m := newMachine(t, runpilotV2)
	m.setNodeBin(t, runpilotV2) // та же версия, что у координатора
	m.startCoordinator(t)
	waitAPI(t, m)
	tok := enrollToken(t, m)
	m.startNode(t, tok)

	elapsed := waitForOnline(t, m, onlineLimit)
	writeEvidence("control1_node_online.json", map[string]any{
		"elapsed_ms": elapsed.Milliseconds(), "limit_ms": onlineLimit.Milliseconds(),
		"pass": elapsed <= onlineLimit,
	})
	t.Logf("CONTROL 1: узел онлайн за %v (лимит %v)", elapsed, onlineLimit)
}

// --- CONTROL 2: новая версия координатора → узел обновлён ≤ 120 с, сессии равны ---

func TestControl2SelfUpdate(t *testing.T) {
	m := newMachine(t, runpilotV2) // dist = runpilot-v2 (новая версия)
	m.setNodeBin(t, runpilotV1)    // узел стартует на старой версии
	m.startCoordinator(t)
	waitAPI(t, m)
	tok := enrollToken(t, m)
	node := m.startNode(t, tok)

	waitForOnline(t, m, onlineLimit) // v1 онлайн
	spawnSession(t, m, "e2e-sess-1") // сессия ДО обновления
	before := waitSessions(t, m, 1)

	t0 := time.Now()
	// v2(координатор) != v1(узел) → self-update по hello: download+SHA+selftest+swap.
	waitBinSwapped(t, m, v2)
	// Перезапуск узла (аналог systemd Restart=always): теперь работает runpilot-v2.
	killProc(node)
	m.startNode(t, tok)
	waitNodeVersion(t, m, v2, updateLimit)
	after := waitSessions(t, m, 1)
	elapsed := time.Since(t0)

	if len(before) != 1 || before[0] != after[0] {
		t.Fatalf("список сессий изменился: до=%v после=%v", before, after)
	}
	writeEvidence("control2_self_update.json", map[string]any{
		"elapsed_ms": elapsed.Milliseconds(), "limit_ms": updateLimit.Milliseconds(),
		"before_version": v1, "after_version": v2,
		"sessions_before": before, "sessions_after": after,
		"pass": elapsed <= updateLimit && len(before) > 0,
	})
	t.Logf("CONTROL 2: узел v1→v2 за %v (лимит %v), сессии=%v", elapsed, updateLimit, after)
}

// --- CONTROL 3: проваленный selftest → бинарник не ставится, узел на старой версии ---

func TestControl3SelftestFail(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "runpilot-bad")
	if err := writeBadSelftestBin(bad); err != nil {
		t.Fatal(err)
	}
	m := newMachine(t, bad) // dist = бинарник, проваливающий selftest
	m.setNodeBin(t, runpilotV1)
	m.startCoordinator(t)
	waitAPI(t, m)
	tok := enrollToken(t, m)
	m.startNode(t, tok)
	waitForOnline(t, m, onlineLimit)

	// Координатор шлёт update (v2 != v1); узел скачает bad, selftest упадёт.
	time.Sleep(8 * time.Second)

	ver := nodeVersion(t, m)
	if ver == "" {
		t.Fatalf("узел не онлайн")
	}
	if ver != v1 {
		t.Fatalf("CONTROL 3: узел обновился до %q при проваленном selftest (хочу %q)", ver, v1)
	}
	if got := binVersion(t, m.nodeBin); got != v1 {
		t.Fatalf("CONTROL 3: nodeBin заменён на %q (хочу %q)", got, v1)
	}
	writeEvidence("control3_selftest_fail.json", map[string]any{
		"node_version": ver, "want": v1, "bin_version": binVersion(t, m.nodeBin),
		"pass": ver == v1,
	})
	t.Logf("CONTROL 3: selftest упал → узел остался на %q", ver)
}

// waitBinSwapped — ждать, пока m.nodeBin выполнит <bin> version == want (swap).
func waitBinSwapped(t *testing.T, m *machine, want string) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		if binVersion(t, m.nodeBin) == want {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("nodeBin не стал %q за 90с (self-update swap)", want)
}

// waitNodeVersion — ждать, пока узел в /nodes будет онлайн на версии want.
func waitNodeVersion(t *testing.T, m *machine, want string, limit time.Duration) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if nodeVersion(t, m) == want {
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("узел не онлайн на версии %q за %v", want, limit)
}
