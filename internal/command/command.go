// Package command — командная строка «/» (v2 раздел 12): единый разбор,
// выполнение и подсказки для веба и Telegram. Реестр команд — источник для
// /help и docs/commands.md (генерируются, не дублируются).
//
// Разбор (Parse) и подсказки (Complete) — чистые функции без зависимостей.
// Выполнение — через Executor (интерфейс, реализует координатор): пакет не
// знает ни о БД, ни об узлах, ни о планировщике — только операции оператора.
package command

// Kind — вид аргумента (для подсказок и валидации).
type Kind string

const (
	KindTarget Kind = "target" // <t>: имя, sid или узел:tmux_session
	KindText   Kind = "text"   // свободный текст задания
	KindClass  Kind = "class"  // resume|high|normal|low
	KindServer Kind = "server" // имя сервера
	KindNode   Kind = "node"   // имя узла
	KindOption Kind = "option" // фиксированный набор (drain|undrain|…)
	KindOnOff  Kind = "onoff"  // on|off
	KindAfter  Kind = "after"  // after:<t>
	KindName   Kind = "name"   // name:N
)

// Arg — аргумент команды.
type Arg struct {
	Name     string // подпись в сигнатуре: «<t>», «[класс]», «[pin:S|prefer:S]»
	Kind     Kind
	Required bool
}

// Command — запись реестра (таблица раздела 12).
type Command struct {
	Name        string // без «/»: "new", "go", …
	Signature   string // полная сигнатура для подсказок/справки
	Description string // короткое описание (справка, docs/commands.md)
	Args        []Arg
	Confirm     string // текст подтверждения ("" = без; /requeue-hold, /clear-queue, /panic)
	Hidden      bool   // не показывать в подсказках (служебные)
}

// registry — все команды раздела 12 (порядок — как в таблице ТЗ).
var registry = []*Command{
	{Name: "new", Signature: "new <узел> <каталог> [name:N] [класс] [pin:S|prefer:S] [auto]",
		Description: "Новая сессия",
		Args: []Arg{{"узел", KindNode, true}, {"каталог", KindText, true},
			{"name:N", KindName, false}, {"класс", KindClass, false},
			{"pin:S|prefer:S", KindOption, false}, {"auto", KindOption, false}}},
	{Name: "go", Signature: "go <t> <текст…>", Description: "Вставить задание и поставить в очередь",
		Args: []Arg{{"t", KindTarget, true}, {"текст", KindText, true}}},
	{Name: "send", Signature: "send <t> <текст…>", Description: "Только вставить задание",
		Args: []Arg{{"t", KindTarget, true}, {"текст", KindText, true}}},
	{Name: "enqueue", Signature: "enqueue <t> [класс] [pin:S|prefer:S] [after:<t2>] [front]",
		Description: "Поставить в очередь",
		Args: []Arg{{"t", KindTarget, true}, {"класс", KindClass, false},
			{"pin:S|prefer:S", KindOption, false}, {"after:<t2>", KindAfter, false},
			{"front", KindOption, false}}},
	{Name: "dequeue", Signature: "dequeue <t>", Description: "Снять с очереди",
		Args: []Arg{{"t", KindTarget, true}}},
	{Name: "requeue", Signature: "requeue <t> [back]", Description: "Вернуть в очередь",
		Args: []Arg{{"t", KindTarget, true}, {"back", KindOption, false}}},
	{Name: "requeue-hold", Signature: "requeue-hold",
		Description: "Вернуть в очередь все требующие внимания",
		Confirm: "Вернуть в очередь все требующие внимания?"},
	{Name: "clear-queue", Signature: "clear-queue", Description: "Очистить очередь",
		Confirm: "Очистить очередь?"},
	{Name: "prio", Signature: "prio <t> <класс>", Description: "Сменить класс",
		Args: []Arg{{"t", KindTarget, true}, {"класс", KindClass, true}}},
	{Name: "pin", Signature: "pin <t> [S]", Description: "Закрепить на сервере",
		Args: []Arg{{"t", KindTarget, true}, {"S", KindServer, false}}},
	{Name: "prefer", Signature: "prefer <t> [S]", Description: "Предпочитать сервер",
		Args: []Arg{{"t", KindTarget, true}, {"S", KindServer, false}}},
	{Name: "unpin", Signature: "unpin <t>", Description: "Снять ограничение",
		Args: []Arg{{"t", KindTarget, true}}},
	{Name: "hold", Signature: "hold <t>", Description: "Удержать сессию",
		Args: []Arg{{"t", KindTarget, true}}},
	{Name: "unhold", Signature: "unhold <t>", Description: "Отпустить сессию",
		Args: []Arg{{"t", KindTarget, true}}},
	{Name: "cancel", Signature: "cancel <t>", Description: "Отменить ход",
		Args: []Arg{{"t", KindTarget, true}}},
	{Name: "approve", Signature: "approve <t> <вариант>", Description: "Ответить на запрос разрешения",
		Args: []Arg{{"t", KindTarget, true}, {"вариант", KindOption, true}}},
	{Name: "compress", Signature: "compress <t>", Description: "Сжать контекст (вставить compress_text и поставить в очередь)",
		Args: []Arg{{"t", KindTarget, true}}},
	{Name: "auto", Signature: "auto <t> on|off", Description: "Автопостановка",
		Args: []Arg{{"t", KindTarget, true}, {"on|off", KindOnOff, true}}},
	{Name: "why", Signature: "why <t>", Description: "Причина ожидания",
		Args: []Arg{{"t", KindTarget, true}}},
	{Name: "open", Signature: "open <t>", Description: "Открыть карточку",
		Args: []Arg{{"t", KindTarget, true}}},
	{Name: "term", Signature: "term <t>", Description: "Открыть терминал",
		Args: []Arg{{"t", KindTarget, true}}},
	{Name: "restart-agent", Signature: "restart-agent <t>", Description: "Перезапустить кодер",
		Args: []Arg{{"t", KindTarget, true}}},
	{Name: "kill-agent", Signature: "kill-agent <t>", Description: "Принудительно завершить кодер",
		Args: []Arg{{"t", KindTarget, true}}},
	{Name: "close", Signature: "close <t>", Description: "Закрыть сессию",
		Args: []Arg{{"t", KindTarget, true}}},
	{Name: "restore", Signature: "restore <t>", Description: "Восстановить закрытую сессию",
		Args: []Arg{{"t", KindTarget, true}}},
	{Name: "server", Signature: "server drain|undrain|disable|enable|cancel-turns|unquarantine <S>",
		Description: "Управление сервером",
		Args: []Arg{{"op", KindOption, true}, {"S", KindServer, true}}},
	{Name: "node", Signature: "node drain|undrain|update <узел>", Description: "Управление узлом",
		Args: []Arg{{"op", KindOption, true}, {"узел", KindNode, true}}},
	{Name: "pause", Signature: "pause", Description: "Пауза очереди"},
	{Name: "resume", Signature: "resume", Description: "Возобновить очередь"},
	{Name: "panic", Signature: "panic", Description: "Аварийная остановка",
		Confirm: "Аварийная остановка: прервать все ходы?"},
	{Name: "unpanic", Signature: "unpanic", Description: "Снять аварийную остановку"},
	{Name: "help", Signature: "help [команда]", Description: "Справка"},
}

// All — все команды реестра (порядок таблицы раздела 12).
func All() []*Command { return registry }

// Find — команда по имени (без «/»).
func Find(name string) (*Command, bool) {
	for _, c := range registry {
		if c.Name == name {
			return c, true
		}
	}
	return nil, false
}
