// Command linthosts — R2: конкретные CGNAT-адреса 100.64.0.0/10 и имена
// реальных машин (список hosts ниже) не должны попадать в код; допустимо
// в docs/, testdata/ и *_test.go. Диапазон-allowlist «100.64.0.0/10» (CIDR)
// — не адрес машины, не флагится. Любая находка — FAIL.
//
// hosts — имена ВАШИХ реальных машин. В публичной версии список пуст:
// реальные имена нельзя коммитить. Заполните его локально (и не коммитьте),
// чтобы линтер не пустил ваши имена в код.
package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	// ← добавьте сюда имена своих реальных машин локально (не коммитить).
	hosts   = []string{}
	// 100.64.0.0/10: 100.64.0.0 – 100.127.255.255 (конкретный адрес машины).
	reCgnat = regexp.MustCompile(`100\.(6[4-9]|[7-9][0-9]|1[01][0-9]|12[0-7])\.\d{1,3}\.\d{1,3}`)
)

func main() {
	bad := 0
	_ = filepath.WalkDir(".", func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || isSkipped(path) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil || isBinary(data) {
			return nil
		}
		for i, line := range strings.Split(string(data), "\n") {
			if reason := hitReason(line); reason != "" {
				fmt.Printf("%s:%d: %s\n", path, i+1, reason)
				bad++
			}
		}
		return nil
	})
	if bad > 0 {
		fmt.Printf("FAIL: %d упоминаний реальных хостов/CGNAT-адресов в коде\n", bad)
		os.Exit(1)
	}
	fmt.Println("OK: в коде нет имён реальных хостов и CGNAT-адресов машин")
}

// isSkipped — VCS, спеку (ТЗ), документацию, сам линтер и разрешённые файлы.
func isSkipped(path string) bool {
	base := filepath.Base(path)
	for _, skip := range []string{".git", "docs", "testdata", "node_modules", ".qwen", ".agent", "tools/linthosts"} {
		if strings.HasPrefix(path, skip+string(filepath.Separator)) || path == skip {
			return true
		}
	}
	// Маркдаун — документация; .lock — сгенерированные манифесты.
	if strings.HasSuffix(base, ".md") || strings.HasSuffix(base, ".lock") {
		return true
	}
	return strings.HasSuffix(base, "_test.go")
}

// isBinary — собраный бинарник/артефакт (ноль-байт в начале) не код.
func isBinary(data []byte) bool {
	n := len(data)
	if n > 8192 {
		n = 8192
	}
	return bytes.IndexByte(data[:n], 0) >= 0
}

// hitReason — причина находки в строке; пусто, если хостов/адресов нет.
func hitReason(line string) string {
	if m := reCgnat.FindStringIndex(line); m != nil && !isCidr(line, m[1]) {
		return "CGNAT-адрес машины (100.64.0.0/10) в коде"
	}
	for _, h := range hosts {
		if strings.Contains(line, h) {
			return "имя реальной машины в коде"
		}
	}
	return ""
}

// isCidr — IP входит в CIDR-диапазон («100.64.0.0/10»): это allowlist,
// а не адрес конкретной машины.
func isCidr(line string, ipEnd int) bool {
	rest := strings.TrimSpace(line[ipEnd:])
	return strings.HasPrefix(rest, "/")
}
