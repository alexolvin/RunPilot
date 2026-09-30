package web

import (
	"context"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"
)

// W1 CONTROL: привязка coordinator.bind недоступна N попыток → повтор с
// интервалом; после появления адреса слушатель получен не позже интервала
// (bind_retry_sec). Веб-слушатель при этом работает (отдельный адрес).
func TestListenWithRetry(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	const interval = 50 * time.Millisecond

	done := make(chan net.Listener, 1)
	go func() {
		l, err := ListenWithRetry(ctx, addr, interval, log)
		if err != nil {
			return
		}
		done <- l
	}()

	// Пока адрес занят — повтор привязки, слушатель не получен.
	select {
	case l := <-done:
		_ = l.Close()
		t.Fatalf("получил слушатель, пока адрес занят")
	case <-time.After(150 * time.Millisecond):
	}

	// Освобождаем адрес — ListenWithRetry вернётся не позже интервала.
	_ = ln.Close()
	select {
	case l := <-done:
		_ = l.Close()
	case <-time.After(interval * 5):
		t.Fatalf("ListenWithRetry не вернулся после освобождения адреса за 5×interval")
	}
}

// Отмена ctx останавливает повтор (нет висящей горутины).
func TestListenWithRetryCancelled(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	addr := ln.Addr().String()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		_, err := ListenWithRetry(ctx, addr, 50*time.Millisecond, log)
		errCh <- err
	}()
	time.Sleep(120 * time.Millisecond)
	cancel()
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatalf("ожидается ошибка (ctx), а не слушатель")
		}
	case <-time.After(time.Second):
		t.Fatalf("ListenWithRetry не завершился после отмены ctx")
	}
}
