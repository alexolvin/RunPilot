package node

import (
	"context"
	"strings"
	"testing"

	"runpilot/internal/proto"
)

// pasteMsg — команда вставки.
func pasteMsg(paneID, text string) proto.Msg {
	m := proto.New(proto.KindPaste)
	m.CmdID, m.PaneID, m.Text = "c9", paneID, text
	return m
}

// TestPasteGone — панели нет.
func TestPasteGone(t *testing.T) {
	re := testRegexps(t)
	s := &screenExec{fakeExec: &fakeExec{}, paneSID: "S1", paneGone: true}
	runner := s.run(re)
	r, err := runner.HandleCommand(context.Background(), "default", pasteMsg("%10", "hello"))
	if err != nil {
		t.Fatal(err)
	}
	if r.Result != ResPaneGone {
		t.Fatalf("result = %s, хочу PANE_GONE", r.Result)
	}
}

// TestPasteMismatch — вставка не совпала с извлечённым вводом.
func TestPasteMismatch(t *testing.T) {
	re := testRegexps(t)
	s := &screenExec{fakeExec: &fakeExec{}, screen: idleScreen, paneSID: "S1"}
	runner := s.run(re)
	r, err := runner.HandleCommand(context.Background(), "default", pasteMsg("%10", "hello task"))
	if err != nil {
		t.Fatal(err)
	}
	if r.Result != ResPasteMismatch {
		t.Fatalf("result = %s, хочу PASTE_MISMATCH", r.Result)
	}
	// Механика: load-buffer (stdin) + paste-buffer -p -d с буфером runpilot-<cmd_id>.
	if s.callCount("load-buffer") != 1 || !strings.Contains(strings.Join(lastArgs(s, "load-buffer"), " "), "runpilot-c9") {
		t.Fatalf("load-buffer: %d, хочу 1 с буфером runpilot-c9", s.callCount("load-buffer"))
	}
	if s.callCount("paste-buffer") != 1 || !strings.Contains(strings.Join(lastArgs(s, "paste-buffer"), " "), "-p -d") {
		t.Fatalf("paste-buffer: %d, хочу 1 с -p -d", s.callCount("paste-buffer"))
	}
}

// TestPasteSubmitted — панель ушла в BUSY (критический дефект).
func TestPasteSubmitted(t *testing.T) {
	re := testRegexps(t)
	s := &screenExec{fakeExec: &fakeExec{}, screen: busyScreen, paneSID: "S1"}
	runner := s.run(re)
	r, err := runner.HandleCommand(context.Background(), "default", pasteMsg("%10", "hello task"))
	if err != nil {
		t.Fatal(err)
	}
	if r.Result != ResPasteSubmitted {
		t.Fatalf("result = %s, хочу PASTE_SUBMITTED", r.Result)
	}
}

// TestPastePasted — извлечённый ввод совпал с вставленным текстом.
func TestPastePasted(t *testing.T) {
	re := testRegexps(t)
	s := &screenExec{fakeExec: &fakeExec{}, screen: idleInputScreen, paneSID: "S1"}
	runner := s.run(re)
	r, err := runner.HandleCommand(context.Background(), "default", pasteMsg("%10", "Однострочный тестовый промпт"))
	if err != nil {
		t.Fatal(err)
	}
	if r.Result != ResPasted {
		t.Fatalf("result = %s, хочу PASTED", r.Result)
	}
}

// TestPasteMultiLine — многострочный ввод совпал побайто.
func TestPasteMultiLine(t *testing.T) {
	re := testRegexps(t)
	screen := `────────────────────────────────────────────────
> Первая строка
Вторая строка ` + "\u200b" + `
────────────────────────────────────────────────
  ➜ /tmp/proj · qwen3.6-27b
`
	s := &screenExec{fakeExec: &fakeExec{}, screen: screen, paneSID: "S1"}
	runner := s.run(re)
	r, err := runner.HandleCommand(context.Background(), "default", pasteMsg("%10", "Первая строка\nВторая строка"))
	if err != nil {
		t.Fatal(err)
	}
	if r.Result != ResPasted {
		t.Fatalf("result = %s, хочу PASTED", r.Result)
	}
}

// TestPasteWrapped — перенос строки в узком терминале: Qwen рендерит
// continuation отдельной строкой с отступом, capture-pane -J жёсткий перенос
// не склеивает. Сверка устойчива к переносам (сравнение без пробельных).
func TestPasteWrapped(t *testing.T) {
	re := testRegexps(t)
	screen := `────────────────────────────────────────────────
> Запусти web интерфейс и сообщи на каком адресе tailnet он будет
  доступен ` + "\u200b" + `
────────────────────────────────────────────────
  ➜ /tmp/proj · qwen3.6-27b
`
	s := &screenExec{fakeExec: &fakeExec{}, screen: screen, paneSID: "S1"}
	runner := s.run(re)
	r, err := runner.HandleCommand(context.Background(), "default",
		pasteMsg("%10", "Запусти web интерфейс и сообщи на каком адресе tailnet он будет доступен"))
	if err != nil {
		t.Fatal(err)
	}
	if r.Result != ResPasted {
		t.Fatalf("result = %s, хочу PASTED (перенос строки должен игнорироваться)", r.Result)
	}
}

