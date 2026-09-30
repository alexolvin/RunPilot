package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	"runpilot/internal/client"
)

// newAPIClient — клиент координатора из конфига (раздел 11 ТЗ).
func newAPIClient() *client.Client {
	return client.New(cfg.Client.Coordinator, cfg.Client.TokenEnv)
}

// printJSON — вывод в режиме --json (машиночитаемый, один документ).
func printJSON(v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}

// printTable — таблица через tabwriter: заголовок + строки.
func printTable(headers []string, rows [][]string) {
	w := tabwriter.NewWriter(os.Stdout, tableMinWidth, tableTabWidth, tablePad, ' ', 0)
	if len(headers) > 0 {
		fmt.Fprintln(w, strings.Join(headers, "\t"))
	}
	for _, r := range rows {
		fmt.Fprintln(w, strings.Join(r, "\t"))
	}
	w.Flush()
}

// resolveTarget — адресация <t> (раздел 11 ТЗ):
//
//	sid | имя | host:tmux_session[:window.pane] | --pane %12
//
// sid и имя проходят как есть (разрешает сервер); составные формы
// разрешаются в sid по GET /api/v1/sessions?all=true.
func resolveTarget(c *client.Client, t, paneID string) (string, error) {
	if paneID != "" {
		return targetByPane(c, paneID)
	}
	if t == "" {
		return "", fmt.Errorf("укажите сессию <t>")
	}
	// Голый id панели (%N) — адресация как --pane.
	if strings.HasPrefix(t, "%") {
		return targetByPane(c, t)
	}
	if !strings.Contains(t, ":") {
		return t, nil
	}
	return targetByComposite(c, t)
}

// targetByPane — --pane %12: сессия по id панели.
func targetByPane(c *client.Client, paneID string) (string, error) {
	var sessions []client.Session
	if _, err := c.Do("GET", "/api/v1/sessions?all=true", nil, &sessions); err != nil {
		return "", err
	}
	for _, s := range sessions {
		if s.PaneID == paneID {
			return s.SID, nil
		}
	}
	return "", fmt.Errorf("нет сессии с панелью %s", paneID)
}

// targetByComposite — host:tmux_session[:window.pane].
func targetByComposite(c *client.Client, t string) (string, error) {
	parts := strings.SplitN(t, ":", compositeParts)
	host, tmuxSess := parts[0], parts[1]
	window := -1
	if len(parts) == compositeParts {
		wp := strings.SplitN(parts[compositeParts-1], ".", windowPaneParts)
		w, err := strconv.Atoi(wp[0])
		if err != nil {
			return "", fmt.Errorf("некорректная адресация %q: window.pane", t)
		}
		window = w
	}
	var sessions []client.Session
	if _, err := c.Do("GET", "/api/v1/sessions?all=true", nil, &sessions); err != nil {
		return "", err
	}
	var matches []client.Session
	for _, s := range sessions {
		if s.Host != host || s.TmuxSession != tmuxSess {
			continue
		}
		if window >= 0 && s.Window != window {
			continue
		}
		matches = append(matches, s)
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("нет сессии %s", t)
	case 1:
		return matches[0].SID, nil
	default:
		names := make([]string, 0, len(matches))
		for _, m := range matches {
			names = append(names, fmt.Sprintf("%s (%s)", m.Name, m.SID))
		}
		return "", fmt.Errorf("несколько сессий %s: %s; уточните window.pane",
			t, strings.Join(names, ", "))
	}
}

// requireTarget — обязательный <t> (или --pane).
func requireTarget(c *client.Client, t, paneID string) (string, error) {
	if t == "" && paneID == "" {
		return "", fmt.Errorf("укажите сессию <t> или --pane")
	}
	return resolveTarget(c, t, paneID)
}
