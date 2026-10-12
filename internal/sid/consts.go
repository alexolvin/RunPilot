// Package sid — константы (таблица R2): длина идентификатора сессии и
// размеры/позиции UUID (RFC 4122).
package sid

// Length — длина sid (10 символов Crockford base32, раздел 14 ТЗ).
const Length = 10

// Размеры и позиции UUID (RFC 4122): 16 байт, формат 8-4-4-4-12 hex.
const (
	uuidBytes    = 16
	uuidVariantI = 6  // байт version (первая часть 4-байтной группы)
	uuidVariantJ = 8  // байт variant
	uuidMaskVer  = 0x0f // низкие 4 бита version
	uuidVerBit   = 0x40 // version 4 (0100)
	uuidMaskVar  = 0x3f // низкие 6 бит variant
	uuidVarBit   = 0x80 // variant 10 (10)
	// Границы 5 hex-групп формата 8-4-4-4-12.
	uuidA = 4
	uuidB = 6
	uuidC = 8
	uuidD = 10
	uuidE = 16
)
