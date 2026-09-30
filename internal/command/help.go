package command

import "strings"

// helpText — ответ /help (под строкой у веба, сообщением в Telegram).
// Без аргументов — список команд; с аргументом — сигнатура одной команды.
func helpText(args []string) string {
	if len(args) >= 1 {
		if c, ok := Find(args[0]); ok {
			sig := "/" + c.Signature
			if c.Confirm != "" {
				sig += " (подтверждение)"
			}
			return sig + " — " + c.Description
		}
		return "неизвестная команда: " + args[0]
	}
	var b strings.Builder
	for _, c := range registry {
		if c.Hidden {
			continue
		}
		b.WriteString("/" + c.Name + " — " + c.Description + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// Docs — docs/commands.md, генерируется из реестра (раздел 12: «справка
// /help и docs/commands.md генерируются из реестра команд»).
func Docs() string {
	var b strings.Builder
	b.WriteString("# Команды (раздел 12)\n\n")
	b.WriteString("Командная строка «/» и горячие клавиши. Разбор, выполнение и подсказки — один " +
		"пакет `internal/command`; его используют веб и Telegram. `<t>` — имя, `sid` или " +
		"`узел:tmux_session`.\n\n")
	b.WriteString("| Команда | Аргументы | Действие |\n|---|---|---|\n")
	for _, c := range registry {
		if c.Hidden {
			continue
		}
		rest := strings.TrimPrefix(c.Signature, c.Name)
		rest = strings.TrimPrefix(rest, " ")
		desc := c.Description
		if c.Confirm != "" {
			desc += " (подтверждение)"
		}
		b.WriteString("| /" + c.Name + " | " + rest + " | " + desc + " |\n")
	}
	return b.String()
}
