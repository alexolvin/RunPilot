// Package fakellm — константы (таблица R2): параметры псевдо-LLM.
package fakellm

const (
	// defaultChunks — число чанков ответа по умолчанию.
	defaultChunks = 5
	// genTokensPerReq — псевдо-генерационных токенов за запрос (/metrics).
	genTokensPerReq = 10
	// promptTokensPerReq — псевдо-промпт-токенов за запрос (/metrics).
	promptTokensPerReq = 5
	// chunkHalfDiv — делитель «половина чанков + 1» при отказе после начала.
	chunkHalfDiv = 2
)
