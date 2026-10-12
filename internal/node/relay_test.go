package node

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"runpilot/internal/clock"
	"runpilot/internal/detect"
	"runpilot/internal/proto"
)

// seedPaneSID — managed-панель с SID и Dir (для turn_report/read_report_file).
func seedPaneSID(t *testing.T, n *Node, paneID, sid, dir, socket string) {
	t.Helper()
	n.snap.sync(PaneInfo{PaneID: paneID, SID: sid, Dir: dir, Socket: socket},
		detect.State(0), 0, "", clock.NewReal().Now())
}

// TestReadReportFileGuards — проверки раздела 6.2 (CONTROL R0).
func TestReadReportFileGuards(t *testing.T) {
	sessionDir := t.TempDir()
	outside := t.TempDir()

	mdIn := filepath.Join(sessionDir, "a.md")
	if err := os.WriteFile(mdIn, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	txtIn := filepath.Join(sessionDir, "b.txt")
	if err := os.WriteFile(txtIn, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	mdOut := filepath.Join(outside, "c.md")
	if err := os.WriteFile(mdOut, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Симлинк внутри каталога → .md вне (PATH_FORBIDDEN через симлинк).
	link := filepath.Join(sessionDir, "link.md")
	if err := os.Symlink(mdOut, link); err != nil {
		t.Fatal(err)
	}
	// Сверх лимита (attach_max_kb=512 по умолчанию → >512 КБ).
	big := filepath.Join(sessionDir, "big.md")
	if err := os.WriteFile(big, make([]byte, 513*1024), 0o600); err != nil {
		t.Fatal(err)
	}

	ex := &fakeExec{}
	n := newTestNode(t, ex)
	seedPaneSID(t, n, "%5", "SID1", sessionDir, "default")

	cases := []struct {
		name string
		path string
		want string
	}{
		{"ok", mdIn, ResSent},
		{"outside", mdOut, proto.ResPathForbidden},
		{"symlink-outside", link, proto.ResPathForbidden},
		{"not-md", txtIn, proto.ResExtForbidden},
		{"too-large", big, proto.ResTooLarge},
		{"missing", filepath.Join(sessionDir, "nope.md"), proto.ResPathForbidden},
	}
	for _, c := range cases {
		m := proto.New(proto.KindReadReportFile)
		m.CmdID = "c1"
		m.SID = "SID1"
		m.Path = c.path
		rep, err := n.readReportFile(context.Background(), m)
		if err != nil {
			t.Fatalf("%s: err = %v", c.name, err)
		}
		if rep.Result != c.want {
			t.Errorf("%s: result = %q, хочу %q", c.name, rep.Result, c.want)
		}
	}
	// Содержимое корректного файла.
	m := proto.New(proto.KindReadReportFile)
	m.SID = "SID1"
	m.Path = mdIn
	rep, _ := n.readReportFile(context.Background(), m)
	if rep.Detail != "hello" {
		t.Fatalf("detail = %q, хочу %q", rep.Detail, "hello")
	}
}

// TestTurnReportRecording — turn_report из записи (CONTROL R0).
func TestTurnReportRecording(t *testing.T) {
	sessionDir := t.TempDir()
	// Запись в $QWEN_HOME/projects/<slug>/chats/<uuid>.jsonl.
	uuid := "11111111-1111-1111-1111-111111111111"
	slug := strings.ReplaceAll(sessionDir, "/", "-")
	chatDir := filepath.Join(t.TempDir(), "projects", slug, "chats")
	if err := os.MkdirAll(chatDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := filepath.Join(chatDir, uuid+".jsonl")
	lines := []string{
		`{"type":"user","uuid":"u1","message":{"role":"user","parts":[{"text":"задача"}]}}`,
		`{"type":"assistant","uuid":"a1","message":{"role":"model","parts":[{"text":"Готово, отчёт в step1.md"}]}}`,
	}
	if err := os.WriteFile(rec, []byte(joinLines(lines)), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("QWEN_HOME", filepath.Dir(filepath.Dir(filepath.Dir(chatDir))))

	ex := &fakeExec{}
	n := newTestNode(t, ex)
	seedPaneSID(t, n, "%5", "SID1", sessionDir, "default")

	m := proto.New(proto.KindTurnReport)
	m.CmdID = "c1"
	m.SID = "SID1"
	m.QwenSessionID = uuid
	rep, err := n.turnReport(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Result != ResSent {
		t.Fatalf("result = %q", rep.Result)
	}
	if rep.TurnReport == nil || !rep.TurnReport.Found {
		t.Fatalf("turn_report = %+v", rep.TurnReport)
	}
	if rep.TurnReport.Report != "Готово, отчёт в step1.md" {
		t.Fatalf("report = %q", rep.TurnReport.Report)
	}
	if rep.TurnReport.Source != "recording" {
		t.Fatalf("source = %q", rep.TurnReport.Source)
	}
}

func joinLines(lines []string) string {
	out := ""
	for i, l := range lines {
		if i > 0 {
			out += "\n"
		}
		out += l
	}
	return out + "\n"
}
