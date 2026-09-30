package command

import (
	"errors"
	"strings"
)

// Ошибки разбора.
var (
	ErrEmpty = errors.New("пустая строка")
	// ErrNotCommand — строка без «/» (на карточке сессии это текст задания,
	// а не команда; вызывающий различает их по месту ввода, не по Parse).
	ErrNotCommand = errors.New("не команда")
)

// UnknownCommand — имя команды не в реестре.
type UnknownCommand struct{ Name string }

func (e *UnknownCommand) Error() string { return "неизвестная команда: " + e.Name }

// Parsed — результат разбора строки.
type Parsed struct {
	Cmd  *Command
	Args []string // все токены после имени команды
	Text string   // для go/send: текст задания (токены с 2-го склеены)
	Raw  string   // исходная строка без ведущего «/»
}

// Parse — строка «/имя аргументы…» → команда + аргументы.
//
// <текст…> (go/send) — всё после целевого аргумента, включая пробелы; каталог
// (new) — один токен (путь без пробелов; команды — по токенам, раздел 12).
func Parse(line string) (*Parsed, error) {
	s := strings.TrimSpace(line)
	if s == "" {
		return nil, ErrEmpty
	}
	if !strings.HasPrefix(s, "/") {
		return nil, ErrNotCommand
	}
	s = strings.TrimPrefix(s, "/")
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return nil, ErrEmpty
	}
	cmd, ok := Find(fields[0])
	if !ok {
		return nil, &UnknownCommand{Name: fields[0]}
	}
	args := fields[1:]
	p := &Parsed{Cmd: cmd, Args: args, Raw: s}
	if cmd.Name == "go" || cmd.Name == "send" {
		if len(args) > 1 {
			p.Text = strings.Join(args[1:], " ")
		}
	}
	return p, nil
}
