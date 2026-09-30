// Package runpilotweb — встроенный (embed) фронтенд web/ (v2 раздел 18: «без сборки,
// ESM-модули и зафиксированные библиотеки встраиваются через embed»).
//
// Пакет в корне модуля, потому что //go:embed видит только каталог пакета и
// его подкаталоги, а web/ лежит в корне модуля. FS() возвращает fs, укоренённый
// в web/ (файлы доступны как index.html, styles/…, app/…, vendor/…, fonts/…).
// Статика раздаётся internal/web; наружу только через tailscale serve (раздел 16).
package runpilotweb

import (
	"embed"
	"io/fs"
)

//go:embed all:web
var webFS embed.FS

// FS — встроенный фронтенд, укоренённый в web/.
func FS() (fs.FS, error) {
	return fs.Sub(webFS, "web")
}
