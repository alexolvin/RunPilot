package web

import (
	"crypto/sha256"
	"encoding/base64"
	"os"
	"regexp"
	"strings"
	"testing"
)

var reImportMap = regexp.MustCompile(`<script type="importmap">([^<]*)</script>`)

// importmapBody — тело <script type="importmap">BODY</script> из HTML-файла.
func importmapBody(t *testing.T, rel string) string {
	t.Helper()
	data, err := os.ReadFile(rel)
	if err != nil {
		t.Fatalf("чтение %s: %v", rel, err)
	}
	m := reImportMap.FindStringSubmatch(string(data))
	if m == nil {
		t.Fatalf("%s: <script type=\"importmap\"> не найден", rel)
	}
	return m[1]
}

// TestCSPImportMapHash — регрессия (W9): hash importmap в CSP соответствует
// телу, и тела в index.html/login.html идентичны (один hash покрывает оба).
// Без этой проверки изменение importmap МОЛЧА блокирует CSP: не резолвятся
// bare-импорты (preact/@xterm) и всё веб-приложение не стартует — страница
// загрузки жива (её hash совпадает), а приложение — нет, что легко пропустить.
// Пересчёт hash:
//
//	printf '%s' '<тело>' | openssl dgst -sha256 -binary | openssl base64 -A
func TestCSPImportMapHash(t *testing.T) {
	const idx = "../../web/index.html"
	const logn = "../../web/login.html"
	bIdx := importmapBody(t, idx)
	bLogn := importmapBody(t, logn)
	if bIdx != bLogn {
		t.Fatalf("importmap разошёлся между index.html и login.html (нужно идентичное тело — один CSP hash):\nindex: %s\nlogin: %s", bIdx, bLogn)
	}
	// Тело — инлайн-скрипт: одна строка, без пробельных символов (иначе hash
	// не совпадёт с тем, что считает браузер).
	if strings.ContainsAny(bIdx, " \t\n\r") {
		t.Fatalf("тело importmap содержит пробельные символы: %q", bIdx)
	}
	sum := sha256.Sum256([]byte(bIdx))
	want := "sha256-" + base64.StdEncoding.EncodeToString(sum[:])
	if !strings.Contains(csp, want) {
		t.Fatalf("CSP hash не соответствует телу importmap:\nожидал: %s\ncsp:     %s", want, csp)
	}
}
