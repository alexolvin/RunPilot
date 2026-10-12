package fakeorch

// templates — именованные формы ответа (фикстуры ТЗ Relay §9).
var templates = map[string]answer{
	// text_only — только текст, блока для агента нет.
	"text_only": {Blocks: []Block{
		{Kind: "text", Text: "Просто текстовый ответ без блоков."},
	}},
	// agent_block — текст «Ответ агенту» + code-блок.
	"agent_block": {Blocks: []Block{
		{Kind: "text", Text: "Ответ агенту:"},
		{Kind: "code", Text: "Сделай шаг 2."},
	}},
	// agent_block_stop — code-блок со стоп-словом.
	"agent_block_stop": {Blocks: []Block{
		{Kind: "text", Text: "Ответ агенту:"},
		{Kind: "code", Text: "RUNPILOT_STOP"},
	}},
	// two_agent_blocks — два блока, берётся последний.
	"two_agent_blocks": {Blocks: []Block{
		{Kind: "text", Text: "Ответ агенту:"},
		{Kind: "code", Text: "Первый блок."},
		{Kind: "text", Text: "Ответ агенту:"},
		{Kind: "code", Text: "Второй блок."},
	}},
	// operator_and_agent_blocks — блок оператору + блок агенту.
	"operator_and_agent_blocks": {Blocks: []Block{
		{Kind: "text", Text: "Оператору:"},
		{Kind: "code", Text: "Для оператора."},
		{Kind: "text", Text: "Ответ агенту:"},
		{Kind: "code", Text: "Для агента."},
	}},
	// no_block — код без заголовка «агенту».
	"no_block": {Blocks: []Block{
		{Kind: "text", Text: "Вот результат:"},
		{Kind: "code", Text: "plain code"},
	}},
	// rate_limit — текст лимита.
	"rate_limit": {Blocks: []Block{
		{Kind: "text", Text: "FAKEORCH_RATE_LIMIT: замедлитесь."},
	}},
	// error — текст ошибки.
	"error": {Blocks: []Block{
		{Kind: "text", Text: "FAKEORCH_ERROR: что-то сломалось."},
	}},
}
