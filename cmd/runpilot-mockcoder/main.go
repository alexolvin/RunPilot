// runpilot-mockcoder — тестовый «кодер» для приёмки (раздел 15 ТЗ).
//
// Имитирует экраны Qwen Code: поле ввода с плейсхолдером профиля qwen,
// busy-экран со строкой «esc to cancel», диалог разрешения, WAIT_UI-экраны.
// Ход — N последовательных потоковых POST $OPENAI_BASE_URL/chat/completions
// с паузами «инструментов»; при ошибке — строка API Error и возврат к вводу.
//
// Флаги:
//
//	--turns N          число ходов (по умолчанию 1)
//	--posts N          потоковых POST на ход (по умолчанию 3)
//	--approval-at k    показать диалог разрешения на k-м ходу (0 — выкл)
//	--wait-ui M        connecting|compact|rate_limit — WAIT_UI-экран на старте,
//	                   снимается по нажатию клавиши
//
// Все экраны обязаны классифицироваться детектором qwen на 100%
// (фикстуры testdata/fixtures/mockcoder/).
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const (
	placeholder = "Type your message or @path/to/file"
	modeIdle    = "  ⏸ Ask permissions (shift + tab to cycle)"
	modeBusy    = "  Enter to steer · Ctrl+Q to queue · ⏸ Ask permissions (shift + tab to cycle)"
	// hostLabel — подпись узла в нарисованном хроме (обобщённая, без реальных
	// имён машин — R2/linthosts).
	hostLabel = "node"
)

type coder struct {
	fd           int
	in           []rune
	turnsLeft    int
	postsPerTurn int
	approvalAt   int
	turnNo       int
	ctx          string // строка контекста в статусе (появляется после первого хода)
	baseURL      string
	model        string
	httpCli      *http.Client
}

func main() {
	turns := flag.Int("turns", 1, "число ходов")
	posts := flag.Int("posts", defaultPosts, "потоковых POST на ход")
	approvalAt := flag.Int("approval-at", 0, "диалог разрешения на k-м ходу (0 — выкл)")
	waitUI := flag.String("wait-ui", "", "connecting|compact|rate_limit — WAIT_UI-экран на старте")
	flag.Parse()

	c := &coder{
		fd:           int(os.Stdin.Fd()),
		turnsLeft:    *turns,
		postsPerTurn: *posts,
		approvalAt:   *approvalAt,
		baseURL:      os.Getenv("OPENAI_BASE_URL"),
		model:        os.Getenv("OPENAI_MODEL"),
		httpCli:      &http.Client{},
	}
	if c.model == "" {
		c.model = "mock-model"
	}
	if *waitUI != "" && *waitUI != "connecting" && *waitUI != "compact" && *waitUI != "rate_limit" {
		fatalf("неизвестный --wait-ui %q", *waitUI)
	}

	old, err := rawMode(c.fd)
	if err != nil {
		fatalf("raw-режим терминала: %v", err)
	}
	defer func() { _ = unix.IoctlSetTermios(c.fd, unix.TCSETS, old) }()

	if *waitUI != "" {
		c.showWaitUI(*waitUI)
		_ = c.readByte() // снять экран любой клавишей
	}
	c.run()
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "runpilot-mockcoder: "+format+"\n", args...)
	os.Exit(fatalExitCode)
}

// rawMode переводит fd в raw-режим и возвращает старый termios.
func rawMode(fd int) (*unix.Termios, error) {
	old, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return nil, err
	}
	// Вход — raw (без эха и канонического режима), вывод остаётся
	// обработанным (OPOST/ONLCR): \n превращается в \r\n, без этого
	// многострочный вывод не возвращается в начало строки.
	raw := *old
	raw.Iflag &^= unix.ICRNL | unix.IXON | unix.ISTRIP
	raw.Lflag &^= unix.ECHO | unix.ICANON | unix.ISIG
	raw.Cc[unix.VMIN] = 1
	raw.Cc[unix.VTIME] = 0
	if err := unix.IoctlSetTermios(fd, unix.TCSETS, &raw); err != nil {
		return nil, err
	}
	return old, nil
}

// readByte — один байт из stdin; -1 при EOF/ошибке (кроме EINTR — 0).
func (c *coder) readByte() int {
	var b [1]byte
	n, err := unix.Read(c.fd, b[:])
	if err == unix.EINTR {
		return 0
	}
	if err != nil || n == 0 {
		return -1
	}
	return int(b[0])
}

