package store

// Повторная регистрация узла (W9 доп-3c): re-claim нового токена для узла
// (host, os_user), у которого уже есть активный токен, не должен падать в
// UNIQUE(node_token) (а за ним — в 500 + ложный SAFE_MODE). Старый активный
// токен узла отзывается, новый привязывается; повтор тем же токеном —
// идемпотентен. Другой узел на том же host (другой os_user) не затрагивается.

import (
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestBindTokenHostReEnrollReplacesOld(t *testing.T) {
	s, _ := openTest(t)
	now := time.Now().UTC()

	// Токен A создаётся (host=""), привязывается к узлу node-c.
	tokA := NewNodeToken("", "", now)
	if err := s.CreateNodeToken(tokA); err != nil {
		t.Fatalf("CreateNodeToken A: %v", err)
	}
	if err := s.BindTokenHost(tokA.Token, "node-c", "", now); err != nil {
		t.Fatalf("claim A: %v", err)
	}
	// Токен B создаётся позже (host="" — другой узел, конфликта нет).
	tokB := NewNodeToken("", "", now)
	if err := s.CreateNodeToken(tokB); err != nil {
		t.Fatalf("CreateNodeToken B: %v", err)
	}
	if nt, err := s.GetNodeToken("node-c", ""); err != nil || nt.Token != tokA.Token {
		t.Fatalf("узел не привязан к A: %v %v", nt, err)
	}

	// Re-enroll: новый токен B для того же узла. ДО фикса — UNIQUE → 500.
	if err := s.BindTokenHost(tokB.Token, "node-c", "", now); err != nil {
		t.Fatalf("re-claim B: %v", err)
	}
	if nt, err := s.GetNodeToken("node-c", ""); err != nil || nt.Token != tokB.Token {
		t.Fatalf("узел должен быть привязан к B: %v %v", nt, err)
	}
	// Старый токен A отозван.
	if host, _, ok := s.VerifyNodeToken(tokA.Token); ok {
		t.Fatalf("токен A должен быть отозван, но валиден (host=%q)", host)
	}

	// Повтор тем же токеном и узлом — идемпотентен.
	if err := s.BindTokenHost(tokB.Token, "node-c", "", now); err != nil {
		t.Fatalf("повторный claim B: %v", err)
	}
	if nt, err := s.GetNodeToken("node-c", ""); err != nil || nt.Token != tokB.Token {
		t.Fatalf("после повтора узел должен быть у B: %v %v", nt, err)
	}
}

// DEFEKT (2026-10-21, инцидент vzu5-big): node_token был ключён по host, а
// идентичность узла — host:os_user. Два узла на одном host (разные os_user)
// отзывают токены друг друга: claim второго (re-enroll ветка BindTokenHost)
// отзывает активный токен первого. Факт: claim admin (2026-10-20) отозвал
// токен botagent → botagent перестал подключаться → power=None у vzu5.
//
// ФИКС: токен ключится по (host, os_user). Claim h:bob (tokB) не затрагивает
// токен h:alice (tokA) — это другой узел (другой os_user). Токен A остаётся
// валидным.
func TestBindTokenHostTwoOsUsersSameHostRevokeEachOther(t *testing.T) {
	s, _ := openTest(t)
	now := time.Now().UTC()

	// Узел h:alice — токен A, привязан к (h, alice).
	tokA := NewNodeToken("", "", now)
	if err := s.CreateNodeToken(tokA); err != nil {
		t.Fatalf("CreateNodeToken A: %v", err)
	}
	if err := s.BindTokenHost(tokA.Token, "h", "alice", now); err != nil {
		t.Fatalf("claim A (h:alice): %v", err)
	}
	if _, _, ok := s.VerifyNodeToken(tokA.Token); !ok {
		t.Fatal("токен A (h:alice) должен быть валиден после claim")
	}

	// Узел h:bob — токен B, привязан к (h, bob) — тот же host, другой os_user.
	tokB := NewNodeToken("", "", now)
	if err := s.CreateNodeToken(tokB); err != nil {
		t.Fatalf("CreateNodeToken B: %v", err)
	}
	if err := s.BindTokenHost(tokB.Token, "h", "bob", now); err != nil {
		t.Fatalf("claim B (h:bob): %v", err)
	}

	// ФИКС: claim h:bob НЕ отзывает токен h:alice (другой os_user).
	if _, _, ok := s.VerifyNodeToken(tokA.Token); !ok {
		t.Fatal("фикс не сработал: токен A (h:alice) отозван claim'ом h:bob")
	}
	// Оба токена валидны, привязаны к своим узлам.
	if host, osUser, ok := s.VerifyNodeToken(tokB.Token); !ok || host != "h" || osUser != "bob" {
		t.Fatalf("токен B (h:bob): host=%q os_user=%q ok=%v", host, osUser, ok)
	}
}

func TestBindTokenHostRevokedFails(t *testing.T) {
	s, _ := openTest(t)
	now := time.Now().UTC()
	tok := NewNodeToken("", "", now)
	if err := s.CreateNodeToken(tok); err != nil {
		t.Fatalf("CreateNodeToken: %v", err)
	}
	if err := s.RevokeNodeTokenByValue(tok.Token, now); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if err := s.BindTokenHost(tok.Token, "h", "", now); !errors.Is(err, ErrNotFound) {
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

	// Прямое нарушение PRIMARY KEY (host, os_user): вставка второго токена
	// на тот же узел (host, os_user).
	tok := NewNodeToken("h1", "", now)
	if err := s.CreateNodeToken(tok); err != nil {
		t.Fatalf("CreateNodeToken: %v", err)
	}
	dup := NewNodeToken("h1", "", now)
	err := s.DoWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO node_token (host, os_user, token, created_at, revoked_at)
			VALUES (?, ?, ?, ?, '')`, "h1", "", dup.Token, ts(now))
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
