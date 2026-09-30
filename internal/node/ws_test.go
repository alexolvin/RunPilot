package node

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"runpilot/internal/clock"
	"runpilot/internal/proto"

	"github.com/gorilla/websocket"
)

// wsTestLog — discard-лог для тестов WS-клиента.
var wsTestLog = slog.New(slog.NewTextHandler(io.Discard, nil))

// TestWSClientSelfUpdateExits — 14.2 ТЗ: успешный self-update → бинарник
// заменён, Run() возвращает ErrUpdated (процесс завершится кодом 75,
// systemd перезапустит новый бинарник). Без ErrUpdated узел бесконечно
// переподключался бы старым кодом (defect, найден на W9).
func TestWSClientSelfUpdateExits(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "runpilot")
	if err := os.WriteFile(bin, []byte("OLD-BIN"), 0o755); err != nil {
		t.Fatal(err)
	}
	newContent := []byte("NEW-BIN")
	sum := sha256.Sum256(newContent)

	// Дистрибутив: координатор отдаёт новый бинарник.
	dist := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer node-tok" {
			t.Errorf("download без Bearer-токена узла: %q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write(newContent)
	}))
	defer dist.Close()

	// Координатор (WS): hello + panes → команда update → ждём ответ UPDATED.
	updated := make(chan string, 1)
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		for i := 0; i < 2; i++ { // hello, panes
			if _, _, err := c.ReadMessage(); err != nil {
				return
			}
		}
		m := proto.New(proto.KindUpdate)
		m.CmdID = "u1"
		m.Version = "9.9.9"
		m.URL = dist.URL
		m.SHA256 = hex.EncodeToString(sum[:])
		if err := c.WriteJSON(m); err != nil {
			return
		}
		// Ответ на update придёт не сразу: до него идут node_health и
		// снимки панелей от цикла узла — читаем до KindReply с CmdID "u1".
		for {
			var rep proto.Msg
			if err := c.ReadJSON(&rep); err != nil {
				return
			}
			if rep.Type == proto.KindReply && rep.CmdID == "u1" {
				updated <- rep.Result
				return
			}
		}
	}))
	defer srv.Close()

	withSelftest(t, func(_ context.Context, _ string, _ time.Duration) error { return nil })

	ex := &fakeExec{}
	n := newTestNode(t, ex)
	n.SetBinPath(bin)
	n.SetNodeToken("node-tok")

	ws := NewWSClient("ws"+srv.URL[len("http"):], "tok", "old", clock.NewReal(), wsTestLog, n)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- ws.Run(ctx) }()

	select {
	case res := <-updated:
		if res != proto.ResUpdated {
			t.Fatalf("ответ координатору = %q, хочу UPDATED", res)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("координатор не получил ответ UPDATED за 10 с")
	}

	select {
	case err := <-errCh:
		if !errors.Is(err, ErrUpdated) {
			t.Fatalf("Run() = %v, хочу ErrUpdated", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run() не вернулся после успешного self-update")
	}

	if got, _ := os.ReadFile(bin); string(got) != "NEW-BIN" {
		t.Errorf("бинарник не заменён: %q", got)
	}
}
