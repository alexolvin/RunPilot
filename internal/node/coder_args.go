package node

// CoderArgsFromEnv — явные аргументы qwen, которые обязан передать запуск
// управляемого кодера, чтобы маршрут (auth + модель + baseUrl) был задан
// ТОЛЬКО аргументами процесса, без правки settings.json (W13 этап 7):
//
//   - --auth-type openai           (перекрывает security.auth.selectedType из settings)
//   - --model <OPENAI_MODEL>       (model_alias)
//   - --openai-base-url <OPENAI_BASE_URL> (<gateway>/s/<sid>/v1)
//
// Qwen Code резолвит модель слоями: --model > settings.model.name >
// env OPENAI_MODEL; auth: --auth-type > security.auth.selectedType. Явные
// аргументы имеют высший приоритет и гарантируют, что запросы идут через
// шлюз, даже если settings.json имеет model.name, совпадающий с id записи
// modelProviders, и selectedType != openai (худший случай оператора).
//
// Возвращает пустой срез, если в env нет OPENAI_MODEL и OPENAI_BASE_URL
// (профиль без шлюза) — аргументы не добавляются.
func CoderArgsFromEnv(env map[string]string) []string {
	model := env["OPENAI_MODEL"]
	base := env["OPENAI_BASE_URL"]
	if model == "" && base == "" {
		return nil
	}
	args := []string{"--auth-type", "openai"}
	if model != "" {
		args = append(args, "--model", model)
	}
	if base != "" {
		args = append(args, "--openai-base-url", base)
	}
	return args
}
