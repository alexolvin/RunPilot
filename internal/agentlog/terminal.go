package agentlog

import "strings"

// ParseTerminal — разбор истории терминала (раздел 6.3, запасной источник).
// text — снимок панели (capture-pane -p -J -S -<scrollback>); нормализация —
// как в детекторе (узлом). Файлы из терминала не определяются.
func ParseTerminal(text string, cfg TermConfig) (*Report, error) {
	lines := strings.Split(text, "\n")
	rep := &Report{Source: SourceTerminal}
	if cfg.TerminalMarkerRegex == nil {
		return rep, nil
	}

	// 6.3.2: снизу вверх — строка, совпадающая с terminal_marker_regex и
	// содержащая слово из report_keywords.
	marker := -1
	for i := len(lines) - 1; i >= 0; i-- {
		l := lines[i]
		if cfg.TerminalMarkerRegex.MatchString(l) && hasKeyword(l, cfg.ReportKeywords) {
			marker = i
			break
		}
	}
	if marker < 0 {
		return rep, nil
	}

	// 6.3.3: отчёт = строки от найденной (без маркера ◆︎) до строки перед
	// последним совпадением input_top_regex.
	end := len(lines)
	if cfg.InputTopRegex != nil {
		for i := len(lines) - 1; i > marker; i-- {
			if cfg.InputTopRegex.MatchString(lines[i]) {
				end = i
				break
			}
		}
	}
	var out []string
	for i := marker; i < end; i++ {
		l := lines[i]
		if i == marker {
			l = cfg.TerminalMarkerRegex.ReplaceAllString(l, "")
		}
		out = append(out, l)
	}
	rep.Found = true
	rep.Report = strings.TrimRight(strings.Join(out, "\n"), "\n")
	return rep, nil
}
