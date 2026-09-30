//go:build e2e

// CONTROL W4 (e2e: go-rod + аqm-stand) — раздел 19.4 / CONTROL (строка 1412):
//
//   (1) событие → DOM ≤ 250 мс (p95 на 200 событиях, метки времени rod);
//   (2) разрыв SSE → состояние клиента == GET /api/v1/state (пустой diff);
//   (3) why.text — полнота в GET /api/v1/meta (дополнительно к unit
//       internal/texts.TestWhyTextAllCodes).
//
// Запуск (вне make check — build-тег e2e, нужен headless Chrome):
//
//	go test -tags e2e -count=1 ./test/e2e/web
//
// Evidence → docs/evidence/W4/control/*.json (флаг -out, по умолчанию туда).
package webtest

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/launcher/flags"
	"github.com/go-rod/rod/lib/proto"
)

const (
	token    = "runpilot-stand-test-token"
	startT   = "2026-09-26T10:00:00Z"
	domLimit = 250 * time.Millisecond
	// fireOverReplay — событий больше web.sse_replay(200): при переподключении
	// сервер шлёт RESYNC, а не досылку (CONTROL 2).
	fireOverReplay = 250
)

var (
	chromeFlag = flag.String("chrome", "/usr/bin/google-chrome", "путь к Chrome/Chromium")
	outFlag    = flag.String("out", "docs/evidence/W4/control", "каталог evidence (пусто — не писать)")
)

var (
	rootDir  string
	standBin string
	browser  *rod.Browser
)

// observerJS — ловит console.error и ошибки (как в tools/screenshots).
const observerJS = `
window.__runpilotConsole = [];
(function(){
  ['error','warn'].forEach(function(lv){
    var o = console[lv];
    console[lv] = function(){ try{ window.__runpilotConsole.push(lv+': '+[].map.call(arguments,String).join(' ')); }catch(e){} return o.apply(this,arguments); };
  });
  window.addEventListener('error', function(e){ try{ window.__runpilotConsole.push('error: '+(e.message||'')); }catch(err){} });
  window.addEventListener('unhandledrejection', function(e){ try{ window.__runpilotConsole.push('rejection: '+String(e.reason)); }catch(err){} });
})();
`

