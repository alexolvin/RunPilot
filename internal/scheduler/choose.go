package scheduler

import (
	"sort"
	"time"

	"runpilot/internal/model"
	"runpilot/internal/store"
)

// slotRec — свободный слот в текущем тике (free := freeSlots()).
type slotRec struct {
	Name      string
	Slot      int
	FreeSince time.Time // zero — свободен «всегда» (самый ранний в LRU)
	taken     bool
}

// serverSlots — число слотов сервера (для инварианта слотов, п.1); 0, если
// сервер не в конфигурации (такой слот в выдачу не попадает).
func (s *Scheduler) serverSlots(name string) int {
	if v, ok := s.viewOf(name); ok {
		return v.Slots
	}
	return 0
}

// leasedSlots — занятые (PENDING/ACTIVE) слоты сервера.
func (s *Scheduler) leasedSlots(name string) map[int]bool {
	out := map[int]bool{}
	leases, err := s.st.LeaseListByServer(name)
	if err != nil {
		return out
	}
	for _, l := range leases {
		out[l.Slot] = true
	}
	return out
}

// freeSlots — список свободных слотов: сервер UP (не DRAINING), слот не
// LEASED, не COOLDOWN, не EXTERNAL. Серверы — в порядке priority.
func (s *Scheduler) freeSlots(now time.Time) []slotRec {
	var out []slotRec
	for _, v := range s.src.List() {
		if v.State != model.ServerUp {
			continue
		}
		leased := s.leasedSlots(v.Name)
		ext := s.externalSlots(v.Name)
		extMarked := 0
		for slot := 1; slot <= v.Slots; slot++ {
			if leased[slot] {
				continue
			}
			if until, ok := s.cooldown[v.Name][slot]; ok && now.Before(until) {
				continue
			}
			if extMarked < ext {
				extMarked++
				continue
			}
			fs := time.Time{}
			if m := s.freeSince[v.Name]; m != nil {
				fs = m[slot]
			}
			out = append(out, slotRec{Name: v.Name, Slot: slot, FreeSince: fs})
		}
	}
	// Серверы — в порядке priority (наибольший первым): правило 3 берёт
	// cand[0] как верхнюю группу, cand внутри группы — в порядке LRU.
	sort.SliceStable(out, func(i, j int) bool {
		return s.topPriority(out[i].Name) > s.topPriority(out[j].Name)
	})
	return out
}

// choose — сервер и слот для выдачи (раздел 5 ТЗ, первое сработавшее
// правило): pin/prefer → тёплый кэш → наибольший priority → LRU.
// Возвращает выбранный слот и правило выбора (pin | prefer | warm_cache |
// priority | lru) для LEASE_GRANT (наблюдаемость, W13 этап 2).
func (s *Scheduler) choose(e model.QueueEntry, sess store.SessionRecord,
	cand []slotRec, now time.Time) (slotRec, string) {
	// Правило 1: цель pin или prefer.
	if e.Constraint.Kind != model.ConstraintNone {
		for _, f := range cand {
			if f.Name == e.Constraint.Server {
				rule := "prefer"
				if e.Constraint.Kind == model.ConstraintPin {
					rule = "pin"
				}
				return f, rule
			}
		}
	}

	// Правило 2: тёплый кэш. s = last_server; с конца хода ≤
	// affinity_ttl_sec; с тех пор на s не было аренды другой сессии;
	// s ≠ last_migrated_from текущего хода.
	if sess.LastServer != "" && !sess.LastTurnEnd.IsZero() &&
		now.Sub(sess.LastTurnEnd) <= s.affinityTTL() &&
		sess.LastMigratedFrom != sess.LastServer {
		lastGrant, ok, err := s.st.LatestGrantByServer(sess.LastServer, sess.SID)
		if err == nil && (!ok || lastGrant.Before(sess.LastTurnEnd)) {
			for _, f := range cand {
				if f.Name == sess.LastServer {
					return f, "warm_cache"
				}
			}
		}
	}

	// Правило 3: наибольший priority (cand уже в порядке priority —
	// берём группу серверов с максимальным).
	// Правило 4: слот, освобождённый раньше остальных (LRU) — внутри
	// группы; zero = «освобождён всегда» = самый ранний.
	top := s.topPriority(cand[0].Name)
	var group []slotRec
	for _, f := range cand {
		if s.topPriority(f.Name) != top {
			break
		}
		group = append(group, f)
	}
	if len(group) == 1 {
		// Единственный слот с максимальным priority — выбор по priority.
		return group[0], "priority"
	}
	// Несколько слотов с одинаковым priority — решает LRU.
	best := group[0]
	for _, f := range group[1:] {
		if newerFree(f.FreeSince, best.FreeSince) {
			best = f
		}
	}
	return best, "lru"
}

// topPriority — priority сервера по имени.
func (s *Scheduler) topPriority(name string) int {
	if v, ok := s.viewOf(name); ok {
		return v.Priority
	}
	return 0
}

// newerFree — a «новее» b (для LRU: ищем самое старое).
func newerFree(a, b time.Time) bool {
	switch {
	case a.IsZero():
		return false // zero = самое старое
	case b.IsZero():
		return true
	default:
		return a.After(b)
	}
}

// slotFree — пометить слот освобождённым в now (для LRU).
func (s *Scheduler) slotFree(name string, slot int, at time.Time) {
	if s.freeSince[name] == nil {
		s.freeSince[name] = map[int]time.Time{}
	}
	s.freeSince[name][slot] = at
}

// slotLeased — слот занят: LRU-пометка сбрасывается.
func (s *Scheduler) slotLeased(name string, slot int) {
	if m := s.freeSince[name]; m != nil {
		delete(m, slot)
	}
}
