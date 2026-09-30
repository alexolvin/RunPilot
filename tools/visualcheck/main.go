// Command visualcheck — проверка VISUAL.md (R4, раздел 19.4).
//
// Успех (exit 0) только если:
//  1. в docs/evidence/<stage>/VISUAL.md перечислены ВСЕ файлы
//     docs/evidence/<stage>/screens/manifest.json;
//  2. у строки каждого файла ровно 12 отметок (V1–V12);
//  3. commit в VISUAL.md равен commit из manifest.json (одна пара «снимки+разбор»);
//  4. этот commit — предок (или равен) git HEAD (снимки не старше кода этапа).
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var reHash = regexp.MustCompile(`[0-9a-f]{40}`)
var reMark = regexp.MustCompile(`[✅➖⚠❌]`)

func main() {
	stage := flag.String("stage", "W3", "этап (W<N>)")
	evidence := flag.String("evidence", "docs/evidence", "каталог evidence")
	flag.Parse()

	stageDir := filepath.Join(*evidence, *stage)
	manifestPath := filepath.Join(stageDir, "screens", "manifest.json")
	visualPath := filepath.Join(stageDir, "VISUAL.md")

	data, err := os.ReadFile(manifestPath)
	if err != nil {
		fatalf("manifest: %v", err)
	}
	var manifest struct {
		Commit string   `json:"commit"`
		Files  []string `json:"files"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		fatalf("manifest json: %v", err)
	}
	if len(manifest.Files) == 0 {
		fatalf("manifest: пустой список файлов")
	}
	vtext, err := os.ReadFile(visualPath)
	if err != nil {
		fatalf("VISUAL.md: %v", err)
	}

	fails := 0
	// (3) commit в VISUAL.md = commit из manifest.
	visualCommit := ""
	for _, line := range strings.Split(string(vtext), "\n") {
		if strings.Contains(strings.ToLower(line), "commit") {
			if m := reHash.FindString(line); m != "" {
				visualCommit = m
				break
			}
		}
	}
	if visualCommit == "" {
		fmt.Println("FAIL: в VISUAL.md не найден commit (40 hex)")
		fails++
	} else if visualCommit != manifest.Commit {
		fmt.Printf("FAIL: commit VISUAL.md %s ≠ manifest %s\n", short(visualCommit), short(manifest.Commit))
		fails++
	}
	// (4) commit — предок HEAD.
	if visualCommit != "" {
		if err := exec.Command("git", "merge-base", "--is-ancestor", visualCommit, "HEAD").Run(); err != nil {
			fmt.Printf("FAIL: commit %s не предок (и не равен) HEAD\n", short(visualCommit))
			fails++
		}
	}

	// (1)+(2) все файлы в VISUAL.md, по 12 отметок в строке.
	rows := strings.Split(string(vtext), "\n")
	for _, f := range manifest.Files {
		row := ""
		for _, line := range rows {
			if strings.HasPrefix(strings.TrimSpace(line), "|") && strings.Contains(line, f) {
				row = line
				break
			}
		}
		if row == "" {
			fmt.Printf("FAIL: файл %s не в VISUAL.md\n", f)
			fails++
			continue
		}
		// Ячейка с наибольшей плотностью отметок = колонка V1–V12.
		best := 0
		for _, cell := range strings.Split(row, "|") {
			if n := len(reMark.FindAllString(cell, -1)); n > best {
				best = n
			}
		}
		if best != 12 {
			fmt.Printf("FAIL: %s — %d отметок (нужно 12)\n", f, best)
			fails++
		}
	}

	if fails > 0 {
		fmt.Printf("visualcheck %s: FAIL (%d)\n", *stage, fails)
		os.Exit(1)
	}
	fmt.Printf("visualcheck %s: OK — %d файлов, 12 отметок в строке, commit %s = предок HEAD\n",
		*stage, len(manifest.Files), short(manifest.Commit))
}

func short(h string) string {
	if len(h) >= 7 {
		return h[:7]
	}
	return h
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "visualcheck: "+format+"\n", args...)
	os.Exit(2)
}
