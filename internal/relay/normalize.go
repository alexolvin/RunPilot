package relay

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode"
)

// normalize — приведение текста для доказательства отправки (профиль
// `normalize: collapse`): все подряд пробельные символы → один пробел,
// обрезаем начало/конец. Фиксируется по фикстурам (ТЗ Relay §9).
func normalize(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// normHash — SHA-256 нормализованного текста (хеш отправки).
func normHash(s string) string {
	sum := sha256.Sum256([]byte(normalize(s)))
	return hex.EncodeToString(sum[:])
}

// isSpaceRun — нет ли текста вне пробелов (пустое поле ввода).
func isBlank(s string) bool {
	for _, r := range s {
		if !unicode.IsSpace(r) {
			return false
		}
	}
	return true
}
