package store

import (
	"testing"
	"time"

	"runpilot/internal/model"
)

// TestExternalUpsertGone — upsert (isNew при первом), GONE при исчезновении,
// повторное появление → isActive снова.
func TestExternalUpsertGone(t *testing.T) {
	s, _ := openTest(t)
	defer s.Close()
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	p := &model.ExternalProcess{
		Host: "h", PID: 10, UID: 1, StartTime: 100, Source: "cron",
		Exe: "qwen", Flags: "--approval-mode", Target: "server:srv-01",
		Status: model.ExtActive, FirstSeen: now, LastSeen: now,
	}
	isNew, err := s.ExternalUpsert(p)
	if err != nil || !isNew {
		t.Fatalf("первый upsert: isNew=%v err=%v", isNew, err)
	}
	if isNew, _ = s.ExternalUpsert(p); isNew {
		t.Fatal("повторный upsert не должен быть isNew")
	}
	// Исчезновение → GONE.
	if err := s.ExternalMarkGone("h", map[string]bool{}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	list, _ := s.ExternalList()
	if len(list) != 1 || list[0].Status != model.ExtGone {
		t.Fatalf("после gone: %+v, хочу GONE", list)
	}
	// Повторное появление → ACTIVE.
	p.LastSeen = now.Add(2 * time.Second)
	if isNew, _ = s.ExternalUpsert(p); isNew {
		t.Fatal("повторное появление — не новый ключ")
	}
	list, _ = s.ExternalList()
	if list[0].Status != model.ExtActive {
		t.Fatalf("после появления: %s, хочу ACTIVE", list[0].Status)
	}
}

// TestIgnoreMatch — правила по host/exe/flags (пустое = любое).
func TestIgnoreMatch(t *testing.T) {
	s, _ := openTest(t)
	defer s.Close()
	now := time.Now()
	if s.IgnoreMatch("h", "qwen", "") {
		t.Fatal("без правил — не игнорится")
	}
	if _, err := s.IgnoreAdd("h", "qwen", "", now); err != nil {
		t.Fatal(err)
	}
	if !s.IgnoreMatch("h", "qwen", "--x --y") {
		t.Fatal("правило host+exe должно совпасть")
	}
	if s.IgnoreMatch("other", "qwen", "") {
		t.Fatal("другой host не совпадает")
	}
	if s.IgnoreMatch("h", "vim", "") {
		t.Fatal("другой exe не совпадает")
	}
	// flags: все флаги правила должны входить в кандидата.
	if _, err := s.IgnoreAdd("", "qwen", "--approval-mode", now); err != nil {
		t.Fatal(err)
	}
	if !s.IgnoreMatch("h2", "qwen", "--approval-mode --other") {
		t.Fatal("флаг входит в кандидата → совпадает")
	}
	if s.IgnoreMatch("h2", "qwen", "--other") {
		t.Fatal("флаг не входит в кандидата → не совпадает")
	}
}
