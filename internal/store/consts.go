// Package store — константы (таблица R2): режимы файлов и параметры SQLite.
package store

const (
	// dbDirMode — режим создаваемого каталога с БД.
	dbDirMode = 0o700
	// dbFileMode — режим файла БД.
	dbFileMode = 0o600
	// maxOpenConns — максимальное число открытых соединений SQLite.
	maxOpenConns = 8
	// migrationNameFields — число полей имени файла миграции «NN_имя.sql».
	migrationNameFields = 2
	// ttfbP95Percent / percentWhole100 — процентиль p95: n * p95 / whole.
	ttfbP95Percent  = 95
	percentWhole100 = 100

	// nodeTokenBytes — длина случайных байтов токена узла (16 → 32 hex-символа).
	nodeTokenBytes = 16
	// bitsPerByte — битов на байт (фолбэк генерации токена).
	bitsPerByte = 8
	// decimalBase — десятичная система (strconv.FormatInt).
	decimalBase = 10
	// sqliteMainCodeMask — выделение базового кода из extended-кода SQLite
	// (напр. 1555 → 19 = SQLITE_CONSTRAINT).
	sqliteMainCodeMask = 0xff
)
