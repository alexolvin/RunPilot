// Ввод оператора (разделы 13.1/13.3 ТЗ, v2). Только по запросу оператора (R6);
// текст задания никуда не сохраняется (R5) — в audit только sid/bytes/lines/operator.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
	"unicode/utf8"

	"runpilot/internal/command"
	"runpilot/internal/model"
	"runpilot/internal/proto"
)

// operatorKey — ключ контекста с идентификатором оператора (ставит эндпоинт).
type operatorKey struct{}

// WithOperator — контекст с оператором (для audit).
func WithOperator(ctx context.Context, op string) context.Context {
	return context.WithValue(ctx, operatorKey{}, op)
}

// operatorFrom — оператор из контекста ("" если нет).
func operatorFrom(ctx context.Context) string {
	if v, ok := ctx.Value(operatorKey{}).(string); ok {
		return v
	}
	return ""
}

// CmdPaste — вставка задания в панель (13.1): условия → hub.Dispatch(KindPaste) →
// интерпретация (PASTED / PASTE_MISMATCH / PASTE_SUBMITTED) → audit. enqueue —
// после PASTED поставить сессию в очередь (/go).
func (s *Server) CmdPaste(ctx context.Context, sid, text string, enqueue bool) (command.Outcome, error) {
	if !utf8.ValidString(text) {
		return command.Outcome{Code: "PASTE_INVALID", Text: "текст не UTF-8"}, nil
	}
	// O9: слишком длинный текст задания → 413 с фактическим размером.
	if len(text) > s.cfg.Web.PasteMaxKB*kbInBytes {
		return command.Outcome{
			Code:  "PASTE_TOO_LONG",
			Text:  fmt.Sprintf("текст %d байт > web.paste_max_kb (%d КБ)", len(text), s.cfg.Web.PasteMaxKB),
			Details: map[string]any{"bytes": len(text), "limit_bytes": s.cfg.Web.PasteMaxKB * kbInBytes},
		}, nil
	}
	info, ok := s.hub.PaneBySID(sid)
	if !ok {
		return command.Outcome{Code: "PANE_NOT_FOUND", Text: "нет панели сессии"}, nil
	}
	p := info.Pane
	if p.State != "IDLE" {
		return command.Outcome{Code: "PANE_NOT_IDLE", Text: "панель не на вводе (не IDLE)"}, nil
	}
	if !p.InputEmpty {
		return command.Outcome{Code: "INPUT_NOT_EMPTY", Text: "ввод кодера не пуст"}, nil
	}
	m := proto.New(proto.KindPaste)
	m.PaneID, m.Text = p.PaneID, text
	cctx, cancel := context.WithTimeout(ctx, time.Duration(s.cfg.Web.PasteVerifySec)*time.Second)
	defer cancel()
	reply, err := s.hub.Dispatch(cctx, p.PaneID, m)
	if err != nil {
		return command.Outcome{Code: "DISPATCH", Text: err.Error()}, nil
	}
	switch reply.Result {
	case proto.ResPasted:
		s.auditPaste(ctx, sid, text)
		if enqueue {
			if e := s.sched.Enqueue(sid, model.QueueEntry{SID: sid}, false); e != nil {
				return command.Outcome{Code: "ENQUEUE", Text: e.Error()}, nil
			}
		}
		return command.Outcome{Code: "OK", Text: "вставлено"}, nil
	case proto.ResPasteMismatch:
		return command.Outcome{Code: "PASTE_MISMATCH", Text: "ввод не совпал с текстом: " + reply.Detail}, nil
	case proto.ResPasteSubmitted:
		return command.Outcome{Code: "PASTE_SUBMITTED", Text: "критический дефект: панель ушла в BUSY"}, nil
	case proto.ResPaneGone:
		return command.Outcome{Code: "PANE_GONE", Text: "панель исчезла"}, nil
	default:
		return command.Outcome{Code: reply.Result, Text: "неизвестный результат вставки"}, nil
	}
}

