// Package store — SQLite-хранилище координатора (раздел 14 ТЗ).
//
// WAL, busy_timeout=5000, одна горутина-писатель, параллельное чтение.
// Каждый переход состояния пишется сюда до побочных эффектов.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"modernc.org/sqlite"
	sqlite3lib "modernc.org/sqlite/lib"
)

// ErrClosed — запись в закрытое хранилище.
var ErrClosed = errors.New("store: closed")

// ErrConstraint — логический конфликт (UNIQUE/NOT NULL/FK). НЕ сбой
// хранилища: SAFE_MODE не срабатывает (K6: он для SQLITE_FULL/IOERR),
// API мапит на 409/400. (W9 доп-3c: re-claim узла падал в 500, а
// UNIQUE(node_token.host) переводил координатор в SAFE_MODE на 10 с.)
var ErrConstraint = errors.New("store: constraint")

// ConstraintError — обёртка SQLite-ошибки ограничения (UNIQUE/FK/NOT NULL).
type ConstraintError struct{ Err error }

func (e *ConstraintError) Error() string { return e.Err.Error() }
func (e *ConstraintError) Unwrap() error { return e.Err }
func (e *ConstraintError) Is(target error) bool { return target == ErrConstraint }

// isConstraintErr — SQLITE_CONSTRAINT (UNIQUE/FK/NOT NULL/check).
// Code() возвращает extended-код (напр. 1555); основной = &sqliteMainCodeMask.
func isConstraintErr(err error) bool {
	var se *sqlite.Error
	if errors.As(err, &se) {
		return se.Code()&sqliteMainCodeMask == sqlite3lib.SQLITE_CONSTRAINT
	}
	return false
}

// Store — хранилище runpilot.
type Store struct {
	db  *sql.DB
	log *slog.Logger

	mu      sync.Mutex
	closed  bool
	writeCh chan writeReq
	stopped chan struct{}

	// writeErrHook — v2 (K6): вызывается при ошибке ЗАПИСИ (не логических
	// ErrNotFound/ErrClosed) — координатор переводит себя в SAFE_MODE (6.4).
	// Вызывается асинхронно (из write-loop синхронная запись = deadlock).
	writeErrHookMu sync.Mutex
	writeErrHook   func(err error)
}

// SetWriteErrorHook — хук на ошибку записи (K6). nil — отключить.
func (s *Store) SetWriteErrorHook(fn func(err error)) {
	s.writeErrHookMu.Lock()
	s.writeErrHook = fn
	s.writeErrHookMu.Unlock()
}

// fireWriteErr — алерт при ошибке записи, кроме логических (no-rows/закрыто).
func (s *Store) fireWriteErr(err error) {
	if err == nil || errors.Is(err, ErrNotFound) || errors.Is(err, ErrClosed) {
		return
	}
	s.writeErrHookMu.Lock()
	fn := s.writeErrHook
	s.writeErrHookMu.Unlock()
	if fn != nil {
		go fn(err)
	}
}

type writeReq struct {
	fn  func(tx *sql.Tx) error
	ack chan<- error
}

// Open открывает (создавая при необходимости) БД в path и применяет миграции.
// Файл создаётся с режимом 0600.
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, dbDirMode); err != nil {
			return nil, fmt.Errorf("store: каталог БД: %w", err)
		}
	}
	// Файл до первого касания SQLite — сразу с правильным режимом.
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, dbFileMode)
	if err != nil {
		return nil, fmt.Errorf("store: открыть файл: %w", err)
	}
	_ = f.Close()
	if err := os.Chmod(path, dbFileMode); err != nil {
		return nil, fmt.Errorf("store: chmod 0600: %w", err)
	}

	dsn := "file:" + path +
		"?_pragma=journal_mode(WAL)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=foreign_keys(1)" +
		"&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: sql.Open: %w", err)
	}
	db.SetMaxOpenConns(maxOpenConns)

	s := &Store{
		db:      db,
		log:     slog.Default(),
		writeCh: make(chan writeReq),
		stopped: make(chan struct{}),
	}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	go s.writeLoop()
	return s, nil
}

// DB даёт доступ для параллельного чтения.
func (s *Store) DB() *sql.DB { return s.db }

// DoWrite выполняет запись в транзакции на единственном писателе (синхронно).
func (s *Store) DoWrite(fn func(tx *sql.Tx) error) error {
	ack := make(chan error, 1)
	req := writeReq{fn: fn, ack: ack}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrClosed
	}
	s.writeCh <- req
	s.mu.Unlock()
	return <-ack
}

// QueueWrite ставит запись в очередь писателя (асинхронно; для событий и метрик).
func (s *Store) QueueWrite(fn func(tx *sql.Tx) error) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrClosed
	}
	s.writeCh <- writeReq{fn: fn}
	s.mu.Unlock()
	return nil
}

// Close дожидается опустошения очереди записей и закрывает БД.
func (s *Store) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	close(s.writeCh)
	s.mu.Unlock()
	<-s.stopped
	return s.db.Close()
}

func (s *Store) writeLoop() {
	defer close(s.stopped)
	for req := range s.writeCh {
		err := s.runWrite(req.fn)
		if req.ack != nil {
			req.ack <- err
		}
	}
}

func (s *Store) runWrite(fn func(tx *sql.Tx) error) error {
	tx, err := s.db.Begin()
	if err != nil {
		s.writeErr("begin", err)
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		s.writeErr("stmt", err)
		if isConstraintErr(err) {
			return &ConstraintError{Err: err}
		}
		return err
	}
	if err := tx.Commit(); err != nil {
		s.writeErr("commit", err)
		return err
	}
	return nil
}

// writeErr — ошибка записи: в журнал (чтобы transient-сбой был диагностируем:
// W9 доп-3b — CRIT шёл без реальной ошибки) + хук SAFE_MODE (K6).
// Логический конфликт (UNIQUE/FK) — только журнал: хранилище здорово.
func (s *Store) writeErr(stage string, err error) {
	if isConstraintErr(err) {
		s.log.Warn("store: конфликт записи", "stage", stage, "err", err.Error())
		return
	}
	s.log.Error("store: ошибка записи", "stage", stage, "err", err.Error())
	s.fireWriteErr(err)
}
