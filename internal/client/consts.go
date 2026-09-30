// Package client — константы (таблица R2): таймауты, HTTP-статусы, буферы.
package client

const (
	// httpTimeoutSec — таймаут HTTP-клиента (сек).
	httpTimeoutSec = 15
	// decimalBase / int64Bits — основание и разрядность strconv.
	decimalBase = 10
	int64Bits   = 64
	// httpNonOK — HTTP-статусы >= httpNonOK считаются не-OK.
	httpNonOK = 300
	// sibInitialBuf / sibMaxBuf — начальный/максимальный буфер SSE-сканера.
	sibInitialBuf = 64 * 1024
	sibMaxBuf     = 1024 * 1024
)
