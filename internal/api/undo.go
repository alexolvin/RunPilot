package api

// O5: ошибочная массовая операция («Очистить очередь», «Вернуть все») —
// подтверждение (веб) + отмена в течение web.undo_sec (кнопка «Отменить»).
// Снимок состояния до операции хранит координатор; отмена возвращает очередь
// в исходный порядок (J12).

import (
	"errors"
	"net/http"
	"time"

	"runpilot/internal/model"
)

// errNoUndo — нет операции для отмены (окно прошло или операция не делалась).
var errNoUndo = errors.New("нет операции для отмены")

// holdSIDs — сессии, требующие внимания (HOLD), для «Вернуть все» (O5).
func (s *Server) holdSIDs() ([]string, error) {
	recs, err := s.store.ListSessions(false)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, r := range recs {
		if r.State == model.SessionHold {
			out = append(out, r.SID)
		}
	}
	return out, nil
}

// storeUndo — сохранить снимок для отмены (O5): kind — "clear" | "requeue-hold";
// entries — для clear, holds — для requeue-hold. Дедлайн web.undo_sec.
func (s *Server) storeUndo(kind string, entries []model.QueueEntry, holds []string) {
	s.undoMu.Lock()
	defer s.undoMu.Unlock()
	s.undoKind = kind
	s.undoEntries = entries
	s.undoHolds = holds
	s.undoExpiry = s.clk.Now().Add(time.Duration(s.cfg.Web.UndoSec) * time.Second)
}

// undoQueue — отменить последнюю массовую операцию в пределах web.undo_sec.
// Возвращает число восстановленных элементов; errNoUndo — окно прошло/нет снимка.
func (s *Server) undoQueue() (int, error) {
	if s.sched == nil {
		return 0, errNoUndo
	}
	s.undoMu.Lock()
	kind, entries, holds := s.undoKind, s.undoEntries, s.undoHolds
	expired := s.clk.Now().After(s.undoExpiry)
	s.undoKind, s.undoEntries, s.undoHolds = "", nil, nil
	s.undoMu.Unlock()
	if kind == "" || expired {
		return 0, errNoUndo
	}
	switch kind {
	case "clear":
		if err := s.sched.RestoreQueue(entries); err != nil {
			return 0, err
		}
		return len(entries), nil
	case "requeue-hold":
		n := 0
		for _, sid := range holds {
			if err := s.sched.Hold(sid); err == nil {
				n++
			}
		}
		return n, nil
	}
	return 0, errNoUndo
}

// handleQueueUndo — POST /api/v1/queue/undo: отменить массовую операцию (O5).
func (s *Server) handleQueueUndo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, http.StatusMethodNotAllowed, "METHOD", "POST")
		return
	}
	if s.sched == nil {
		httpError(w, http.StatusServiceUnavailable, "NO_SCHEDULER", "планировщик не запущен")
		return
	}
	n, err := s.undoQueue()
	if err != nil {
		httpError(w, http.StatusGone, "UNDO_EXPIRED",
			"окно отмены (web.undo_sec) прошло или операция не отменялась")
		return
	}
	writeJSON(w, map[string]any{"restored": n})
}
