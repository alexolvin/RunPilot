package command

import "strings"

// DataProvider — живые данные для подсказок (веб: из /state; Telegram — из
// координатора). Сессии — нечётко: по имени и sid (раздел 12).
type DataProvider interface {
	Targets() []TargetValue // сессии: sid, имя, состояние
	Servers() []string      // имена серверов
	Nodes() []string        // имена узлов
	Catalogs() []string     // каталоги узлов (для /new)
}

// TargetValue — сессия для подсказки.
type TargetValue struct {
	SID   string
	Name  string
	State string
}

// Completion — вариант вставки.
type Completion struct {
	Text        string // что дописать
	Description string // подпись
}

// serverOps/nodeOps/classOps — фиксированные наборы (таблица раздела 12).
var (
	serverOps = []string{"drain", "undrain", "disable", "enable", "cancel-turns", "unquarantine"}
	nodeOps   = []string{"drain", "undrain", "update"}
	classOps  = []string{"resume", "high", "normal", "low"}
)

// Complete — подсказки для строки (с «/» или без). Вызывающий держит бюджет
// ui.complete_budget_ms p95.
func Complete(line string, dp DataProvider) []Completion {
	s := strings.TrimSpace(line)
	s = strings.TrimPrefix(s, "/")
	if s == "" || !strings.Contains(s, " ") {
		return completeCommand(s)
	}
	fields := strings.Fields(s)
	cmd, ok := Find(fields[0])
	if !ok {
		return nil
	}
	argPos := len(fields) - two // 0-based позиция введённого аргумента в cmd.Args
	if argPos < 0 || argPos >= len(cmd.Args) {
		return nil
	}
	return completeArg(cmd, argPos, fields[len(fields)-1], dp)
}

func completeCommand(prefix string) []Completion {
	var out []Completion
	for _, c := range registry {
		if c.Hidden || !strings.HasPrefix(c.Name, prefix) {
			continue
		}
		out = append(out, Completion{Text: c.Name + " ", Description: c.Description})
	}
	return out
}

// completeArg — варианты текущего аргумента по виду.
func completeArg(cmd *Command, argPos int, prefix string, dp DataProvider) []Completion {
	// Каталог /new (KindText, 2-й аргумент) — подсказка каталогов узлов.
	if cmd.Name == "new" && argPos == 1 {
		return byPrefix(dp.Catalogs(), prefix)
	}
	// Маркерные префиксы определяют вид независимо от позиции (after:/pin:/prefer:).
	if strings.HasPrefix(prefix, "after:") {
		return withPrefix(completeTargets(strings.TrimPrefix(prefix, "after:"), dp), "after:")
	}
	if strings.HasPrefix(prefix, "pin:") || strings.HasPrefix(prefix, "prefer:") {
		i := strings.IndexRune(prefix, ':')
		return withPrefix(byPrefix(dp.Servers(), prefix[i+1:]), prefix[:i+1])
	}
	switch cmd.Args[argPos].Kind {
	case KindTarget:
		return completeTargets(prefix, dp)
	case KindServer:
		return byPrefix(dp.Servers(), prefix)
	case KindNode:
		return byPrefix(dp.Nodes(), prefix)
	case KindClass:
		return byPrefix(classOps, prefix)
	case KindOnOff:
		return byPrefix([]string{"on", "off"}, prefix)
	case KindAfter:
		rest := prefix
		if r, ok := strings.CutPrefix(prefix, "after:"); ok {
			rest = r
		}
		return withPrefix(completeTargets(rest, dp), "after:")
	case KindOption:
		return completeOption(cmd, argPos, prefix, dp)
	}
	return nil // KindText / KindName — свободный ввод
}

func completeOption(cmd *Command, argPos int, prefix string, dp DataProvider) []Completion {
	switch cmd.Name {
	case "server":
		return byPrefix(serverOps, prefix)
	case "node":
		return byPrefix(nodeOps, prefix)
	case "pin", "prefer":
		if argPos == 1 {
			return byPrefix(dp.Servers(), prefix)
		}
	case "enqueue", "new":
		// pin:S|prefer:S — сервер после префикса «pin:»/«prefer:».
		if i := strings.IndexRune(prefix, ':'); i >= 0 {
			return withPrefix(byPrefix(dp.Servers(), prefix[i+1:]), prefix[:i+1])
		}
	}
	return nil
}

func completeTargets(prefix string, dp DataProvider) []Completion {
	var out []Completion
	for _, t := range dp.Targets() {
		if strings.HasPrefix(t.Name, prefix) || strings.HasPrefix(t.SID, prefix) {
			out = append(out, Completion{Text: t.Name + " ", Description: t.SID + " — " + t.State})
		}
	}
	return out
}

func byPrefix(items []string, prefix string) []Completion {
	var out []Completion
	for _, it := range items {
		if strings.HasPrefix(it, prefix) {
			out = append(out, Completion{Text: it, Description: it})
		}
	}
	return out
}

func withPrefix(list []Completion, pre string) []Completion {
	out := make([]Completion, len(list))
	for i, c := range list {
		out[i] = Completion{Text: pre + c.Text, Description: c.Description}
	}
	return out
}
