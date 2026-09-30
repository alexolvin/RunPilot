package scheduler

import "time"

// tickExternal — переходы слотов в EXTERNAL и обратно (раздел 5 ТЗ):
// ext > 0 держится external_confirm_sec → min(ext, свободные слоты)
// слотов EXTERNAL; обратно — через тот же интервал после ext = 0.
//
// extSince — момент, с которого текущее значение target требует
// подтверждения (сбрасывается после перехода).
func (s *Scheduler) tickExternal(now time.Time) {
	for name, target := range s.extTarget {
		in := s.extCount[name] > 0
		switch {
		case target > 0 && !in:
			// Входить в EXTERNAL: подтвердить интервалом.
			if since, ok := s.extSince[name]; !ok {
				s.extSince[name] = now
			} else if now.Sub(since) >= s.externalConfirm() {
				s.extCount[name] = s.externalWant(name, target, now)
				delete(s.extSince, name)
			}
		case target > 0 && in:
			// Уже в EXTERNAL: держать min(ext, свободные слоты).
			s.extCount[name] = s.externalWant(name, target, now)
			delete(s.extSince, name)
		case target == 0 && in:
			// Возврат FREE: подтвердить тем же интервалом.
			if since, ok := s.extSince[name]; !ok {
				s.extSince[name] = now
			} else if now.Sub(since) >= s.externalConfirm() {
				s.extCount[name] = 0
				delete(s.extSince, name)
			}
		default:
			delete(s.extSince, name)
		}
	}
}

// externalWant — min(ext, свободные слоты сервера). «Свободный» здесь —
// не LEASED и не COOLDOWN; EXTERNAL-слоты НЕ исключаются: иначе при
// ext ≥ числа слотов externalWant давал 0 и EXTERNAL сносился следующим
// тиком (Э6, регрессия TestExternalFullLoad).
func (s *Scheduler) externalWant(name string, ext int, now time.Time) int {
	avail := 0
	for _, v := range s.src.List() {
		if v.Name != name {
			continue
		}
		leased := s.leasedSlots(name)
		for slot := 1; slot <= v.Slots; slot++ {
			if leased[slot] {
				continue
			}
			if until, ok := s.cooldown[name][slot]; ok && now.Before(until) {
				continue
			}
			avail++
		}
	}
	if ext > avail {
		return avail
	}
	return ext
}

// externalSlots — сколько слотов сервера в EXTERNAL (для freeSlots).
func (s *Scheduler) externalSlots(name string) int {
	return s.extCount[name]
}

// externalSlotsAny — суммарно по серверам (предикат why).
func (s *Scheduler) externalSlotsAny() int {
	n := 0
	for _, c := range s.extCount {
		n += c
	}
	return n
}
