// Package clock — единственный источник времени для планировщика,
// автоматов и таймаутов (строгое правило ТЗ: time.Now в этих путях запрещён).
package clock

import (
	"time"
)

// Clock возвращает «сейчас» в UTC и ставит одноразовые таймеры.
type Clock interface {
	Now() time.Time
	// AfterFunc ставит одноразовый таймер: по прошествии d (от Now())
	// вызывается fn. У настенных часов fn — в отдельной горушине (time.AfterFunc);
	// у виртуальных — при Advance/Set, когда время доходит до дедлайна.
	AfterFunc(d time.Duration, fn func())
}

// Real — настенные часы.
type Real struct{}

// NewReal возвращает настенные часы.
func NewReal() Real { return Real{} }

// Now возвращает текущее время UTC.
func (Real) Now() time.Time { return time.Now().UTC() }

// AfterFunc — одноразовый таймер на d (wall-clock).
func (Real) AfterFunc(d time.Duration, fn func()) { time.AfterFunc(d, fn) }
