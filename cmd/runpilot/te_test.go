package main

// TE-<ID> — строки раздела 7 ТЗ (CONTROL W7), которые решаются в CLI runpilot.

import (
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"runpilot/internal/config"
)

// TestTE_X4 — cron запускает `runpilot run`, координатор недоступен: клиент
// классифицирует ошибку как errNoCoordinator → код выхода 69, qwen не
// запускается (X4 ТЗ). Реальный exit-код бинарника проверяет живой e2e.
func TestTE_X4(t *testing.T) {
	cfg = config.Defaults()
	cfg.Client.Coordinator = "http://127.0.0.1:1" // закрытый порт
	_, _, _, err := clientDo("POST", "/api/v1/run", nil, nil)
	if err == nil || !errors.Is(err, errNoCoordinator) {
		t.Fatalf("ошибка: %v, хочу errNoCoordinator (координатор недоступен)", err)
	}
	if code := jobExitCode(err); code != exitNoCoordinator {
		t.Fatalf("код выхода: %d, хочу 69", code)
	}
}

// TestTE_K1 — штатный рестарт (SIGTERM): shutdownServices закрывает все
// HTTP-слушатели координатора в пределах grace; каждый Serve завершается
// штатно (http.ErrServerClosed), активные запросы дочитываются.
func TestTE_K1(t *testing.T) {
	mk := func() (httpService, <-chan error) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		srv := &http.Server{Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})}
		errCh := make(chan error, 1)
		go func() { errCh <- srv.Serve(ln) }()
		return httpService{name: "s", ln: ln, srv: srv}, errCh
	}
	a, aErr := mk()
	b, bErr := mk()
	shutdownServices([]httpService{a, b}, 2*time.Second,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	for name, ch := range map[string]<-chan error{"a": aErr, "b": bErr} {
		select {
		case err := <-ch:
			if err != http.ErrServerClosed {
				t.Fatalf("сервер %s: %v, хочу http.ErrServerClosed (штатная остановка)", name, err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("сервер %s не остановлен штатно", name)
		}
	}
}

// TestTE_U1 — новая версия координатора (`runpilot upgrade`): selftest нового
// бинарника пройден → текущий сохранён как runpilot.prev (откат 6.6), новый
// установлен, бинарники целей скопированы в coordinator.dist_dir (узлы
// скачают сами). Перезапуск делает менеджер службы.
func TestTE_U1(t *testing.T) {
	dir := t.TempDir()
	dist := filepath.Join(dir, "dist")
	if err := os.MkdirAll(dist, 0o755); err != nil {
		t.Fatal(err)
	}
	// Новый бинарник в dist: selftest завершается 0.
	newContent := "#!/bin/sh\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dist, "runpilot"), []byte(newContent), 0o755); err != nil {
		t.Fatal(err)
	}
	// Бинарник-цель для узлов (копируется в dist_dir).
	if err := os.WriteFile(filepath.Join(dist, "runpilot-node"), []byte("NODE-BIN"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Текущий бинарник координатора (подставной).
	cur := filepath.Join(dir, "runpilot")
	const curBin = "CURRENT-BIN"
	if err := os.WriteFile(cur, []byte(curBin), 0o755); err != nil {
		t.Fatal(err)
	}
	d := filepath.Join(dir, "distdir")

	out, err := runUpgrade(dist, cur, d)
	if err != nil {
		t.Fatalf("runUpgrade: %v", err)
	}
	// Текущий сохранён как runpilot.prev (откат).
	if b, _ := os.ReadFile(runpilotPrevNameIn(dir)); string(b) != curBin {
		t.Fatalf("runpilot.prev = %q, хочу %q (откат сохранён)", b, curBin)
	}
	// Новый установлен поверх cur.
	if b, _ := os.ReadFile(cur); string(b) != newContent {
		t.Fatalf("cur = %q, хочу новый бинарник (установлен)", b)
	}
	// Бинарник цели скопирован в dist_dir (узлы скачают сами).
	if b, rerr := os.ReadFile(filepath.Join(d, "runpilot-node")); rerr != nil || string(b) != "NODE-BIN" {
		t.Fatalf("dist_dir/runpilot-node: %q err=%v, хочу NODE-BIN (для узлов)", b, rerr)
	}
	if _, ok := out["dist_targets"]; !ok {
		t.Fatal("нет dist_targets в результате upgrade")
	}
}