func approveMsg(paneID, option string) proto.Msg {
	m := proto.New(proto.KindApprove)
	m.CmdID, m.PaneID, m.Text = "c10", paneID, option
	return m
}

// TestApproveSuccess — панель в PROMPT, клавиши варианта ввели её из PROMPT.
func TestApproveSuccess(t *testing.T) {
	re := testRegexps(t)
	s := &screenExec{fakeExec: &fakeExec{}, screen: promptScreen, screenAfter: idleScreen, paneSID: "S1"}
	runner := s.run(re)
	r, err := runner.HandleCommand(context.Background(), "default", approveMsg("%10", "allow"))
	if err != nil {
		t.Fatal(err)
	}
	if r.Result != ResApproved {
		t.Fatalf("result = %s, хочу APPROVED", r.Result)
	}
	// Клавиша варианта из профиля (allow → y) ушла в панель.
	if s.callCount("send-keys") < 1 {
		t.Fatalf("send-keys: %d, хочу >= 1", s.callCount("send-keys"))
	}
}

// TestApproveNotPrompt — панель не в PROMPT → APPROVE_NO_EFFECT, без send-keys.
func TestApproveNotPrompt(t *testing.T) {
	re := testRegexps(t)
	s := &screenExec{fakeExec: &fakeExec{}, screen: idleScreen, paneSID: "S1"}
	runner := s.run(re)
	r, err := runner.HandleCommand(context.Background(), "default", approveMsg("%10", "allow"))
	if err != nil {
		t.Fatal(err)
	}
	if r.Result != ResApproveNoEffect {
		t.Fatalf("result = %s, хочу APPROVE_NO_EFFECT", r.Result)
	}
	if s.callCount("send-keys") != 0 {
		t.Fatalf("send-keys: %d, хочу 0 (не PROMPT)", s.callCount("send-keys"))
	}
}

// TestApproveUnknownOption — неизвестный вариант → APPROVE_NO_EFFECT.
func TestApproveUnknownOption(t *testing.T) {
	re := testRegexps(t)
	s := &screenExec{fakeExec: &fakeExec{}, screen: promptScreen, paneSID: "S1"}
	runner := s.run(re)
	r, err := runner.HandleCommand(context.Background(), "default", approveMsg("%10", "bogus"))
	if err != nil {
		t.Fatal(err)
	}
	if r.Result != ResApproveNoEffect {
		t.Fatalf("result = %s, хочу APPROVE_NO_EFFECT", r.Result)
	}
}

func compressMsg(paneID string) proto.Msg {
	m := proto.New(proto.KindCompress)
	m.CmdID, m.PaneID = "c11", paneID
	return m
}

// TestCompress — панель IDLE с пустым вводом: compress_text вставлен + submit.
func TestCompress(t *testing.T) {
	re := testRegexps(t)
	s := &screenExec{fakeExec: &fakeExec{}, screen: idleScreen, paneSID: "S1"}
	runner := s.run(re)
	r, err := runner.HandleCommand(context.Background(), "default", compressMsg("%10"))
	if err != nil {
		t.Fatal(err)
	}
	if r.Result != ResSent || r.Detail != "compress" {
		t.Fatalf("result = %s/%s, хочу SENT/compress", r.Result, r.Detail)
	}
	// compress_text вставлен через load-buffer + paste-buffer.
	if s.callCount("load-buffer") != 1 || s.callCount("paste-buffer") != 1 {
		t.Fatalf("load/paste = %d/%d, хочу 1/1", s.callCount("load-buffer"), s.callCount("paste-buffer"))
	}
}

// TestCompressNotIdle — панель не IDLE (ввод непуст) → отказ без вставки.
func TestCompressNotIdle(t *testing.T) {
	re := testRegexps(t)
	s := &screenExec{fakeExec: &fakeExec{}, screen: idleInputScreen, paneSID: "S1"}
	runner := s.run(re)
	r, err := runner.HandleCommand(context.Background(), "default", compressMsg("%10"))
	if err != nil {
		t.Fatal(err)
	}
	if r.Result != ResApproveNoEffect {
		t.Fatalf("result = %s, хочу APPROVE_NO_EFFECT (ввод не пуст)", r.Result)
	}
	if s.callCount("paste-buffer") != 0 {
		t.Fatalf("paste-buffer: %d, хочу 0 (не IDLE)", s.callCount("paste-buffer"))
	}
}

// lastArgs — аргументы последнего вызова команды с подстрокой.
func lastArgs(s *screenExec, substr string) []string {
	s.fakeExec.mu.Lock()
	defer s.fakeExec.mu.Unlock()
	var out []string
	for _, c := range s.fakeExec.calls {
		if strings.Contains(strings.Join(c, " "), substr) {
			out = c
		}
	}
	return out
}
