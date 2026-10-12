// Package textnorm — нормализация текста и SHA-256 (ТЗ Relay, строгое
// правило 3): хеш хранится в БД, текст — нет. Общий для relay и agentlog.
package textnorm

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// Normalize — схлопывает пробельные последовательности в один пробел.
func Normalize(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// NormHash — SHA-256 нормализованного текста (hex).
func NormHash(s string) string {
	sum := sha256.Sum256([]byte(Normalize(s)))
	return hex.EncodeToString(sum[:])
}

// IsBlank — пустой ли текст после нормализации.
func IsBlank(s string) bool {
	return strings.TrimSpace(s) == ""
}
