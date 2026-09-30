package node

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"runpilot/internal/clock"
	"runpilot/internal/config"
	"runpilot/internal/detect"
	"runpilot/internal/proto"
	"runpilot/profiles"
)

// newTestNode — узел на fakeExec с дефолтным конфигом.
func newTestNode(t *testing.T, ex *fakeExec) *Node {
	t.Helper()
	cfg := config.Defaults()
	cfg.Node.TmuxSockets = []string{"default"}
	prof, err := profiles.LoadEmbeddedQwen()
	if err != nil {
		t.Fatal(err)
	}
	n, err := New(cfg, prof, ex, clock.NewReal(),
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// --- validateDir (CONTROL 6, раздел 13.2) ---

func TestValidateDir(t *testing.T) {
	rootA := t.TempDir()
	rootB := t.TempDir()
	proj := filepath.Join(rootA, "proj")
	if err := os.Mkdir(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	// Симлинк внутри rootA → каталог вне (rootB/leak).
	leak := filepath.Join(rootB, "leak")
	if err := os.Mkdir(leak, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(rootA, "link")
	if err := os.Symlink(leak, link); err != nil {
		t.Fatal(err)
	}

	ex := &fakeExec{}
	n := newTestNode(t, ex)
	n.SetProjectRoots([]string{rootA})

	cases := []struct {
		name string
		dir  string
		want string
	}{
		{"inside", proj, ""},
		{"root-itself", rootA, ""},
		{"outside", leak, proto.ResDirForbidden},
		{"symlink-outside", link, proto.ResDirForbidden},
		{"missing", filepath.Join(rootA, "nope"), proto.ResDirNotFound},
	}
	for _, c := range cases {
		if got := n.validateDir(c.dir); got != c.want {
			t.Errorf("validateDir(%s) = %q, хочу %q", c.name, got, c.want)
		}
	}
}

// --- spawn (CONTROL 5, раздел 13.2) ---

func TestSpawn(t *testing.T) {
	dir := t.TempDir()
	ex := &fakeExec{}
	ex.respond = func(name string, args ...string) (string, error) {
		s := strings.Join(args, " ")
		switch {
		case strings.Contains(s, "has-session"):
			return "", errors.New("no session") // имя свободно
		case strings.Contains(s, "new-session"):
			return "", nil
		case strings.Contains(s, "list-panes"):
			return "%0\n", nil
		case strings.Contains(s, "set-option"):
			return "", nil
		}
		return "", nil
	}
	n := newTestNode(t, ex)
	n.SetProjectRoots([]string{dir})

	m := proto.New(proto.KindSpawn)
	m.CmdID = "c1"
	m.Name = "sess-x"
	m.Dir = dir
	m.SID = "SS00000001"
	rep, err := n.Handle(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Result != proto.ResSpawned {
		t.Fatalf("Result = %q, хочу SPAWNED (detail=%q)", rep.Result, rep.Detail)
	}
	if rep.Detail != "%0" {
		t.Errorf("Detail (панель) = %q, хочу %%0", rep.Detail)
	}
	// Панель получила @runpilot_sid (CONTROL 5: pane with @runpilot_sid ≤5s).
	if !ex.callContains("set-option", "@runpilot_sid", "SS00000001") {
		t.Error("spawn не поставил @runpilot_sid на панель")
	}
}

func TestSpawnNameInUse(t *testing.T) {
	ex := &fakeExec{}
	ex.respond = func(name string, args ...string) (string, error) {
		if strings.Contains(strings.Join(args, " "), "has-session") {
			return "", nil // сессия уже есть → коллизия
		}
		return "", errors.New("unexpected")
	}
	d := t.TempDir()
	n := newTestNode(t, ex)
	n.SetProjectRoots([]string{d})
	m := proto.New(proto.KindSpawn)
	m.CmdID = "c1"
	m.Name = "taken"
	m.Dir = d
	rep, _ := n.Handle(context.Background(), m)
	if rep.Result != "NAME_IN_USE" {
		t.Fatalf("Result = %q, хочу NAME_IN_USE", rep.Result)
	}
}

// --- list_dirs ---

func TestListDirs(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"b", "a"} {
		if err := os.Mkdir(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	ex := &fakeExec{}
	n := newTestNode(t, ex)
	n.SetProjectRoots([]string{root})
	m := proto.New(proto.KindListDirs)
	m.CmdID = "c1"
	rep, _ := n.Handle(context.Background(), m)
	if rep.Result != ResSent {
		t.Fatalf("Result = %q, хочу SENT", rep.Result)
	}
	dirs := strings.Split(rep.Detail, "\n")
	if len(dirs) != 2 || !strings.HasSuffix(dirs[0], "/a") || !strings.HasSuffix(dirs[1], "/b") {
		t.Errorf("dirs = %v, хочу [a b] в порядке сортировки", dirs)
	}
	want, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Roots) != 1 || rep.Roots[0] != want {
		t.Errorf("Roots = %v, хочу [%s]", rep.Roots, want)
	}
}

// list_dirs → reply.msg(): Roots попадает в существующее поле proto
// ProjectRoots (подсказка веба с конкретными путями).
func TestReplyMsgCarriesRoots(t *testing.T) {
	m := Reply{CmdID: "c1", Result: ResSent, Roots: []string{"/root/a"}}.msg()
	if len(m.ProjectRoots) != 1 || m.ProjectRoots[0] != "/root/a" {
		t.Errorf("ProjectRoots = %v, хочу [/root/a]", m.ProjectRoots)
	}
	if m.CmdID != "c1" || m.Result != ResSent {
		t.Errorf("msg() = %+v", m)
	}
}

// --- kill_session / release_pane / respawn ---

func TestKillSession(t *testing.T) {
	ex := &fakeExec{}
	ex.respond = func(name string, args ...string) (string, error) {
		if strings.Contains(strings.Join(args, " "), "display-message") {
			return "sessname\n", nil
		}
		return "", nil
	}
	n := newTestNode(t, ex)
	seedPane(t, n, "%7", "default")
	m := proto.New(proto.KindKillSession)
	m.CmdID = "c1"
	m.PaneID = "%7"
	rep, _ := n.Handle(context.Background(), m)
	if rep.Result != proto.ResKilled || rep.Detail != "sessname" {
		t.Fatalf("Result=%q Detail=%q, хочу KILLED/sessname", rep.Result, rep.Detail)
	}
	if !ex.callContains("kill-session", "sessname") {
		t.Error("kill-session не вызван с именем сессии")
	}
}

func TestReleasePane(t *testing.T) {
	ex := &fakeExec{}
	n := newTestNode(t, ex)
	seedPane(t, n, "%9", "default")
	m := proto.New(proto.KindReleasePane)
	m.CmdID = "c1"
	m.PaneID = "%9"
	rep, _ := n.Handle(context.Background(), m)
	if rep.Result != ResSent {
		t.Fatalf("Result = %q, хочу SENT", rep.Result)
	}
	if !ex.callContains("set-option", "-u", "@runpilot_sid") {
		t.Error("release_pane не снял @runpilot_sid (unset)")
	}
}

func TestRespawn(t *testing.T) {
	ex := &fakeExec{}
	n := newTestNode(t, ex)
	seedPane(t, n, "%3", "default")
	m := proto.New(proto.KindRespawn)
	m.CmdID = "c1"
	m.PaneID = "%3"
	rep, _ := n.Handle(context.Background(), m)
	if rep.Result != proto.ResRespawned {
		t.Fatalf("Result = %q, хочу RESPAWNED", rep.Result)
	}
	if !ex.callContains("respawn-pane", "%3") {
		t.Error("respawn-pane не вызван")
	}
}

// --- kill_process ---

func TestKillProcess(t *testing.T) {
	ex := &fakeExec{}
	n := newTestNode(t, ex)
	m := proto.New(proto.KindKillProcess)
	m.CmdID = "c1"
	m.PID = 4242
	rep, _ := n.Handle(context.Background(), m)
	if rep.Result != proto.ResKilled || rep.Detail != "TERM:4242" {
		t.Fatalf("Result=%q Detail=%q, хочу KILLED/TERM:4242", rep.Result, rep.Detail)
	}

	bad := proto.New(proto.KindKillProcess)
	bad.CmdID = "c2"
	bad.PID = 0
	rep2, _ := n.Handle(context.Background(), bad)
	if rep2.Result != ResBadKeys {
		t.Errorf("pid=0 → Result=%q, хочу BAD_KEYS", rep2.Result)
	}
}

// --- update / self-update (CONTROL 3, раздел 14.2) ---

// seedPane — добавить managed-панель в снапшот узла, чтобы socketFor нашла
// сокет (реальный путь: cycle синхронизирует управляемые панели в snap).
func seedPane(t *testing.T, n *Node, paneID, socket string) {
	t.Helper()
	n.snap.sync(PaneInfo{PaneID: paneID, Socket: socket}, detect.State(0), 0, "",
		clock.NewReal().Now())
}

// seedUnmanaged — добавить unmanaged-панель в реестр узла, чтобы socketFor
// нашла сокет (реальный путь: cycle заполняет реестр unmanaged, 8.3 X1).
// НЕ кладёт панель в snap — это и ловит исходный баг adopt→PANE_GONE.
func seedUnmanaged(t *testing.T, n *Node, p PaneInfo) {
	t.Helper()
	if p.Socket == "" {
		p.Socket = "default"
	}
	n.umMu.Lock()
	n.unmanaged[p.PaneID] = p
	n.umMu.Unlock()
}

// withSelftest — временно подменить selftestFunc и вернуть defer-восстановление.
func withSelftest(t *testing.T, fn func(ctx context.Context, bin string, timeout time.Duration) error) {
	t.Helper()
	old := selftestFunc
	selftestFunc = fn
	t.Cleanup(func() { selftestFunc = old })
}

// TestUpdateSelftestFailKeepsOld — CONTROL 3: selftest нового бинарника упал →
// новый НЕ ставится, текущий бинарник остаётся прежним.
func TestUpdateSelftestFailKeepsOld(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "runpilot")
	if err := os.WriteFile(bin, []byte("OLD-BIN"), 0o755); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer node-tok" {
			t.Errorf("download без Bearer-токена узла: %q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte("NEW-BIN"))
	}))
	defer srv.Close()

	withSelftest(t, func(_ context.Context, _ string, _ time.Duration) error {
		return errors.New("selftest boom")
	})

	ex := &fakeExec{}
	n := newTestNode(t, ex)
	n.SetBinPath(bin)
	n.SetNodeToken("node-tok")

	m := proto.New(proto.KindUpdate)
	m.CmdID = "c1"
	m.URL = srv.URL
	m.Version = "9.9.9"
	rep, _ := n.Handle(context.Background(), m)
	if rep.Result != proto.ResUpdateFailed {
		t.Fatalf("Result = %q, хочу NODE_UPDATE_FAILED", rep.Result)
	}
	if got, _ := os.ReadFile(bin); string(got) != "OLD-BIN" {
		t.Errorf("бинарник изменился после неудачного selftest: %q", got)
	}
	if _, err := os.Stat(bin + ".new"); !os.IsNotExist(err) {
		t.Errorf("runpilot.new не убран после неудачного selftest")
	}
}

// TestUpdateSuccess — selftest прошёл → бинарник заменён новым (14.2).
func TestUpdateSuccess(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "runpilot")
	if err := os.WriteFile(bin, []byte("OLD-BIN"), 0o755); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("NEW-BIN"))
	}))
	defer srv.Close()
	withSelftest(t, func(_ context.Context, _ string, _ time.Duration) error { return nil })

	ex := &fakeExec{}
	n := newTestNode(t, ex)
	n.SetBinPath(bin)
	n.SetNodeToken("node-tok")

	m := proto.New(proto.KindUpdate)
	m.CmdID = "c1"
	m.URL = srv.URL
	m.Version = "9.9.9"
	rep, _ := n.Handle(context.Background(), m)
	if rep.Result != proto.ResUpdated {
		t.Fatalf("Result = %q, хочу UPDATED", rep.Result)
	}
	if got, _ := os.ReadFile(bin); string(got) != "NEW-BIN" {
		t.Errorf("бинарник не заменён: %q", got)
	}
}

// TestUpdateShamismatch — CONTROL 3-связанное: SHA-256 не сошёлся → отказ.
func TestUpdateShamismatch(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "runpilot")
	if err := os.WriteFile(bin, []byte("OLD-BIN"), 0o755); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("NEW-BIN"))
	}))
	defer srv.Close()

	ex := &fakeExec{}
	n := newTestNode(t, ex)
	n.SetBinPath(bin)
	m := proto.New(proto.KindUpdate)
	m.CmdID = "c1"
	m.URL = srv.URL
	m.SHA256 = strings.Repeat("0", 64) // неверный
	rep, _ := n.Handle(context.Background(), m)
	if rep.Result != proto.ResUpdateFailed {
		t.Fatalf("Result = %q, хочу NODE_UPDATE_FAILED (sha)", rep.Result)
	}
	if got, _ := os.ReadFile(bin); string(got) != "OLD-BIN" {
		t.Errorf("бинарник изменился при sha-mismatch: %q", got)
	}
}
