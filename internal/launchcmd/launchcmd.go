// Package launchcmd — «команды для запуска» сессии (раздел постановки в очередь).
//
// Заряд запуска — последовательность литерального текста и клавиш (Enter,
// курсоры, пробел, Esc), которую оператор формирует наглядно в поле «Команды
// для запуска». Текст хранится в системе (не в панели) и воспроизводится узлом
// атомарно только в момент запуска (dispatch), а не нажатием прямо в терминал.
//
// Клавиши обозначаются человекочитаемыми токенами в скобках: [Enter], [Up],
// [Down], [Left], [Right], [Space], [Esc]. Всё, что не является известным
// токеном, — литеральный текст. Токены в скобках не могут совпасть с текстом
// промпта (в промпте почти никогда нет ровно «[Enter]»), поэтому парсинг
// однозначен.
package launchcmd

import (
	"strings"
	"unicode/utf8"
)

// TokenKind — вид элемента «команды для запуска».
type TokenKind int

const (
	// Text — литеральный текст (вставляется кодеру побайто).
	Text TokenKind = iota
	// Key — клавиша (Enter, курсоры, пробел, Esc).
	Key
)

// Token — один элемент «команды для запуска».
type Token struct {
	Kind  TokenKind
	Value string // Text: строка; Key: имя клавиши tmux (Enter/Up/Down/Left/Right/Space/Escape)
}

// keyDef — токен клавиши → клавиша tmux. Срез (порядок кнопок UI и парсинга).
type keyDef struct{ token, key string }

// keyDefs — минимальный набор клавиш. Ни один токен не является префиксом
// другого, поэтому порядок совпадения не влияет на результат.
var keyDefs = []keyDef{
	{"[Enter]", "Enter"},
	{"[Up]", "Up"},
	{"[Down]", "Down"},
	{"[Left]", "Left"},
	{"[Right]", "Right"},
	{"[Space]", "Space"},
	{"[Esc]", "Escape"},
}

// KeyInfo — описание клавиши для кнопок/легенды UI.
type KeyInfo struct {
	Token string // токен в поле (например, [Enter])
	Label string // подпись кнопки (Enter, ↑, Пробел, Esc)
	Key   string // клавиша tmux
}

// Keys — набор клавиш для UI (порядок кнопок).
var Keys = []KeyInfo{
	{"[Enter]", "Enter", "Enter"},
	{"[Up]", "↑", "Up"},
	{"[Down]", "↓", "Down"},
	{"[Left]", "←", "Left"},
	{"[Right]", "→", "Right"},
	{"[Space]", "Пробел", "Space"},
	{"[Esc]", "Esc", "Escape"},
}

// matchKey — если s начинается с токена клавиши: (клавиша, длина токена).
func matchKey(s string) (string, int, bool) {
	for _, kd := range keyDefs {
		if strings.HasPrefix(s, kd.token) {
			return kd.key, len(kd.token), true
		}
	}
	return "", 0, false
}

// Parse — разбирает «команду для запуска» в токены. Всё, что не является
// известным токеном клавиши, — литеральный текст (по rune, UTF-8 не режется).
func Parse(s string) []Token {
	var out []Token
	var buf strings.Builder
	flush := func() {
		if buf.Len() > 0 {
			out = append(out, Token{Kind: Text, Value: buf.String()})
			buf.Reset()
		}
	}
	for i := 0; i < len(s); {
		if key, n, ok := matchKey(s[i:]); ok {
			flush()
			out = append(out, Token{Kind: Key, Value: key})
			i += n
			continue
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		buf.WriteString(s[i : i+size])
		i += size
	}
	flush()
	return out
}

// HasKey — содержит ли «команда для запуска» хотя бы одну клавишу.
func HasKey(s string) bool {
	for _, t := range Parse(s) {
		if t.Kind == Key {
			return true
		}
	}
	return false
}

// HasEnter — содержит ли «команда для запуска» клавишу Enter.
func HasEnter(s string) bool {
	for _, t := range Parse(s) {
		if t.Kind == Key && t.Value == "Enter" {
			return true
		}
	}
	return false
}

// IsTextOnly — только литеральный текст (без клавиш): годится для немедленной
// вставки («Вставить»), но не для запуска.
func IsTextOnly(s string) bool { return !HasKey(s) }

// EnsureTrailingEnter — гарантирует завершающий Enter: пустая команда →
// «[Enter]»; если последний токен уже Enter — без изменений; иначе добавлять.
// Правый whitespace обрезается (не меняет смысл команды).
func EnsureTrailingEnter(s string) string {
	t := strings.TrimRight(s, " \t\r\n")
	if t == "" {
		return "[Enter]"
	}
	toks := Parse(t)
	if n := len(toks); n > 0 {
		last := toks[n-1]
		if last.Kind == Key && last.Value == "Enter" {
			return t
		}
	}
	return t + "[Enter]"
}

// PlainText — только литеральные части (для превью: что будет вставлено как
// текст, без клавиш).
func PlainText(s string) string {
	var b strings.Builder
	for _, t := range Parse(s) {
		if t.Kind == Text {
			b.WriteString(t.Value)
		}
	}
	return b.String()
}
