package clock

import (
	"sort"
	"sync"
	"time"
)

// vtimer — отложенный одноразовый таймер виртуальных часов.
type vtimer struct {
	deadline time.Time
	fn       func()
}

// Virtual — детерминированные часы для тестов автоматов и планировщика.
type Virtual struct {
	mu     sync.Mutex
	t      time.Time
	timers []vtimer
}

// NewVirtual создаёт виртуальные часы, начатые в start.
func NewVirtual(start time.Time) *Virtual { return &Virtual{t: start.UTC()} }

// Now возвращает текущее виртуальное время.
func (v *Virtual) Now() time.Time {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.t
}

// AfterFunc ставит одноразовый таймер: fn вызывается, когда виртуальное
// время дойдёт до Now()+d (при Advance/Set). Детерминировано — без реального
// времени (строгое правило ТЗ: time.Now запрещён в планировщике).
func (v *Virtual) AfterFunc(d time.Duration, fn func()) {
	v.mu.Lock()
	v.timers = append(v.timers, vtimer{deadline: v.t.Add(d), fn: fn})
	v.mu.Unlock()
}

// fireDue — запустить (вне lock) все таймеры с дедлайном <= текущего времени,
// по возрастанию дедлайна. Новые таймеры, добавленные в fn, сработают при
// следующем Advance/Set (защита от бесконечного цикла).
func (v *Virtual) fireDue() {
	v.mu.Lock()
	var due, rest []vtimer
	for _, tm := range v.timers {
		if !tm.deadline.After(v.t) {
			due = append(due, tm)
		} else {
			rest = append(rest, tm)
		}
	}
	v.timers = rest
	v.mu.Unlock()

	sort.Slice(due, func(i, j int) bool { return due[i].deadline.Before(due[j].deadline) })
	for _, tm := range due {
		tm.fn()
	}
}

// Advance сдвигает время вперёд на d и срабатывает наступившие таймеры.
func (v *Virtual) Advance(d time.Duration) time.Time {
	v.mu.Lock()
	v.t = v.t.Add(d)
	v.mu.Unlock()
	v.fireDue()
	return v.t
}

// Set ставит время явно и срабатывает наступившие таймеры.
func (v *Virtual) Set(t time.Time) time.Time {
	v.mu.Lock()
	v.t = t.UTC()
	v.mu.Unlock()
	v.fireDue()
	return v.t
}
