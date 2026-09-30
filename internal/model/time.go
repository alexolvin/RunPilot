package model

import "time"

// timeFormat — фиксированная дробная часть (9 знаков), чтобы
// лексикографическое сравнение строк в SQLite совпадало с хронологическим.
const timeFormat = "2006-01-02T15:04:05.000000000Z07:00"

// FormatTime сериализует время для БД (всегда UTC).
func FormatTime(t time.Time) string { return t.UTC().Format(timeFormat) }

// ParseTime разбирает время из БД.
func ParseTime(s string) (time.Time, error) {
	return time.Parse(timeFormat, s)
}
