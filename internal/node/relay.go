package node

// Связь с оркестратором (ТЗ Relay): узловые команды turn_report и
// read_report_file. turn_report разбирает последний ход (запись Qwen Code или
// терминал), read_report_file отдаёт .md-файл сессии с проверками раздела 6.2.

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"runpilot/internal/agentlog"
	"runpilot/internal/config"
	"runpilot/internal/proto"
)

// qwenHome — каталог записей Qwen Code: $QWEN_HOME или ~/.qwen.
func qwenHome() string {
	if h := os.Getenv("QWEN_HOME"); h != "" {
		return h
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".qwen")
}

// recordingPath — путь к записи <uuid>.jsonl по каталогу сессии.
// Slug каталога — путь с / → - (формат Qwen Code).
func recordingPath(cwd, uuid string) string {
	slug := strings.ReplaceAll(cwd, "/", "-")
	return filepath.Join(qwenHome(), "projects", slug, "chats", uuid+".jsonl")
}

// turnReport — разобранный последний ход (ТЗ Relay §2/§6).
// Источник: запись (если задан qwen_session_id и файл существует), иначе терминал.
func (n *Node) turnReport(ctx context.Context, m proto.Msg) (*Reply, error) {
	info, ok := n.snap.PaneBySID(m.SID)
	if !ok {
		return &Reply{CmdID: m.CmdID, Result: ResPaneGone}, nil
	}
	rel := n.cfg.Relay

	// Источник — запись Qwen Code (основной, 6.1).
	if m.QwenSessionID != "" {
		path := recordingPath(info.Dir, m.QwenSessionID)
		if _, err := os.Stat(path); err == nil {
			rep, err := agentlog.ParseRecording(path, info.Dir, agentlog.Config{
				ReportKeywords: rel.ReportKeywords,
				AttachMaxFiles: rel.AttachMaxFiles,
			})
			if err == nil {
				return &Reply{CmdID: m.CmdID, Result: ResSent, TurnReport: toProtoTurnReport(rep)}, nil
			}
		}
	}

	// Источник — терминал (запасной, 6.3).
	rep, err := n.terminalReport(ctx, info, rel)
	if err != nil {
		return &Reply{CmdID: m.CmdID, Result: "TERMINAL_ERROR", Detail: err.Error()}, nil
	}
	return &Reply{CmdID: m.CmdID, Result: ResSent, TurnReport: toProtoTurnReport(rep)}, nil
}

// terminalReport — снимок панели + разбор (6.3).
func (n *Node) terminalReport(ctx context.Context, info PaneInfo, rel config.Relay) (*agentlog.Report, error) {
	scrollback := rel.ScrollbackLines
	if scrollback <= 0 {
		scrollback = defaultScrollback
	}
	out, err := n.ex.Output(ctx, "tmux", tmuxArgs(info.Socket,
		"capture-pane", "-p", "-J", "-S", "-"+strconv.Itoa(scrollback), "-t", info.PaneID)...)
	if err != nil {
		return nil, err
	}
	markerRe, err := regexp.Compile(rel.TerminalMarkerRegex)
	if err != nil {
		return nil, err
	}
	return agentlog.ParseTerminal(string(out), agentlog.TermConfig{
		ReportKeywords:      rel.ReportKeywords,
		TerminalMarkerRegex: markerRe,
		InputTopRegex:       n.regexps.InputTop,
	})
}

// readReportFile — содержимое .md-файла сессии (ТЗ Relay §6.2).
// Проверки: реальный путь внутри каталога сессии, расширение .md, размер ≤ лимита.
func (n *Node) readReportFile(ctx context.Context, m proto.Msg) (*Reply, error) {
	info, ok := n.snap.PaneBySID(m.SID)
	if !ok {
		return &Reply{CmdID: m.CmdID, Result: ResPaneGone}, nil
	}
	root := filepath.Clean(info.Dir)

	abs := m.Path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(root, abs)
	}
	abs = filepath.Clean(abs)

	// Реальный путь (разрешение симлинков) внутри каталога сессии.
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return &Reply{CmdID: m.CmdID, Result: proto.ResPathForbidden, Detail: "нет файла"}, nil
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		realRoot = root
	}
	rel, err := filepath.Rel(realRoot, real)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return &Reply{CmdID: m.CmdID, Result: proto.ResPathForbidden, Detail: "вне каталога"}, nil
	}
	// Расширение .md.
	if !strings.EqualFold(filepath.Ext(real), ".md") {
		return &Reply{CmdID: m.CmdID, Result: proto.ResExtForbidden, Detail: "не .md"}, nil
	}
	// Размер ≤ attach_max_kb.
	limKB := n.cfg.Relay.AttachMaxKB
	if limKB <= 0 {
		limKB = defaultAttachMaxKB
	}
	fi, err := os.Stat(real)
	if err != nil {
		return &Reply{CmdID: m.CmdID, Result: proto.ResPathForbidden, Detail: "нет файла"}, nil
	}
	if fi.Size() > int64(limKB)*kbInBytes {
		return &Reply{CmdID: m.CmdID, Result: proto.ResTooLarge, Detail: "сверх лимита"}, nil
	}
	data, err := os.ReadFile(real)
	if err != nil {
		return &Reply{CmdID: m.CmdID, Result: proto.ResPathForbidden, Detail: err.Error()}, nil
	}
	return &Reply{CmdID: m.CmdID, Result: ResSent, Detail: string(data)}, nil
}

// toProtoTurnReport — agentlog.Report → proto.TurnReport.
func toProtoTurnReport(r *agentlog.Report) *proto.TurnReport {
	if r == nil {
		return nil
	}
	out := &proto.TurnReport{Source: r.Source, Found: r.Found, Report: r.Report}
	for _, f := range r.Files {
		out.Files = append(out.Files, proto.ReportFile{Path: f.Path, Size: f.Size})
	}
	if r.Question != nil {
		q := &proto.ReportQuestion{}
		for _, qi := range r.Question.Questions {
			item := proto.ReportQuestionItem{Question: qi.Question, MultiSelect: qi.MultiSelect}
			for _, o := range qi.Options {
				item.Options = append(item.Options, proto.ReportOption{Label: o.Label, Description: o.Description})
			}
			q.Questions = append(q.Questions, item)
		}
		out.Question = q
	}
	return out
}
