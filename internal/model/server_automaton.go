package model

import "time"

// ServerHealth — состояние автомата сервера (раздел 4 ТЗ):
// UP → DOWN после downAfter подряд неудачных /health,
// DOWN → UP после upAfter подряд удачных.
//
// Чистая функция: серии считаются здесь, тайминги — на стороне вызывающего.
type ServerHealth struct {
	State      ServerState
	FailStreak int
	OKStreak   int
}

// NewServerHealth — сервер стартует в UP до первой проверки.
func NewServerHealth() ServerHealth { return ServerHealth{State: ServerUp} }

// Observe применяет результат одной health-проверки.
// Возвращает новое состояние и флаг смены состояния (DownSince ставит вызывающий).
func (h ServerHealth) Observe(healthy bool, downAfter, upAfter int) (ServerHealth, bool) {
	if healthy {
		h.FailStreak = 0
		h.OKStreak++
		if h.State == ServerDown && h.OKStreak >= upAfter {
			return ServerHealth{State: ServerUp}, true
		}
		return h, false
	}
	h.OKStreak = 0
	h.FailStreak++
	if (h.State == ServerUp || h.State == ServerDraining) && h.FailStreak >= downAfter {
		return ServerHealth{State: ServerDown}, true
	}
	return h, false
}

// SetDraining — команда оператора: UP ↔ DRAINING (раздел 4).
// Health-серии сохраняются; из DRAINING health-отказ также ведёт в DOWN,
// восстановление из DOWN возвращает в UP (документировано в ARCHITECTURE.md).
func (h ServerHealth) SetDraining(on bool) (ServerHealth, bool) {
	target := ServerDraining
	if !on {
		target = ServerUp
	}
	if h.State == target {
		return h, false
	}
	h.State = target
	return h, true
}

// Slot — состояние слота сервера (раздел 4 ТЗ):
// FREE / LEASED / COOLDOWN (scheduler.cooldown_sec после освобождения) / EXTERNAL.
//
// Все методы — чистые функции; время передаётся явно (часы координатора).
type Slot struct {
	State SlotState
	// CooldownUntil — когда COOLDOWN истекает; zero = не в cooldown.
	CooldownUntil time.Time
	// ExtSince — с какого момента ext непрерывно > 0; zero = ext = 0.
	ExtSince time.Time
	// Ext0Since — с какого момента ext непрерывно = 0; zero = ext > 0 или нет данных.
	Ext0Since time.Time
}

// NewSlot — слот начинается FREE.
func NewSlot() Slot { return Slot{State: SlotFree} }

// Leased — слот занят арендой.
func (s Slot) Leased() Slot {
	s.State = SlotLeased
	return s
}

// ReleasedAt — освобождение: LEASED → COOLDOWN на cooldown (раздел 4).
func (s Slot) ReleasedAt(now time.Time, cooldown time.Duration) Slot {
	if s.State != SlotLeased {
		return s
	}
	return Slot{State: SlotCooldown, CooldownUntil: now.Add(cooldown)}
}

// TickCooldown — COOLDOWN → FREE после истечения (чистая функция).
func (s Slot) TickCooldown(now time.Time) Slot {
	if s.State == SlotCooldown && !now.Before(s.CooldownUntil) {
		return Slot{State: SlotFree}
	}
	return s
}

// SampleExternal — новый снимок внешней нагрузки (раздел 5).
// extPositive — ext > 0 на этом снимке.
func (s Slot) SampleExternal(extPositive bool, now time.Time) Slot {
	if extPositive {
		if s.ExtSince.IsZero() {
			s.ExtSince = now
		}
		s.Ext0Since = time.Time{}
	} else {
		if s.Ext0Since.IsZero() {
			s.Ext0Since = now
		}
		s.ExtSince = time.Time{}
	}
	return s
}

// ExternalReady — слот вправе перейти в EXTERNAL:
// ext > 0 держится не меньше confirm (scheduler.external_confirm_sec).
func (s Slot) ExternalReady(now time.Time, confirm time.Duration) bool {
	return s.State == SlotFree && !s.ExtSince.IsZero() && now.Sub(s.ExtSince) >= confirm
}

// ExternalCleared — слот вправе вернуться в FREE:
// ext = 0 держится не меньше confirm.
func (s Slot) ExternalCleared(now time.Time, confirm time.Duration) bool {
	return s.State == SlotExternal && !s.Ext0Since.IsZero() && now.Sub(s.Ext0Since) >= confirm
}

// ToExternal — переход в EXTERNAL (вызывает планировщик по ExternalReady).
func (s Slot) ToExternal() Slot {
	s.State = SlotExternal
	return s
}

// ToFree — переход в FREE.
func (s Slot) ToFree() Slot {
	s.State = SlotFree
	return s
}
