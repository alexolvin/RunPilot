// Command genicons — регенерация web/app/components/icon.js из web/icons/*.svg.
//
// Иконки Lucide хранятся как SVG-файлы (web/icons/, источник правды + PWA-иконки).
// Компонент иконок рендерит их инлайн (stroke=currentColor, синхронно — для
// воспроизводимых скриншотов). icon.js содержит ВНУТРЕННЮЮ разметку SVG (ровно как
// в файлах). `make icons` перегоняет web/icons/ → icon.js.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var reSVG = regexp.MustCompile(`(?s)<svg[^>]*>(.*)</svg>`)
var reWS = regexp.MustCompile(`\s+`)

func main() {
	dir := "web/icons"
	entries, err := os.ReadDir(dir)
	if err != nil {
		fatalf("web/icons: %v", err)
	}
	type pair struct{ name, inner string }
	var pairs []pair
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".svg") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".svg")
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			fatalf("чтение %s: %v", e.Name(), err)
		}
		m := reSVG.FindSubmatch(data)
		if m == nil {
			fatalf("%s: нет тега <svg>", e.Name())
		}
		inner := reWS.ReplaceAllString(strings.TrimSpace(string(m[1])), " ")
		pairs = append(pairs, pair{name, inner})
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].name < pairs[j].name })

	var b strings.Builder
	b.WriteString(`// Иконки Lucide (web/icons/*.svg, v1.48.0). Внутренняя разметка — ровно как в
// web/icons/<name>.svg (stroke=currentColor; stroke-width = 1.75 задаёт CSS .icon svg,
// раздел 9.2). Сгенерировано из web/icons/ — синхронизировать через tools/genicons.
// R2/lintweb: только строковые литералы, без цветов/кириллицы/чисел вне строк.
export const ICONS = {
`)
	for _, p := range pairs {
		// Ключ в кавычках: имена иконок содержат дефис (file-text, more-vertical),
		// что некорректно как некавычный JS-идентификатор (file-text ≡ file - text).
		fmt.Fprintf(&b, "  %s: %s,\n", jsQuote(p.name), jsQuote(p.inner))
	}
	b.WriteString("}\n")
	if err := os.MkdirAll("web/app/components", 0o755); err != nil {
		fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile("web/app/components/icon.js", []byte(b.String()), 0o644); err != nil {
		fatalf("запись icon.js: %v", err)
	}
	fmt.Printf("OK: genicons — %d иконок в web/app/components/icon.js\n", len(pairs))
}

func jsQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "genicons: "+format+"\n", args...)
	os.Exit(1)
}
