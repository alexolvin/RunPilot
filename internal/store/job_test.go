package store

// Раздел 8.1 ТЗ: таблица job_heartbeat (пульс runpilot exec, X5).

import (
	"testing"
	"time"
)

func TestJobHeartbeat(t *testing.T) {
	s, _ := openTest(t)
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)

	// Без пульса — записи нет.
	if _, ok := s.JobBeatTime("J1"); ok {
		t.Fatalf("JobBeatTime: есть запись до первого пульса")
	}
	// Первый пульс создаёт запись.
	if err := s.JobBeat("J1", now); err != nil {
		t.Fatal(err)
	}
	at, ok := s.JobBeatTime("J1")
	if !ok || !at.Equal(now) {
		t.Fatalf("JobBeatTime = %v %v, хочу %v", at, ok, now)
	}
	// Обновление.
	beat2 := now.Add(time.Second)
	if err := s.JobBeat("J1", beat2); err != nil {
		t.Fatal(err)
	}
	if at, _ = s.JobBeatTime("J1"); !at.Equal(beat2) {
		t.Fatalf("после обновления last_beat = %v, хочу %v", at, beat2)
	}

	// Сталeness: cutoff между now и beat2.
	cutoff := now.Add(500 * time.Millisecond)
	stale, err := s.JobStale(cutoff)
	if err != nil {
		t.Fatal(err)
	}
	// beat2 > cutoff → не stale.
	if len(stale) != 0 {
		t.Fatalf("JobStale(cutoff<beat2) = %v, хочу пусто", stale)
	}
	// cutoff > beat2 → stale.
	stale, err = s.JobStale(now.Add(2 * time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) != 1 || stale[0] != "J1" {
		t.Fatalf("JobStale(cutoff>beat2) = %v, хочу [J1]", stale)
	}

	// cancel-флаг.
	if s.JobCancel("J1") {
		t.Fatalf("JobCancel до set = true")
	}
	if err := s.JobSetCancel("J1", true); err != nil {
		t.Fatal(err)
	}
	if !s.JobCancel("J1") {
		t.Fatalf("JobCancel после set = false")
	}
	// Удаление.
	if err := s.JobDelete("J1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.JobBeatTime("J1"); ok {
		t.Fatalf("запись не удалена")
	}
}