// CmdApprove — ответ на запрос подтверждения (13.3): hub.Dispatch(KindApprove) →
// APPROVED / APPROVE_NO_EFFECT.
func (s *Server) CmdApprove(ctx context.Context, sid, option string) (command.Outcome, error) {
	info, ok := s.hub.PaneBySID(sid)
	if !ok {
		return command.Outcome{Code: "PANE_NOT_FOUND", Text: "нет панели сессии"}, nil
	}
	m := proto.New(proto.KindApprove)
	m.PaneID, m.Text = info.Pane.PaneID, option
	cctx, cancel := context.WithTimeout(ctx, time.Duration(s.cfg.Web.PasteVerifySec)*time.Second)
	defer cancel()
	reply, err := s.hub.Dispatch(cctx, info.Pane.PaneID, m)
	if err != nil {
		return command.Outcome{Code: "DISPATCH", Text: err.Error()}, nil
	}
	switch reply.Result {
	case proto.ResApproved:
		return command.Outcome{Code: "OK", Text: "разрешено: " + option}, nil
	case proto.ResApproveNoEffect:
		return command.Outcome{Code: "APPROVE_NO_EFFECT", Text: "панель не вышла из PROMPT: " + reply.Detail}, nil
	case proto.ResPaneGone:
		return command.Outcome{Code: "PANE_GONE", Text: "панель исчезла"}, nil
	default:
		return command.Outcome{Code: reply.Result, Text: "неизвестный результат"}, nil
	}
}

// CmdCompress — сжатие контекста (13.1): узел вставляет compress_text (из профиля)
// + submit, затем сессия возвращается в очередь.
func (s *Server) CmdCompress(ctx context.Context, sid string) (command.Outcome, error) {
	info, ok := s.hub.PaneBySID(sid)
	if !ok {
		return command.Outcome{Code: "PANE_NOT_FOUND", Text: "нет панели сессии"}, nil
	}
	m := proto.New(proto.KindCompress)
	m.PaneID = info.Pane.PaneID
	cctx, cancel := context.WithTimeout(ctx, time.Duration(s.cfg.Web.PasteVerifySec)*time.Second)
	defer cancel()
	reply, err := s.hub.Dispatch(cctx, info.Pane.PaneID, m)
	if err != nil {
		return command.Outcome{Code: "DISPATCH", Text: err.Error()}, nil
	}
	if reply.Result == proto.ResPaneGone {
		return command.Outcome{Code: "PANE_GONE", Text: "панель исчезла"}, nil
	}
	if e := s.sched.Enqueue(sid, model.QueueEntry{SID: sid}, false); e != nil {
		return command.Outcome{Code: "ENQUEUE", Text: e.Error()}, nil
	}
	return command.Outcome{Code: "OK", Text: "сжатие запущено"}, nil
}

// CmdClose — закрытие сессии (5.4): owned-сессия → kill tmux-сессии на узле,
// затем GONE. Панели в hub нет (потерянная: координатор перезапускался) —
// GONE без операции узла.
func (s *Server) CmdClose(ctx context.Context, sid string) (command.Outcome, error) {
	rec, err := s.store.GetSession(sid)
	if err != nil {
		return command.Outcome{Code: "NOT_FOUND", Text: "сессия не найдена"}, nil
	}
	if rec.State == model.SessionGone {
		return command.Outcome{Code: "OK", Text: "сессия уже закрыта"}, nil
	}
	if p, ok := s.hub.PaneBySID(sid); ok {
		m := proto.New(proto.KindKillSession)
		m.PaneID = p.Pane.PaneID
		cctx, cancel := context.WithTimeout(ctx, nodeCmdTimeout)
		defer cancel()
		reply, err := s.hub.Dispatch(cctx, p.Pane.PaneID, m)
		if err != nil {
			return command.Outcome{Code: "DISPATCH", Text: err.Error()}, nil
		}
		switch reply.Result {
		case proto.ResKilled, proto.ResPaneGone:
			s.hub.DropPane(p.Pane.PaneID)
			s.sched.PaneGone(p.Pane.PaneID, sid)
			return command.Outcome{Code: "OK", Text: "закрыта: " + reply.Detail}, nil
		default:
			return command.Outcome{Code: reply.Result, Text: "не удалось закрыть: " + reply.Detail}, nil
		}
	}
	s.sched.PaneGone("", sid)
	return command.Outcome{Code: "OK", Text: "закрыта (панель отсутствовала)"}, nil
}

// auditPaste — журнал: PASTE (sid, bytes, lines, operator; текст НЕ хранить, R5).
func (s *Server) auditPaste(ctx context.Context, sid, text string) {
	payload, _ := json.Marshal(map[string]any{
		"bytes":    len(text),
		"lines":    countLines(text),
		"operator": operatorFrom(ctx),
	})
	_ = s.store.EventRecord(model.Event{
		TS: s.clk.Now(), Kind: "paste", SID: sid, Payload: payload,
	})
}

// countLines — число строк (для audit).
func countLines(s string) int {
	n := 1
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			n++
		}
	}
	return n
}