// border — линия рамки.
func border() string { return strings.Repeat("─", width) }

// status — строка статуса.
func (c *coder) status() string {
	s := "  ➜ mockcoder · " + c.model
	if c.ctx != "" {
		s += " · " + c.ctx
	}
	return s
}

// pad — строка, дополненная пробелами до width.
func pad(s string) string {
	if cols(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-cols(s))
}

// cols — ширина строки в терминальных колонках (все используемые символы
// имеют ширину 1, ZWSP не считается — курсор).
func cols(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}

// screen — перерисовка панели: курсор в угол, очистка, строки сверху
// (как у реального qwen: хром вверху, остальное — пусто).
func (c *coder) screen(lines []string) {
	var b strings.Builder
	b.WriteString("\x1b[H\x1b[2J")
	for i, l := range lines {
		if i > 0 {
			b.WriteString("\r\n")
		}
		b.WriteString(pad(l))
	}
	_, _ = os.Stdout.WriteString(b.String())
}

// inputLine — содержимое строки ввода: плейсхолдер (без курсора) или
// текст + ZWSP-курсор (как в фикстурах qwen).
func (c *coder) inputLine() string {
	if len(c.in) == 0 {
		return ">   " + placeholder + "  "
	}
	return "> " + string(c.in) + "\u200b"
}

// renderInput — перерисовка только строки ввода, курсор в конце текста.
func (c *coder) renderInput() {
	line := c.inputLine()
	_, _ = fmt.Fprintf(os.Stdout, "\x1b[%d;1H%s\x1b[K\x1b[%d;%dH",
		inputRow, line, inputRow, cols(line)+1)
}

// idle — экран простоя (хром + курсор в поле ввода).
func (c *coder) idle() {
	c.screen([]string{border(), c.inputLine(), border(), c.status(), modeIdle})
	c.renderInput()
}

// busy — экран занятой панели.
func (c *coder) busy() {
	c.screen([]string{border(), c.inputLine(), border(), c.status(), modeBusy})
}

// doc — строка документа (попадает в историю терминала).
func (c *coder) doc(line string) {
	_, _ = fmt.Fprintln(os.Stdout, line)
}

func (c *coder) beginDoc() {
	_, _ = fmt.Fprintf(os.Stdout, "\x1b[%d;1H", docRow)
}

// run — главный цикл: ввод, ходы, возврат в idle.
func (c *coder) run() {
	c.idle()
	for {
		b := c.readByte()
		switch b {
		case -1:
			return // EOF (панель убита)
		case ctrlCByte: // Ctrl-C
			return
		case '\r', '\n':
			if len(c.in) > 0 {
				text := string(c.in)
				c.in = nil
				if c.turnsLeft > 0 {
					c.turnsLeft--
					c.turn(text)
				} else {
					// лимит ходов исчерпан: текст сбрасывается в историю
					c.beginDoc()
					c.doc("  > " + text)
					c.doc("  (mockcoder: лимит ходов исчерпан)")
					c.idle()
				}
			} else {
				c.idle()
			}
		case delByte, backspaceByte: // DEL, Backspace
			if len(c.in) > 0 {
				c.in = c.in[:len(c.in)-1]
			}
			c.renderInput()
		case ctrlUByte: // Ctrl-U
			c.in = nil
			c.renderInput()
		default:
			if b >= utf8High && b&utf8Lead2 == utf8Lead2 {
				// многобайтовая UTF-8-последовательность: стартовый байт
				// маскируется по длине (110xxxxx / 1110xxxx / 11110xxx)
				n := utf8Len(b)
				if n > 1 {
					r := rune(b)
					switch n {
					case utf8Len2:
						r &= utf8Mask2
					case utf8Len3:
						r &= utf8Mask3
					case utf8Len4:
						r &= utf8Mask4
					}
					ok := true
					for i := 1; i < n; i++ {
						nb := c.readByte()
						if nb < 0 {
							ok = false
							break
						}
						r = r<<utf8ContShift | rune(nb)&utf8ContMask
					}
					if ok {
						c.in = append(c.in, r)
						c.renderInput()
					}
				}
				continue
			}
			if b >= printableMin {
				c.in = append(c.in, rune(b))
				c.renderInput()
			}
		}
	}
}

