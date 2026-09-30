// Package sid — идентификаторы сессий (раздел 14 ТЗ): 10 символов
// Crockford base32, криптостойкий CSPRNG, алфавит без гласных.
package sid

import (
	"crypto/rand"
	"fmt"
)

// Alphabet — Crockford base32 без гласных I, L, O, U (32 символа,
// 256 % 32 == 0 — остаточная модульная смещённость отсутствует).
const Alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// New генерирует sid криптостойким CSPRNG.
func New() (string, error) {
	buf := make([]byte, Length)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("sid: CSPRNG: %w", err)
	}
	out := make([]byte, Length)
	for i, b := range buf {
		out[i] = Alphabet[int(b)%len(Alphabet)]
	}
	return string(out), nil
}
