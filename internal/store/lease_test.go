package store

import (
	"errors"
	"testing"
	"time"

	"runpilot/internal/model"
)

// п.1: инвариант слотов. Выдача НОВОЙ аренды при занятом слоте → ErrSlotFull.
// Все пути выдачи (планировщик, acquireNow, миграция) сходятся в
// LeaseCreateIfAbsent, поэтому проверка инварианта — здесь.
func TestLeaseCreateIfAbsentSlotFullRejects(t *testing.T) {
	s, _ := openTest(t)
	now := time.Now().UTC()

	// 1-слотовый сервер «a»: первая сессия занимает единственный слот.
	if _, err := s.LeaseCreateIfAbsent(model.Lease{
		SID: "s1", Server: "a", Slot: 1, State: model.LeasePending,
		Origin: model.LeaseOriginDispatch, GrantedAt: now,
	}, 1, false); err != nil {
		t.Fatalf("первая аренда: %v", err)
	}

	// Вторая сессия на тот же сервер → отказ (слот занят).
	_, err := s.LeaseCreateIfAbsent(model.Lease{
		SID: "s2", Server: "a", Slot: 1, State: model.LeasePending,
		Origin: model.LeaseOriginDispatch, GrantedAt: now,
	}, 1, false)
	if !errors.Is(err, ErrSlotFull) {
		t.Fatalf("ожидали ErrSlotFull, получили %v", err)
	}

	// В БД ровно одна действующая аренда — двойной аренды нет.
	n, err := s.LeaseCountByServer("a")
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("действующих аренд = %d, ждём 1", n)
	}
}

// п.1: у сессии, у которой ЕСТЬ действующая аренда, повторная «выдача»
// возвращает существующую (if-absent) — слот не пересчитывается, ошибки нет.
func TestLeaseCreateIfAbsentReturnsExisting(t *testing.T) {
	s, _ := openTest(t)
	now := time.Now().UTC()

	l1, err := s.LeaseCreateIfAbsent(model.Lease{
		SID: "s1", Server: "a", Slot: 1, State: model.LeasePending,
		Origin: model.LeaseOriginDispatch, GrantedAt: now,
	}, 1, false)
	if err != nil {
		t.Fatalf("аренда: %v", err)
	}
	// Тот же sid, 1-слотовый сервер занят → не ErrSlotFull, а та же аренда.
	l2, err := s.LeaseCreateIfAbsent(model.Lease{
		SID: "s1", Server: "a", Slot: 1, State: model.LeaseActive,
		Origin: model.LeaseOriginImplicit, GrantedAt: now,
	}, 1, false)
	if err != nil {
		t.Fatalf("повторная выдача (if-absent): %v", err)
	}
	if l1.ID != l2.ID {
		t.Fatalf("ожидали ту же аренду, id %d != %d", l1.ID, l2.ID)
	}
}

// п.1: RECOVERING занимает слот — инвариант считает PENDING/ACTIVE/RECOVERING.
func TestLeaseCreateIfAbsentRecoveringOccupiesSlot(t *testing.T) {
	s, _ := openTest(t)
	now := time.Now().UTC()

	if _, err := s.LeaseCreate(model.Lease{
		SID: "s1", Server: "a", Slot: 1, State: model.LeaseRecovering,
		Origin: model.LeaseOriginMigrated, GrantedAt: now,
	}); err != nil {
		t.Fatalf("RECOVERING-аренда: %v", err)
	}
	_, err := s.LeaseCreateIfAbsent(model.Lease{
		SID: "s2", Server: "a", Slot: 1, State: model.LeasePending,
		Origin: model.LeaseOriginDispatch, GrantedAt: now,
	}, 1, false)
	if !errors.Is(err, ErrSlotFull) {
		t.Fatalf("RECOVERING должен занимать слот: ожидали ErrSlotFull, получили %v", err)
	}
}

// п.1: подтверждение старта (PENDING→ACTIVE) для RELEASED-аренды → отказ.
// RELEASED никогда не переводится в ACTIVE.
func TestLeaseSetActiveReleasedRejected(t *testing.T) {
	s, _ := openTest(t)
	now := time.Now().UTC()

	if _, err := s.LeaseCreate(model.Lease{
		SID: "s1", Server: "a", Slot: 1, State: model.LeasePending,
		Origin: model.LeaseOriginDispatch, GrantedAt: now,
	}); err != nil {
		t.Fatalf("аренда: %v", err)
	}
	// Освобождаем → RELEASED.
	if err := s.LeaseRelease("s1", "test", now); err != nil {
		t.Fatalf("release: %v", err)
	}
	// Подтверждение старта для RELEASED-аренды → отказ.
	if err := s.LeaseSetActive("s1"); err == nil {
		t.Fatal("подтверждение RELEASED-аренды прошло, а не должно")
	}
	// Действующей аренды нет (она RELEASED).
	if _, err := s.LeaseGet("s1"); err != ErrNotFound {
		t.Fatalf("после RELEASED аренды не должно быть, err = %v", err)
	}
}

// п.1: подтверждение старта для действующей PENDING-аренды проходит и
// переводит её в ACTIVE (свежий сценарий — не нарушен инвариантом).
func TestLeaseSetActivePendingOK(t *testing.T) {
	s, _ := openTest(t)
	now := time.Now().UTC()

	if _, err := s.LeaseCreate(model.Lease{
		SID: "s1", Server: "a", Slot: 1, State: model.LeasePending,
		Origin: model.LeaseOriginDispatch, GrantedAt: now,
	}); err != nil {
		t.Fatalf("аренда: %v", err)
	}
	if err := s.LeaseSetActive("s1"); err != nil {
		t.Fatalf("подтверждение PENDING: %v", err)
	}
	l, err := s.LeaseGet("s1")
	if err != nil {
		t.Fatalf("lease: %v", err)
	}
	if l.State != model.LeaseActive {
		t.Fatalf("state = %s, ждём ACTIVE", l.State)
	}
}
