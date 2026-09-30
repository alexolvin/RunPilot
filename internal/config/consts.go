// Package config — константы (таблица R2): единицы счёта и диапазоны.
package config

const (
	// kibi — байт в кибибайте (двоичный счёт).
	kibi = 1024
	// maxPort — максимальный номер TCP-порта (раздел 13 ТЗ: валидация).
	maxPort = 65535
	// serverNamePattern — шаблон имени сервера (v2 раздел 3.4).
	serverNamePattern = `^[a-z0-9-]{1,32}$`
	// timeFieldsInHM — число полей в формате "HH:MM".
	timeFieldsInHM = 2
	// hoursPerDay / minutesPerHour — диапазоны часов и минут.
	hoursPerDay   = 24
	minutesPerHour = 60
)
