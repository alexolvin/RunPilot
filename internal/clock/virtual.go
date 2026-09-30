package clock

import (
	"sync"
	"time"
)

// Virtual — детерминированные часы для тестов автоматов и планировщика.
type Virtual struct {
	mu sync.Mutex
	t  time.Time
}

// NewVirtual создаёт виртуальные часы, начатые в start.
func NewVirtual(start time.Time) *Virtual { return &Virtual{t: start.UTC()} }

// Now возвращает текущее виртуальное время.
func (v *Virtual) Now() time.Time {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.t
}

// Advance сдвигает время вперёд на d.
func (v *Virtual) Advance(d time.Duration) time.Time {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.t = v.t.Add(d)
	return v.t
}

// Set ставит время явно.
func (v *Virtual) Set(t time.Time) time.Time {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.t = t.UTC()
	return v.t
}