func TestMain(m *testing.M) {
	flag.Parse()
	r, err := findModuleRoot()
	if err != nil {
		e2efatal("go.mod: %v", err)
	}
	rootDir = r
	standBin = filepath.Join(os.TempDir(), fmt.Sprintf("runpilot-stand-e2e-%d", os.Getpid()))
	bld := exec.Command("go", "build", "-o", standBin, "./cmd/runpilot-stand")
	bld.Dir = rootDir
	if out, err := bld.CombinedOutput(); err != nil {
		e2efatal("сборка стенда: %v\n%s", err, out)
	}
	defer os.Remove(standBin)

	l := launcher.New().Bin(*chromeFlag).Headless(true).NoSandbox(true)
	l.Set(flags.Flag("disable-gpu"))
	l.Set(flags.Flag("disable-dev-shm-usage"))
	l.Set(flags.Flag("hide-scrollbars"))
	l.Set(flags.Flag("force-color-profile"), "srgb")
	l.Set(flags.Flag("no-first-run"))
	ws, err := l.Launch()
	if err != nil {
		e2efatal("chrome: %v", err)
	}
	browser = rod.New().ControlURL(ws).MustConnect()
	defer browser.Close()
	defer l.Kill()

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

// startStand — аqm-stand как подпроцесс (свой порт/БД), info-file → web_url.
func startStand(t *testing.T, scenario string) string {
	t.Helper()
	infoFile := filepath.Join(t.TempDir(), "stand.json")
	cmd := exec.Command(standBin, "-token", token, "-start", startT,
		"-scenario", scenario, "-info-file", infoFile)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("запуск стенда: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(infoFile); err == nil {
			var info struct {
				WebURL string `json:"web_url"`
			}
			if json.Unmarshal(b, &info) == nil && info.WebURL != "" {
				return info.WebURL
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("стенд не записал info-file за 30с")
	return ""
}

// openApp — новая вкладка: логин + переход на страницу + догрузка (SSE).
func openApp(t *testing.T, webURL, page string) *rod.Page {
	t.Helper()
	pg, err := browser.Page(proto.TargetCreateTarget{URL: "about:blank"})
	if err != nil {
		t.Fatalf("вкладка: %v", err)
	}
	t.Cleanup(func() { _ = pg.Close() })
	pg.MustSetViewport(1440, 900, 1, false)
	pg.MustEvalOnNewDocument(observerJS)

	pg.MustNavigate(webURL + "/web/login")
	time.Sleep(300 * time.Millisecond)
	pg.MustElement("#token").MustInput(token)
	pg.MustElement("button[type=submit]").MustClick()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if v, err := pg.Eval("() => location.pathname"); err == nil && v.Value.Str() == "/" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if page != "queue" {
		pg.MustEval(fmt.Sprintf("() => { location.hash = '#/%s'; }", page))
	}
	time.Sleep(1500 * time.Millisecond) // догрузка данных + SSE-подключение
	return pg
}

func requireOnline(t *testing.T, pg *rod.Page) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if v, err := pg.Eval("() => window.__runpilotState().online"); err == nil && v.Value.Bool() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("SSE не подключился за 10с")
}

// checkConsole — console.error/rejection → FAIL.
func checkConsole(t *testing.T, pg *rod.Page) {
	t.Helper()
	v, err := pg.Eval("() => JSON.stringify(window.__runpilotConsole||[])")
	if err != nil {
		return
	}
	var c []string
	_ = json.Unmarshal([]byte(v.Value.Str()), &c)
	for _, f := range c {
		if strings.HasPrefix(f, "error") || strings.HasPrefix(f, "rejection") {
			t.Fatalf("консоль: %s", f)
		}
	}
}

func readSSEId(t *testing.T, pg *rod.Page) int64 {
	t.Helper()
	v, err := pg.Eval(`() => { const el = document.querySelector('[data-sse-id]'); return el ? el.getAttribute('data-sse-id') : '0'; }`)
	if err != nil {
		t.Fatalf("data-sse-id: %v", err)
	}
	id, _ := strconv.ParseInt(v.Value.Str(), 10, 64)
	return id
}

func mustPost(t *testing.T, pg *rod.Page, js string) {
	t.Helper()
	if _, err := pg.Eval(js); err != nil {
		t.Fatalf("fetch: %v", err)
	}
}

func evalJSON(t *testing.T, pg *rod.Page, js string) []byte {
	t.Helper()
	v, err := pg.Eval(js)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	return []byte(v.Value.Str())
}

func writeEvidence(t *testing.T, name string, data any) {
	t.Helper()
	if *outFlag == "" {
		return
	}
	outDir := *outFlag
	if !filepath.IsAbs(outDir) {
		// go test работает в каталоге пакета — относительный путь от модуля.
		outDir = filepath.Join(rootDir, outDir)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Logf("evidence: каталог: %v", err)
		return
	}
	b, _ := json.MarshalIndent(data, "", "  ")
	if err := os.WriteFile(filepath.Join(outDir, name), b, 0o644); err != nil {
		t.Logf("evidence %s: %v", name, err)
	}
}

func percentile(d []time.Duration, p int) time.Duration {
	if len(d) == 0 {
		return 0
	}
	s := make([]int64, len(d))
	for i, x := range d {
		s[i] = x.Nanoseconds()
	}
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	idx := int(math.Ceil(float64(p)/100*float64(len(s)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(s) {
		idx = len(s) - 1
	}
	return time.Duration(s[idx]) * time.Nanosecond
}

func maxDur(d []time.Duration) time.Duration {
	var m time.Duration
	for _, x := range d {
		if x > m {
			m = x
		}
	}
	return m
}

// ---- Срезы состояния для CONTROL 2 ----

type sessRef struct{ State, Hold string }

type stateSnap struct {
	Mode     string
	Sessions map[string]sessRef
	Servers  map[string]string
	Nodes    map[string]bool
}

func emptySnap(mode string) stateSnap {
	return stateSnap{Mode: mode, Sessions: map[string]sessRef{}, Servers: map[string]string{}, Nodes: map[string]bool{}}
}

// clientSnap — срез состояния клиента (window.__runpilotState, нормализованный).
func clientSnap(t *testing.T, pg *rod.Page) stateSnap {
	t.Helper()
	b := evalJSON(t, pg, "() => JSON.stringify(window.__runpilotState())")
	var s struct {
		Mode     string `json:"mode"`
		Sessions []struct {
			SID   string `json:"sid"`
			State string `json:"state"`
			Hold  string `json:"hold_reason"`
		} `json:"sessions"`
		Servers []struct {
			Name  string `json:"name"`
			State string `json:"state"`
		} `json:"servers"`
		Nodes []struct {
			Host string `json:"host"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatalf("client state: %v", err)
	}
	out := emptySnap(s.Mode)
	for _, x := range s.Sessions {
		out.Sessions[x.SID] = sessRef{x.State, x.Hold}
	}
	for _, x := range s.Servers {
		out.Servers[x.Name] = x.State
	}
	for _, x := range s.Nodes {
		out.Nodes[x.Host] = true
	}
	return out
}

// serverSnap — GET /api/v1/state (сырые ключи SessionRecord/NodeView).
func serverSnap(t *testing.T, pg *rod.Page) stateSnap {
	t.Helper()
	b := evalJSON(t, pg, `() => (async()=>{const r=await fetch('/api/v1/state');return JSON.stringify(await r.json());})()`)
	var s struct {
		Mode     string `json:"mode"`
		Sessions []struct {
			SID   string `json:"SID"`
			State string `json:"State"`
			Hold  string `json:"HoldReason"`
		} `json:"sessions"`
		Servers []struct {
			Name  string `json:"name"`
			State string `json:"state"`
		} `json:"servers"`
		Nodes []struct {
			Host string `json:"Host"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatalf("/state: %v", err)
	}
	out := emptySnap(s.Mode)
	for _, x := range s.Sessions {
		out.Sessions[x.SID] = sessRef{x.State, x.Hold}
	}
	for _, x := range s.Servers {
		out.Servers[x.Name] = x.State
	}
	for _, x := range s.Nodes {
		out.Nodes[x.Host] = true
	}
	return out
}

func clientSess(t *testing.T, pg *rod.Page, sid string) sessRef {
	t.Helper()
	if r, ok := clientSnap(t, pg).Sessions[sid]; ok {
		return r
	}
	return sessRef{}
}

// fullStateDiff — разница клиент vs /state (пусто == состояние совпадает).
func fullStateDiff(cli, srv stateSnap) []string {
	var d []string
	if cli.Mode != srv.Mode {
		d = append(d, fmt.Sprintf("mode: клиент=%q /state=%q", cli.Mode, srv.Mode))
	}
	for sid, c := range cli.Sessions {
		sr, ok := srv.Sessions[sid]
		if !ok {
			d = append(d, fmt.Sprintf("session %s: нет в /state", sid))
			continue
		}
		if c != sr {
			d = append(d, fmt.Sprintf("session %s: клиент=%+v /state=%+v", sid, c, sr))
		}
	}
	for sid := range srv.Sessions {
		if _, ok := cli.Sessions[sid]; !ok {
			d = append(d, fmt.Sprintf("session %s: нет у клиента", sid))
		}
	}
	for name, c := range cli.Servers {
		sr, ok := srv.Servers[name]
		if !ok {
			d = append(d, fmt.Sprintf("server %s: нет в /state", name))
			continue
		}
		if c != sr {
			d = append(d, fmt.Sprintf("server %s: клиент=%q /state=%q", name, c, sr))
		}
	}
	for name := range srv.Servers {
		if _, ok := cli.Servers[name]; !ok {
			d = append(d, fmt.Sprintf("server %s: нет у клиента", name))
		}
	}
	for h := range cli.Nodes {
		if !srv.Nodes[h] {
			d = append(d, fmt.Sprintf("node %s: нет в /state", h))
		}
	}
	for h := range srv.Nodes {
		if !cli.Nodes[h] {
			d = append(d, fmt.Sprintf("node %s: нет у клиента", h))
		}
	}
	return d
}

// ---- CONTROL 1: событие → DOM ≤ 250 мс (p95, 200 событий) ----

func TestControl1EventToDOM(t *testing.T) {
	webURL := startStand(t, "normal")
	pg := openApp(t, webURL, "queue")
	requireOnline(t, pg)

	const n = 200
	latencies := make([]time.Duration, 0, n)
	prev := readSSEId(t, pg)
	for i := 0; i < n; i++ {
		mustPost(t, pg, `() => fetch('/control/fire?n=1',{method:'POST'}).then(r=>r.status)`)
		t0 := time.Now() // событие записано (fetch вернулся)
		for {
			id := readSSEId(t, pg)
			if id > prev {
				latencies = append(latencies, time.Since(t0))
				prev = id
				break
			}
			if time.Since(t0) > 3*time.Second {
				t.Fatalf("событие #%d не дошло до DOM за 3с (id=%d prev=%d)", i, id, prev)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	checkConsole(t, pg)

	p50 := percentile(latencies, 50)
	p95 := percentile(latencies, 95)
	writeEvidence(t, "control1_event_dom.json", map[string]any{
		"n": len(latencies), "p50_ms": p50.Milliseconds(),
		"p95_ms": p95.Milliseconds(), "max_ms": maxDur(latencies).Milliseconds(),
		"limit_ms": domLimit.Milliseconds(), "pass": p95 <= domLimit,
	})
	t.Logf("событие→DOM: n=%d p50=%v p95=%v max=%v (лимит %v)",
		len(latencies), p50, p95, maxDur(latencies), domLimit)
	if p95 > domLimit {
		t.Fatalf("событие→DOM p95 = %v > %v", p95, domLimit)
	}
}

// ---- CONTROL 2: разрыв SSE → состояние клиента == GET /api/v1/state ----

func TestControl2SSEResync(t *testing.T) {
	webURL := startStand(t, "normal")
	pg := openApp(t, webURL, "queue")
	requireOnline(t, pg)

	before := serverSnap(t, pg)
	var targetSID string
	for sid := range before.Sessions {
		targetSID = sid
		break
	}
	if targetSID == "" {
		t.Fatal("в /state нет сессий")
	}

	// 1) Разрыв всех SSE-подключений (клиент переподключится сам).
	mustPost(t, pg, `() => fetch('/control/drop-sse',{method:'POST'}).then(r=>r.status)`)
	// 2) Во время разрыва: смена состояния сессии + fired >> sse_replay.
	sidJS, _ := json.Marshal(targetSID)
	mustPost(t, pg, fmt.Sprintf(
		`() => fetch('/control/state',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({sid:%s,to:'HOLD',hold:'OPERATOR_HOLD'})}).then(r=>r.status)`, sidJS))
	mustPost(t, pg, fmt.Sprintf(`() => fetch('/control/fire?n=%d',{method:'POST'}).then(r=>r.status)`, fireOverReplay))

	// 3) Ждём: клиент переподключился → RESYNC → заново /state → сессия HOLD.
	var got sessRef
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		got = clientSess(t, pg, targetSID)
		if got.State == "HOLD" {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	checkConsole(t, pg)
	if got.State != "HOLD" {
		t.Fatalf("клиент не увидел смену %s→HOLD за 20с (state=%q)", targetSID, got.State)
	}

	// 4) Состояние клиента == GET /api/v1/state (пустой diff по ключевым полям).
	cli := clientSnap(t, pg)
	srv := serverSnap(t, pg)
	diffs := fullStateDiff(cli, srv)
	writeEvidence(t, "control2_sse_resync.json", map[string]any{
		"target_sid": targetSID, "fired": fireOverReplay,
		"recovered": got.State == "HOLD", "diff": diffs, "pass": len(diffs) == 0,
	})
	if len(diffs) > 0 {
		t.Fatalf("diff клиент vs /state не пуст (%d):\n%s", len(diffs), strings.Join(diffs, "\n"))
	}
	t.Logf("разрыв SSE → ресинк: сессия %s=HOLD, diff пуст (сессии=%d серверы=%d узлы=%d)",
		targetSID, len(cli.Sessions), len(cli.Servers), len(cli.Nodes))
}

// ---- CONTROL 3: why.text — полнота в GET /api/v1/meta ----

func TestControl3WhyTextMeta(t *testing.T) {
	webURL := startStand(t, "normal")
	pg := openApp(t, webURL, "queue")
	requireOnline(t, pg)

	b := evalJSON(t, pg, `() => (async()=>{const r=await fetch('/api/v1/meta');return JSON.stringify(await r.json());})()`)
	var meta struct {
		Ineligible  map[string]string `json:"ineligible"`
		HoldReasons map[string]string `json:"hold_reasons"`
	}
	if err := json.Unmarshal(b, &meta); err != nil {
		t.Fatalf("meta: %v", err)
	}
	// Пустой/совпадающий с кодом текст = пропуск подписи.
	bad := 0
	for code, txt := range meta.Ineligible {
		if strings.TrimSpace(txt) == "" || strings.TrimSpace(txt) == code {
			t.Errorf("ineligible %q: why.text пуст или = коду", code)
			bad++
		}
	}
	for code, txt := range meta.HoldReasons {
		if strings.TrimSpace(txt) == "" || strings.TrimSpace(txt) == code {
			t.Errorf("hold_reason %q: why.text пуст или = коду", code)
			bad++
		}
	}
	writeEvidence(t, "control3_why_meta.json", map[string]any{
		"ineligible": len(meta.Ineligible), "hold_reasons": len(meta.HoldReasons),
		"bad": bad, "pass": bad == 0 && len(meta.Ineligible) > 0 && len(meta.HoldReasons) > 0,
	})
	checkConsole(t, pg)
	if len(meta.Ineligible) == 0 || len(meta.HoldReasons) == 0 {
		t.Fatalf("meta неполный: ineligible=%d hold_reasons=%d", len(meta.Ineligible), len(meta.HoldReasons))
	}
	t.Logf("why.text: ineligible=%d hold_reasons=%d, пусто/дублей кода: %d",
		len(meta.Ineligible), len(meta.HoldReasons), bad)
}
