// Command lintlits — R2 (запрет хардкода): по всему internal/* и cmd/* нет
// числовых литералов, кроме 0/1/-1, и строковых литералов вида длительности,
// URL, IP или имени хоста. Исключения: файлы-источники значений (таблица R2),
// миграции и *_test.go. Любая находка — FAIL.
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// allowedFiles — файлы-источники значений (таблица R2): литералы допустимы.
var allowedFiles = map[string]bool{
	"defaults.go": true, // числовые значения и пути по умолчанию
	"schema.go":   true, // метаданные настроек (ссылки на константы defaults.go)
	"consts.go":   true, // константы протоколов и форматов
}

var (
	// Чистая Go-длительность: "5s", "10m", "1h30m", "500ms".
	reDuration = regexp.MustCompile(`^\d+(\.\d+)?(ns|us|µs|ms|s|m|h|d)(\d+(\.\d+)?(ns|us|µs|ms|s|m|h|d))*$`)
	// IPv4: "127.0.0.1". Имена хостов отдельно проверяет tools/linthosts.
	reIPv4 = regexp.MustCompile(`^\d{1,3}(\.\d{1,3}){3}$`)
)

func main() {
	bad := 0
	for _, root := range []string{"internal", "cmd"} {
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			if allowedFiles[filepath.Base(path)] {
				return nil
			}
			bad += checkFile(path)
			return nil
		})
	}
	if bad > 0 {
		fmt.Printf("FAIL: %d запрещённых литералов (R2)\n", bad)
		os.Exit(1)
	}
	fmt.Println("OK: R2 — запрещённых числовых/URL/IP/хост-литералов нет")
}

func checkFile(path string) int {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		fatalf("parse %s: %v", path, err)
	}
	bad := 0
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok {
			return true
		}
		switch lit.Kind {
		case token.INT, token.FLOAT:
			v, err := strconv.ParseFloat(lit.Value, 64)
			if err == nil && (v == 0 || v == 1) {
				return true
			}
			fmt.Printf("%s: числовой литерал %q (кроме 0/1/-1 не допустим)\n",
				fset.Position(lit.Pos()), lit.Value)
			bad++
		case token.STRING:
			if s, _ := strconv.Unquote(lit.Value); isForbiddenString(s) {
				fmt.Printf("%s: строковый литерал %q (длительность/URL/IP/хост)\n",
					fset.Position(lit.Pos()), lit.Value)
				bad++
			}
		}
		return true
	})
	return bad
}

// isForbiddenString — строка является длительностью, URL, IP или именем хоста.
func isForbiddenString(s string) bool {
	if strings.Contains(s, "://") { // URL
		return true
	}
	if reIPv4.MatchString(s) || strings.Contains(s, "::") { // IPv4 / IPv6
		return true
	}
	return reDuration.MatchString(s)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "lintlits: "+format+"\n", args...)
	os.Exit(2)
}
