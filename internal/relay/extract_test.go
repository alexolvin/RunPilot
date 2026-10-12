package relay

import (
	"testing"

	"runpilot/internal/config"
	"runpilot/internal/orchsite"
)

// relayTest — автомат с конфигурацией по умолчанию.
func relayTest() *Relay {
	return &Relay{cfg: config.Defaults().Relay}
}

// TestExtractAgentBlock — извлечение блока для агента (все формы, ТЗ §11.4–7).
func TestExtractAgentBlock(t *testing.T) {
	r := relayTest()
	cases := []struct {
		name   string
		blocks []orchsite.Block
		want   string
		wantOK bool
	}{
		{
			name:   "agent_block",
			blocks: []orchsite.Block{{Kind: "text", Text: "Ответ агенту:"}, {Kind: "code", Text: "Сделай шаг 2."}},
			want:   "Сделай шаг 2.", wantOK: true,
		},
		{
			name: "two_agent_blocks_последний",
			blocks: []orchsite.Block{
				{Kind: "text", Text: "Ответ агенту:"}, {Kind: "code", Text: "Первый"},
				{Kind: "text", Text: "Ответ агенту:"}, {Kind: "code", Text: "Второй"},
			},
			want: "Второй", wantOK: true,
		},
		{
			name: "operator_and_agent",
			blocks: []orchsite.Block{
				{Kind: "text", Text: "Оператору:"}, {Kind: "code", Text: "Для оператора"},
				{Kind: "text", Text: "Ответ агенту:"}, {Kind: "code", Text: "Для агента"},
			},
			want: "Для агента", wantOK: true,
		},
		{
			name:   "no_block",
			blocks: []orchsite.Block{{Kind: "text", Text: "Вот результат:"}, {Kind: "code", Text: "plain code"}},
			want:   "", wantOK: false,
		},
		{
			name:   "text_only",
			blocks: []orchsite.Block{{Kind: "text", Text: "Просто текст."}},
			want:   "", wantOK: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := r.findAgentBlock(c.blocks)
			if got != c.want {
				t.Fatalf("findAgentBlock = %q, хочу %q", got, c.want)
			}
		})
	}
}

// TestExtractStopWord — стоп-слово → DONE.
func TestExtractStopWord(t *testing.T) {
	r := relayTest()
	blocks := []orchsite.Block{{Kind: "text", Text: "Ответ агенту:"}, {Kind: "code", Text: "RUNPILOT_STOP"}}
	// Эмуляция Extract без страницы: проверяем findAgentBlock + стоп-слово.
	got := r.findAgentBlock(blocks)
	if got != "RUNPILOT_STOP" {
		t.Fatalf("findAgentBlock = %q, хочу стоп-слово", got)
	}
	if got != r.cfg.StopWord {
		t.Fatalf("стоп-слово не совпало: %q != %q", got, r.cfg.StopWord)
	}
}