// utf8Len — длина UTF-8-символа по стартовому байту.
func utf8Len(b int) int {
	switch {
	case b&utf8High == 0:
		return 1
	case b&utf8Lead3 == utf8Lead2:
		return utf8Len2
	case b&utf8Lead4 == utf8Lead3:
		return utf8Len3
	case b&utf8Lead4Mask == utf8Lead4:
		return utf8Len4
	}
	return 1
}

// turn — один ход: диалог (если назначен), busy-экран, N потоковых POST
// с паузами «инструментов», возврат в idle (при ошибке — API Error).
func (c *coder) turn(text string) {
	if c.approvalAt > 0 && c.turnNo+1 == c.approvalAt {
		if !c.approvalDialog() {
			c.idle()
			return // Esc — ход отменён
		}
	}
	c.turnNo++
	if c.ctx == "" {
		c.ctx = "1.2k Context 3% used"
	}
	c.busy()
	c.beginDoc()
	c.doc("  > " + text)
	c.doc("  Working... (0s · esc to cancel)")
	for i := 1; i <= c.postsPerTurn; i++ {
		chunks, err := c.post(text)
		if err != nil {
			c.doc("  ✕ [API Error: " + err.Error() + "]")
			break
		}
		c.doc(fmt.Sprintf("  ▍ post %d/%d done (%d chunks)", i, c.postsPerTurn, chunks))
		if i < c.postsPerTurn {
			c.doc(fmt.Sprintf("  ⚙ tool %d/%d done", i, c.postsPerTurn-1))
			time.Sleep(toolPause)
		}
	}
	c.idle()
}

// approvalDialog — диалог разрешения в стиле qwen. Enter — разрешить,
// Esc — отменить. Возвращает true, если разрешено.
func (c *coder) approvalDialog() bool {
	c.screen([]string{
		"  ? Shell mock-tool (запустить инструмент)",
		"     mock-tool",
		"   Allow execution of: 'mock-tool'?",
		"   ● 1. Yes, allow once",
		"     2. Always allow run 'mock-tool *' commands in this project",
		"     3. Always allow run 'mock-tool *' commands for this user",
		"     4. No, suggest changes (esc)",
		"",
		"  Waiting for user confirmation...",
	})
	for {
		b := c.readByte()
		if b == -1 || b == ctrlCByte {
			return false
		}
		if b == escByte { // Esc
			return false
		}
		if b == '\r' || b == '\n' {
			return true
		}
	}
}

// showWaitUI — экраны WAIT_UI (раздел 15 ТЗ).
func (c *coder) showWaitUI(mode string) {
	switch mode {
	case "connecting":
		c.screen([]string{
			"  Tips: Try /insight to generate personalized insights from your chat history.",
			"",
			"  ➜ mockcoder · " + c.model + " (" + hostLabel + ")",
			"  Initializing...",
		})
	case "compact":
		c.screen([]string{
			border(),
			">   " + placeholder + "  ",
			border(),
			"  ➜ mockcoder · " + c.model + " · 16.4k Context 100% used   >100% context used",
			modeBusy,
		})
	case "rate_limit":
		c.screen([]string{
			border(),
			">   " + placeholder + "  ",
			border(),
			"  ✕ [API Error: 429 Rate limit reached for requests.] (Retrying in 56s… (attempt 1/10))",
			"  ↻ Retrying in 56s… (attempt 1/10)",
			c.status(),
			modeBusy,
		})
	}
}

// post — один потоковый POST $OPENAI_BASE_URL/chat/completions;
// возвращает число SSE-чанков.
func (c *coder) post(text string) (int, error) {
	if c.baseURL == "" {
		return 0, errors.New("OPENAI_BASE_URL не задан")
	}
	body, err := json.Marshal(map[string]any{
		"model":  c.model,
		"stream": true,
		"messages": []map[string]string{
			{"role": "user", "content": text},
		},
	})
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequest(http.MethodPost,
		strings.TrimRight(c.baseURL, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if key := os.Getenv("OPENAI_API_KEY"); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := c.httpCli.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("%d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
	}
	chunks := 0
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		d := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if d == "[DONE]" {
			break
		}
		chunks++
	}
	return chunks, sc.Err()
}
