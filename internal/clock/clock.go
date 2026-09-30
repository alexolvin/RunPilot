// Package clock — единственный источник времени для планировщика,
// автоматов и таймаутов (строгое правило ТЗ: time.Now в этих путях запрещён).
package clock

import "time"

// Clock возвращает «сейчас» в UTC.
type Clock interface {
	Now() time.Time
}

// Real — настенные часы.
type Real struct{}

// NewReal возвращает настенные часы.
func NewReal() Real { return Real{} }

// Now возвращает текущее время UTC.
func (Real) Now() time.Time { return time.Now().UTC() }
