package store

// Повторная регистрация узла (W9 доп-3c): re-claim нового токена для host,
// у которого уже есть активный токен, не должен падать в UNIQUE(node_token.host)
// (а за ним — в 500 + ложный SAFE_MODE). Старый активный токен отзывается,
// новый привязывается; повтор тем же токеном — идемпотентен.

import (
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestBindTokenHostReEnrollReplacesOld(t *testing.T) {
	s, _ := openTest(t)
	now := time.Now().UTC()

	// Токен A создаётся (host=""), привязывается к node-c.
	tokA := NewNodeToken("", now)
	if err := s.CreateNodeToken(tokA); err != nil {
		t.Fatalf("CreateNodeToken A: %v", err)
	}
	if err := s.BindTokenHost(tokA.Token, "node-c", now); err != nil {
		t.Fatalf("claim A: %v", err)
	}
	// Токен B создаётся позже (host="" — другой host, конфликта нет).
	tokB := NewNodeToken("", now)
	if err := s.CreateNodeToken(tokB); err != nil {
		t.Fatalf("CreateNodeToken B: %v", err)
	}
	if nt, err := s.GetNodeToken("node-c"); err != nil || nt.Token != tokA.Token {
		t.Fatalf("host не привязан к A: %v %v", nt, err)
	}

	// Re-enroll: новый токен B для того же host. ДО фикса — UNIQUE → 500.
	if err := s.BindTokenHost(tokB.Token, "node-c", now); err != nil {
		t.Fatalf("re-claim B: %v", err)
	}
	if nt, err := s.GetNodeToken("node-c"); err != nil || nt.Token != tokB.Token {
		t.Fatalf("host должен быть привязан к B: %v %v", nt, err)
	}
	// Старый токен A отозван.
	if host, ok := s.VerifyNodeToken(tokA.Token); ok {
		t.Fatalf("токен A должен быть отозван, но валиден (host=%q)", host)
	}

	// Повтор тем же токеном и host — идемпотентен.
	if err := s.BindTokenHost(tokB.Token, "node-c", now); err != nil {
		t.Fatalf("повторный claim B: %v", err)
	}
	if nt, err := s.GetNodeToken("node-c"); err != nil || nt.Token != tokB.Token {
		t.Fatalf("после повтора host должен быть у B: %v %v", nt, err)
	}
}

func TestBindTokenHostRevokedFails(t *testing.T) {
	s, _ := openTest(t)
	now := time.Now().UTC()
	tok := NewNodeToken("", now)
	if err := s.CreateNodeToken(tok); err != nil {
		t.Fatalf("CreateNodeToken: %v", err)
	}
	if err := s.RevokeNodeTokenByValue(tok.Token, now); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if err := s.BindTokenHost(tok.Token, "h", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("отозванный токен: err=%v, хочу ErrNotFound", err)
	}
}

// K6: логический конфликт (UNIQUE) не алертит хук SAFE_MODE — только
// ошибки хранилища (SQLITE_FULL/IOERR).
func TestConstraintDoesNotTripWriteErrHook(t *testing.T) {
	s, _ := openTest(t)
	now := time.Now().UTC()
	fired := make(chan struct{}, 1)
	s.SetWriteErrorHook(func(error) { fired <- struct{}{} })

	// Прямое нарушение PRIMARY KEY(host): вставка второго токена на host.
	tok := NewNodeToken("h1", now)
	if err := s.CreateNodeToken(tok); err != nil {
		t.Fatalf("CreateNodeToken: %v", err)
	}
	dup := NewNodeToken("h1", now)
	err := s.DoWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO node_token (host, token, created_at, revoked_at)
			VALUES (?, ?, ?, '')`, "h1", dup.Token, ts(now))
		return err
	})
	if err == nil {
		t.Fatal("ожидал UNIQUE-ошибку")
	}
	if !errors.Is(err, ErrConstraint) {
		t.Fatalf("err=%v, хочу ErrConstraint", err)
	}
	select {
	case <-fired:
		t.Fatal("UNIQUE сработал хук SAFE_MODE, а не должен")
	default:
	}
}
