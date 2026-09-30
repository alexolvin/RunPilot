// Command lintweb — R2 для фронтенда (web/):
//   - цвета (#hex, rgb(), hsl()) — только в web/styles/tokens.css;
//   - кириллица в строковых литералах JS — только в web/app/i18n/ru.js;
//   - числовые литералы в JS — только в web/app/constants.js, кроме 0/1/-1.
//
// web/ появляется на этапе W3; до этого цели нет — OK (0 находок).
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

var (
	reHex  = regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b`)
	reFunc = regexp.MustCompile(`\b(rgb|rgba|hsl|hsla)\(`)
)

func main() {
	root := "web"
	if _, err := os.Stat(root); os.IsNotExist(err) {
		fmt.Println("OK: web/ отсутствует (фронтенд — с этапа W3)")
		return
	}
	bad := 0
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			// vendor/ — фиксированные сторонние библиотеки (хеши в VERSIONS.txt):
			// R2 к ним не применяется (это не наш исходник).
			if d.Name() == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		switch {
		case strings.HasSuffix(path, ".css"):
			if !isTokens(path) {
				bad += checkCSS(path)
			}
		case strings.HasSuffix(path, ".js"):
			bad += checkJS(path, isI18nRU(path), isConstants(path))
		}
		return nil
	})
	if bad > 0 {
		fmt.Printf("FAIL: %d нарушений R2 в web/\n", bad)
		os.Exit(1)
	}
	fmt.Println("OK: R2 — цвета/кириллица/числа в web/ на своих местах")
}

func isTokens(p string) bool  { return strings.Contains(p, "tokens.css") }
func isI18nRU(p string) bool  { return strings.Contains(p, "i18n") && strings.HasSuffix(p, "ru.js") }
func isConstants(p string) bool { return strings.Contains(p, "constants.js") }

// checkCSS — цвета вне tokens.css.
func checkCSS(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		fatalf("чтение %s: %v", path, err)
	}
	bad := 0
	for i, line := range strings.Split(string(data), "\n") {
		if reHex.MatchString(line) || reFunc.MatchString(line) {
			fmt.Printf("%s:%d: цвет вне tokens.css\n", path, i+1)
			bad++
		}
	}
	return bad
}

// checkJS — кириллица в строках (кроме i18n/ru.js) и числовые литералы
// (кроме constants.js, кроме 0/1/-1). Мини-токенизатор: строки и комментарии.
func checkJS(path string, allowCyrillic, allowNumbers bool) int {
	data, err := os.ReadFile(path)
	if err != nil {
		fatalf("чтение %s: %v", path, err)
	}
	bad := 0
	line := 1
	inStr := byte(0)     // 0 | ' | " | `
	inLineC, inBlockC := false, false
	prevSignificant := byte(0)
	i := 0
	for i < len(data) {
		c := data[i]
		if c == '\n' {
			line++
			inLineC = false
			i++
			continue
		}
		if inLineC {
			i++
			continue
		}
		if inBlockC {
			if c == '*' && i+1 < len(data) && data[i+1] == '/' {
				inBlockC = false
				i += 2
				continue
			}
			i++
			continue
		}
		if inStr != 0 {
			if c == '\\' && i+1 < len(data) {
				i += 2
				continue
			}
			if c == inStr {
				inStr = 0
			} else if isCyrillic(c) && !allowCyrillic {
				fmt.Printf("%s:%d: кириллица в JS-строке (вне i18n/ru.js)\n", path, line)
				bad++
				i++
				continue
			}
			i++
			continue
		}
		// нормальный режим
		switch {
		case c == '/' && i+1 < len(data) && data[i+1] == '/':
			inLineC = true
			i += 2
			continue
		case c == '/' && i+1 < len(data) && data[i+1] == '*':
			inBlockC = true
			i += 2
			continue
		case c == '\'' || c == '"' || c == '`':
			inStr = c
			i++
			continue
		case c >= '0' && c <= '9' && !isIdentChar(prevSignificant):
			end := numberEnd(data, i)
			tok := string(data[i:end])
			if !isIdentChar(data[end]) && !isUnitNumber(tok, prevSignificant) && !allowNumbers {
				fmt.Printf("%s:%d: числовой литерал %q в JS (вне constants.js)\n", path, line, tok)
				bad++
			}
			i = end
			prevSignificant = data[i-1]
			continue
		}
		if c != ' ' && c != '\t' && c != '\r' {
			prevSignificant = c
		}
		i++
	}
	return bad
}

func isIdentChar(c byte) bool {
	return c == '_' || c == '$' || c == '.' ||
		(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isCyrillic(c byte) bool {
	// UTF-8 кириллица: первый байт D0/D1.
	return c == 0xD0 || c == 0xD1
}

// numberEnd — конец числового токена.
func numberEnd(data []byte, i int) int {
	j := i
	for j < len(data) && (data[j] >= '0' && data[j] <= '9' || data[j] == '.') {
		j++
	}
	return j
}

// isUnitNumber — 0, 1 или -1 (допустимы везде).
func isUnitNumber(tok string, prev byte) bool {
	if tok == "0" || tok == "1" {
		return true
	}
	return tok == "1" && prev == '-'
}

var _ = unicode.IsLetter // (запас на расширение)

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "lintweb: "+format+"\n", args...)
	os.Exit(2)
}
