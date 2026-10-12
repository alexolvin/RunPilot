package relay

import (
	"runpilot/internal/textnorm"
)

// normalize — приведение текста для доказательства отправки (профиль
// `normalize: collapse`): все подряд пробельные символы → один пробел.
// Фиксируется по фикстурам (ТЗ Relay §9).
func normalize(s string) string { return textnorm.Normalize(s) }

// normHash — SHA-256 нормализованного текста (хеш отправки/доставки).
func normHash(s string) string { return textnorm.NormHash(s) }

// isBlank — пустое ли поле ввода.
func isBlank(s string) bool { return textnorm.IsBlank(s) }
