// Command vendorweb — W3: загрузка фиксированных версий ESM-библиотек, шрифтов
// и иконок из npm registry в web/vendor/, web/fonts/, web/icons/ + запись
// web/vendor/VERSIONS.txt (SHA-256 каждого файла).
//
// Обновление зависимостей фронтенда — только здесь: меняется spec (версия/файлы),
// запускается `make vendor-web`, git фиксирует новые хеши. CI сверяет хеши.
// Внешних хостов в рантайме веба нет (раздел 16); загрузка — только на этапе
// сборки (vendor-web).
package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// file — одна выгружаемая единица: файл внутри npm-тарбола → путь в репозитории.
type file struct {
	pkg     string // имя npm-пакета (scope-ок: @preact/signals)
	version string // точная версия
	inTar   string // путь внутри тарбола (package/...)
	dest    string // путь в репозитории (web/...)
}

var spec = []file{
	// ESM-модули (раздел 18). Preact 10 + htm (сигналы @preact/signals — W4,
	// с реальным SSE-стором; каркас W3 — на hooks).
	{pkg: "preact", version: "10.29.8", inTar: "package/dist/preact.module.js", dest: "web/vendor/preact.module.js"},
	{pkg: "preact", version: "10.29.8", inTar: "package/hooks/dist/hooks.module.js", dest: "web/vendor/preact-hooks.module.js"},
	{pkg: "htm", version: "3.1.1", inTar: "package/dist/htm.module.js", dest: "web/vendor/htm.module.js"},
	// Терминал в браузере (13.4): xterm.js ESM + CSS (v6: CSS без @font-face —
	// шрифт берётся из fontFamily; аддоны вынесены в отдельные @xterm/* пакеты,
	// для W8 достаточно ядра Terminal).
	{pkg: "@xterm/xterm", version: "6.0.0", inTar: "package/lib/xterm.mjs", dest: "web/vendor/xterm.mjs"},
	{pkg: "@xterm/xterm", version: "6.0.0", inTar: "package/css/xterm.css", dest: "web/vendor/xterm.css"},
}

func fontSpecs() []file {
	var out []file
	// Inter: 400/500/600; поднаборы latin/cyrillic/cyrillic-ext (UI на русском).
	for _, w := range []string{"400", "500", "600"} {
		for _, s := range []string{"latin", "cyrillic", "cyrillic-ext"} {
			name := fmt.Sprintf("inter-%s-%s-normal.woff2", s, w)
			out = append(out, file{
				pkg: "@fontsource/inter", version: "5.3.0",
				inTar: "package/files/" + name, dest: "web/fonts/" + name,
			})
		}
	}
	// IBM Plex Mono: 400/500; те же поднаборы (технические данные).
	for _, w := range []string{"400", "500"} {
		for _, s := range []string{"latin", "cyrillic", "cyrillic-ext"} {
			name := fmt.Sprintf("ibm-plex-mono-%s-%s-normal.woff2", s, w)
			out = append(out, file{
				pkg: "@fontsource/ibm-plex-mono", version: "5.3.0",
				inTar: "package/files/" + name, dest: "web/fonts/" + name,
			})
		}
	}
	return out
}

func iconSpecs() []file {
	// Lucide SVG (lucide-static) — только используемые в каркасе (W3).
	names := []string{
		"list-todo", "users", "server", "monitor", "file-text", "activity",
		"settings", "bell", "sun", "moon", "search", "menu", "plus",
		"more-vertical", "triangle-alert", "inbox", "command", "x", "pause", "play",
	}
	var out []file
	for _, n := range names {
		out = append(out, file{
			pkg: "lucide-static", version: "1.48.0",
			inTar: "package/icons/" + n + ".svg", dest: "web/icons/" + n + ".svg",
		})
	}
	return out
}

func main() {
	all := append(append(spec, fontSpecs()...), iconSpecs()...)
	if err := vendor(all); err != nil {
		fmt.Fprintln(os.Stderr, "vendorweb:", err)
		os.Exit(1)
	}
}

func vendor(files []file) error {
	type entry struct{ sha, dest string }
	var entries []entry
	for _, f := range files {
		data, err := tarFile(f)
		if err != nil {
			return fmt.Errorf("%s@%s %s: %w", f.pkg, f.version, f.inTar, err)
		}
		if err := writeDest(f.dest, data); err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		entries = append(entries, entry{hex.EncodeToString(sum[:]), f.dest})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].dest < entries[j].dest })
	var b strings.Builder
	for _, e := range entries {
		fmt.Fprintf(&b, "%s  %s\n", e.sha, e.dest)
	}
	if err := os.MkdirAll(filepath.Dir("web/vendor/VERSIONS.txt"), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile("web/vendor/VERSIONS.txt", []byte(b.String()), 0o644); err != nil {
		return err
	}
	fmt.Printf("OK: vendor-web — %d файлов в web/ (хеши в web/vendor/VERSIONS.txt)\n", len(entries))
	return nil
}

// tarFile — скачивает тарбол npm-пакета (кэш в TEMP) и извлекает f.inTar.
func tarFile(f file) ([]byte, error) {
	url := pkgTarURL(f.pkg, f.version)
	cache := filepath.Join(os.TempDir(), "runpilot-vendorweb-"+strings.NewReplacer("/", "-", "@", "").Replace(f.pkg+"-"+f.version)+".tgz")
	if _, err := os.Stat(cache); err != nil {
		data, err := download(url)
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(cache, data, 0o644); err != nil {
			return nil, err
		}
	}
	tr, data, err := openTarGz(cache)
	if err != nil {
		return nil, err
	}
	defer data.Close()
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil, fmt.Errorf("файл %s не найден в %s", f.inTar, f.pkg)
		}
		if err != nil {
			return nil, err
		}
		if hdr.Name != f.inTar {
			continue
		}
		buf, err := io.ReadAll(tr)
		if err != nil {
			return nil, err
		}
		return buf, nil
	}
}

func pkgTarURL(pkg, ver string) string {
	base := pkg
	if i := strings.Index(pkg, "/"); i >= 0 {
		base = pkg[i+1:]
	}
	return "https://registry.npmjs.org/" + pkg + "/-/" + base + "-" + ver + ".tgz"
}

func download(url string) ([]byte, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d на %s", resp.StatusCode, url)
	}
	return io.ReadAll(resp.Body)
}

func openTarGz(path string) (*tar.Reader, io.ReadCloser, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	gz, err := gzip.NewReader(f)
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	return tar.NewReader(gz), f, nil
}

func writeDest(dest string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if strings.HasSuffix(dest, ".js") || strings.HasSuffix(dest, ".mjs") {
		mode = os.FileMode(0o644)
	}
	return os.WriteFile(dest, data, mode)
}
