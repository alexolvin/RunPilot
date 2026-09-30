// check-literals — строгое правило 2 ТЗ: в коде планировщика, шлюза и
// детектора нет литералов порогов/таймаутов/размеров/лимитов.
//
// Сканит не-тестовые .go-файлы трёх пакетов через AST и требует, чтобы
// каждое целое/вещественное литеральное значение было 0 или 1
// (идентичные значения, не пороги). Запущено как CI-шаг.
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

var packages = []string{
	"internal/scheduler",
	"internal/gateway",
	"internal/detect",
}

func main() {
	bad := 0
	for _, dir := range packages {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			fatalf("чтение %s: %v", dir, err)
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			path := filepath.Join(dir, name)
			bad += checkFile(path)
		}
	}
	if bad > 0 {
		fmt.Printf("FAIL: %d чужих числовых литералов в %v\n", bad, packages)
		os.Exit(1)
	}
	fmt.Println("OK: в scheduler/gateway/detect нет числовых литералов, кроме 0 и 1")
}

func checkFile(path string) int {
	bad := 0
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		fatalf("parse %s: %v", path, err)
	}
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok {
			return true
		}
		if lit.Kind != token.INT && lit.Kind != token.FLOAT {
			return true
		}
		if lit.Kind == token.INT {
			if v, err := strconv.ParseInt(lit.Value, 0, 64); err == nil && (v == 0 || v == 1) {
				return true
			}
		}
		fmt.Printf("%s: лишнее числовое литеральное значение %q\n",
			fset.Position(lit.Pos()), lit.Value)
		bad++
		return true
	})
	return bad
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "check-literals: "+format+"\n", args...)
	os.Exit(2)
}
