package node

// CoderArgsFromEnv — явные аргументы qwen, которые обязателен передать
// при запуске управляемого кодера, чтобы не обойти шлюз:
//
//   - --model <OPENAI_MODEL>            (model_alias)
//   - --openai-base-url <OPENAI_BASE_URL> (<gateway>/s/<sid>/v1)
//
// Qwen Code резолвит модель слоями: --model > settings.model.name >
// env OPENAI_MODEL. Если settings.model.name совпадает с id записи
// modelProviders, qwen берёт baseUrl из этой записи (прямой vLLM) — шлюз
// обойден. Явные аргументы --model и --openai-base-url имеют высший
// приоритет и гарантируют, что запросы идут через шлюз. Env остаётся
// как есть (низший приоритет, но нужен для fallback).
//
// Возвращает пустой срез, если в env нет OPENAI_MODEL и OPENAI_BASE_URL
// (например, профиль без этих переменных) — аргументы не добавляются.
func CoderArgsFromEnv(env map[string]string) []string {
	var args []string
	if model := env["OPENAI_MODEL"]; model != "" {
		args = append(args, "--model", model)
	}
	if base := env["OPENAI_BASE_URL"]; base != "" {
		args = append(args, "--openai-base-url", base)
	}
	return args
}
