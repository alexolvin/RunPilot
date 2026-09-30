package clock

import (
	"testing"
	"time"
)

func TestVirtualAdvance(t *testing.T) {
	start := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	v := NewVirtual(start)
	if got := v.Now(); !got.Equal(start) {
		t.Fatalf("Now() = %v, want %v", got, start)
	}
	v.Advance(90 * time.Second)
	want := start.Add(90 * time.Second)
	if got := v.Now(); !got.Equal(want) {
		t.Fatalf("Now() after Advance = %v, want %v", got, want)
	}
	v.Set(start)
	if got := v.Now(); !got.Equal(start) {
		t.Fatalf("Now() after Set = %v, want %v", got, start)
	}
}

func TestRealUTC(t *testing.T) {
	r := NewReal()
	now := r.Now()
	if now.Location() != time.UTC {
		t.Fatalf("Real.Now() не в UTC: %v", now)
	}
}
