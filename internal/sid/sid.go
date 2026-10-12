// Package sid — идентификаторы сессий (раздел 14 ТЗ): 10 символов
// Crockford base32, криптостойкий CSPRNG, алфавит без гласных.
package sid

import (
	"crypto/rand"
	"fmt"
)

// NewUUIDv4 — UUIDv4 (RFC 4122) из CSPRNG. Используется для
// qwen_session_id (R0 ТЗ Relay §3): --session-id / --resume / --chat-recording.
func NewUUIDv4() (string, error) {
	buf := make([]byte, uuidBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("sid: UUIDv4 CSPRNG: %w", err)
	}
	// variant 10, version 4 (RFC 4122).
	buf[uuidVariantI] = (buf[uuidVariantI] & uuidMaskVer) | uuidVerBit
	buf[uuidVariantJ] = (buf[uuidVariantJ] & uuidMaskVar) | uuidVarBit
	return fmt.Sprintf("%x-%x-%x-%x-%x",
		buf[0:uuidA], buf[uuidA:uuidB], buf[uuidB:uuidC], buf[uuidC:uuidD], buf[uuidD:uuidE]), nil
}

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

// Valid — проверяет формат sid: 10 символов Crockford base32.
func Valid(s string) bool {
	if len(s) != Length {
		return false
	}
	for _, c := range s {
		if !isValidChar(c) {
			return false
		}
	}
	return true
}

func isValidChar(c rune) bool {
	for _, a := range Alphabet {
		if c == a {
			return true
		}
	}
	return false
}
