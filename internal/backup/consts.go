// Package backup — константы (таблица R2).
package backup

const (
	// backupsName — каталог копий в каталоге БД.
	backupsName = "backups"
	// timeStamp — формат времени в имени копии.
	timeStamp = "20060102-150405"
	// quickCheckOK — ожидаемая строка PRAGMA quick_check.
	quickCheckOK = "ok"
	// расширения файлов SQLite.
	dbSuffix  = ".db"
	walSuffix = "-wal"
	shmSuffix = "-shm"
	// tmpSuffix — временный файл при restore (атомарный rename).
	tmpSuffix = ".restore-tmp"
	// binTmpSuffix / binOldSuffix — атомарная замена бинарника: rename в
	// сторону, а не O_TRUNC (перезапись запущенного бинарника = ETXTBSY).
	binTmpSuffix = ".bin-restore-tmp"
	binOldSuffix = ".bin-restore-old"
	// права каталога/файлов копий.
	dirMode  = 0o700
	fileMode = 0o600
	// binFileMode — права возвращаемого бинарника runpilot.prev.
	binFileMode = 0o755
)
