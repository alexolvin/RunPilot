package node

// TE-<ID> — строки раздела 7 ТЗ (CONTROL W7), которые решаются в узле:
// сканер панелей и источник внешних кодеров по цепочке родителей.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"runpilot/internal/proto"
	"runpilot/profiles"
)

// TestTE_X1 — qwen в tmux без runpilot (в т.ч. запущенный cron): панель с кодером
// без @runpilot_sid → UNMANAGED; источник определяется по цепочке родителей
// (tmux → tmux, cron → cron).
func TestTE_X1(t *testing.T) {
	prof, err := profiles.LoadEmbeddedQwen()
	if err != nil {
		t.Fatal(err)
	}
	// 1) Панель с кодером без @runpilot_sid → UNMANAGED.
	f := testScannerExec(t, false)
	panes, err := Scan(context.Background(), f, []string{"default"}, prof.Cmdline())
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]PaneInfo{}
	for _, p := range panes {
		byID[p.PaneID] = p
	}
	if !byID["%11"].Unmanaged() {
		t.Fatalf("%%11 (qwen без @runpilot_sid) должен быть UNMANAGED: %+v", byID["%11"])
	}
	if byID["%10"].Unmanaged() {
		t.Fatalf("%%10 (с @runpilot_sid) не должен быть UNMANAGED: %+v", byID["%10"])
	}
	// 2) Источник по цепочке родителей: кодер, запущенный cron → cron.
	cronTree := `  1    0    0   0 /sbin/init
900    1    0  60 /usr/sbin/cron
901  900 1000  30 /usr/local/bin/qwen --approval-mode default`
	tbl, err := processTable(context.Background(), psExec(cronTree))
	if err != nil {
		t.Fatal(err)
	}
	if src := tbl.sourceOf(901); src != sourceCron {
		t.Fatalf("источник кодера под cron: %q, хочу cron", src)
	}
}

// TestTE_C15 — каталог сессии удалён: при перезапуске/восстановлении узел
// проверяет каталог (validateDir) и каталог больше не существует → отказ
// DIR_NOT_FOUND (оператору предлагается выбрать другой каталог).
func TestTE_C15(t *testing.T) {
	root := t.TempDir()
	n := newTestNode(t, &fakeExec{})
	n.SetProjectRoots([]string{root})

	sessDir := filepath.Join(root, "proj")
	if err := os.Mkdir(sessDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Живой каталог внутри project_roots → OK (пустая строка).
	if code := n.validateDir(sessDir); code != "" {
		t.Fatalf("живой каталог: %q, хочу OK (пусто)", code)
	}
	// Каталог сессии удалён → DIR_NOT_FOUND.
	if err := os.RemoveAll(sessDir); err != nil {
		t.Fatal(err)
	}
	if code := n.validateDir(sessDir); code != proto.ResDirNotFound {
		t.Fatalf("удалённый каталог: %q, хочу DIR_NOT_FOUND", code)
	}
}

// TestTE_N5 — новый бинарник не прошёл самопроверку (runpilot.new selftest ≠ 0):
// замена НЕ выполняется, текущий бинарник остаётся прежним, runpilot.new убран,
// результат NODE_UPDATE_FAILED (повтор обновления — через
// enroll.update_retry_min по расписанию координатора).
func TestTE_N5(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "runpilot")
	if err := os.WriteFile(bin, []byte("OLD-BIN"), 0o755); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("NEW-BIN"))
	}))
	defer srv.Close()

	withSelftest(t, func(_ context.Context, _ string, _ time.Duration) error {
		return errors.New("selftest ≠ 0")
	})

	n := newTestNode(t, &fakeExec{})
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
		t.Errorf("бинарник заменили после неудачного selftest: %q", got)
	}
	if _, err := os.Stat(bin + ".new"); !os.IsNotExist(err) {
		t.Errorf("runpilot.new не убран после неудачного selftest")
	}
}
