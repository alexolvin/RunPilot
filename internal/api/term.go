// Терминал в браузере (13.4 ТЗ): мост «браузерский WS ↔ PTY панели узла».
//
// Поток: оператор открывает GET /web/ws/term/{sid} (веб: cookieAuth + Origin)
// → координатор поднимает PTY панели узла (pty_open) и держит два направления
// двоичных кадров: нажатия клавиш (браузер→узел) и вывод PTY (узел→браузер).
// Лимит терминалов на узел и закрытие по бездействию — раздел 13.4 ТЗ.
// Ввод и вывод терминала никуда не сохраняются (R5) — в audit только
// идентификаторы, длительность и байты.
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"runpilot/internal/model"
	"runpilot/internal/proto"

	"github.com/gorilla/websocket"
)

// terminalSession — один открытый терминал: браузерский WS ↔ PTY панели узла.
type terminalSession struct {
	chan_  int    // номер PTY-канала (в заголовках двоичных кадров)
	host   string // узел с PTY
	sid    string
	actor  string // оператор (Tailscale-User / id_hash)
	conn   *websocket.Conn
	wmu    sync.Mutex // один писатель на conn (open/error + бинарный вывод)
	openedAt time.Time // ставится до register (happens-before через termMu)
	last   atomic.Int64 // UnixNano последней активности (бездействие)
	bytesIn  atomic.Int64 // клавиши браузер→узел
	bytesOut atomic.Int64 // вывод PTY узел→браузер
}

// allocChan — новый номер PTY-канала (монотонный; кадры несут его в заголовке).
func (s *Server) allocChan() int {
	s.termMu.Lock()
	defer s.termMu.Unlock()
	s.termSeq++
	return s.termSeq
}

// termCountForNode — число открытых терминалов узла (лимит 13.4).
func (s *Server) termCountForNode(host string) int {
	s.termMu.Lock()
	defer s.termMu.Unlock()
	n := 0
	for _, ts := range s.terminals {
		if ts.host == host {
			n++
		}
	}
	return n
}

// registerTerminal — регистрация открытого терминала.
func (s *Server) registerTerminal(ts *terminalSession) {
	s.termMu.Lock()
	defer s.termMu.Unlock()
	s.terminals[ts.chan_] = ts
}

// touchTerminal — отметка активности (для закрытия по бездействию).
func (s *Server) touchTerminal(ts *terminalSession) {
	ts.last.Store(s.clk.Now().UnixNano())
}

// TerminalWS — мост терминала (13.4 ТЗ): GET /web/ws/term/{sid}.
// Путь: веб (cookieAuth + Origin) → сюда. Порядок:
//  1. панель сессии (404 PANE_NOT_FOUND),
//  2. лимит терминалов узла (503 TERMINAL_LIMIT),
//  3. WS-апгрейд,
//  4. pty_open узлу (PTY_OPENED), иначе error + close,
//  5. реестр + audit TERM_OPEN, затем мост (браузер→узел).
func (s *Server) TerminalWS(w http.ResponseWriter, r *http.Request, sid, actor string) {
	info, ok := s.hub.PaneBySID(sid)
	if !ok {
		httpError(w, http.StatusNotFound, "PANE_NOT_FOUND", "нет панели сессии")
		return
	}
	host := info.Host
	if s.termCountForNode(host) >= s.termMaxPerNode {
		httpError(w, http.StatusServiceUnavailable, "TERMINAL_LIMIT",
			"превышен лимит терминалов на узел")
		return
	}
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	now := s.clk.Now()
	ts := &terminalSession{
		chan_:  s.allocChan(),
		host:   host,
		sid:    sid,
		actor:  actor,
		conn:   conn,
	}
	ts.openedAt = now
	ts.last.Store(now.UnixNano())

	m := proto.New(proto.KindPtyOpen)
	m.Chan = ts.chan_
	m.TmuxSession = info.Pane.Session
	m.Socket = info.Pane.Socket
	cctx, cancel := context.WithTimeout(r.Context(), ptyOpenTimeout)
	reply, derr := s.hub.DispatchToNode(cctx, host, m)
	cancel()
	if derr != nil || reply.Result != proto.ResPtyOpened {
		// Idempotent: если PTY не открыт (PANE_GONE / узел недоступен) — no-op;
		// если открыт, но dispatch ушёл в таймаут — убирает tmux attach.
		s.sendPtyClose(host, ts.chan_)
		s.termError(conn, "PTY_OPEN_FAILED")
		return
	}

	s.registerTerminal(ts)
	s.auditTermOpen(ts)
	s.termOpen(conn, ts.chan_) // подтверждение: номер PTY-канала для кадров

	s.termBridge(ts) // блокирует до закрытия браузером
	// Браузер закрылся — убить tmux attach (list-clients вернётся к исходному)
	// и снять терминал с реестра.
	s.sendPtyClose(host, ts.chan_)
	s.closePty(ts.chan_)
}

// termBridge — чтение нажатий клавиш из браузерского WS → узлу (13.4).
// Возвращается, когда браузер закрыл соединение или узел/PTY недоступен.
func (s *Server) termBridge(ts *terminalSession) {
	for {
		msgType, data, err := ts.conn.ReadMessage()
		if err != nil {
			return
		}
		if msgType != websocket.BinaryMessage || len(data) == 0 {
			continue
		}
		s.touchTerminal(ts)
		ts.bytesIn.Add(int64(len(data)))
		if err := s.hub.PtySendToNode(ts.host, ts.chan_, data); err != nil {
			return
		}
	}
}

// applyPtyData — вывод PTY с узла (двоичный кадр) → браузеру (13.4).
// Вызывается read-циклом канала узла.
func (s *Server) applyPtyData(chan_ int, data []byte) {
	if len(data) == 0 {
		return
	}
	s.termMu.Lock()
	ts := s.terminals[chan_]
	s.termMu.Unlock()
	if ts == nil {
		return
	}
	s.touchTerminal(ts)
	ts.bytesOut.Add(int64(len(data)))
	ts.wmu.Lock()
	_ = ts.conn.WriteMessage(websocket.BinaryMessage, data)
	ts.wmu.Unlock()
}

// closePty — закрыть терминал (13.4): снять с реестра, закрыть браузерский WS,
// audit TERM_CLOSE. Идемпотентно.
func (s *Server) closePty(chan_ int) {
	s.termMu.Lock()
	ts, ok := s.terminals[chan_]
	if ok {
		delete(s.terminals, chan_)
	}
	s.termMu.Unlock()
	if !ok {
		return
	}
	ts.wmu.Lock()
	_ = ts.conn.WriteControl(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, "терминал закрыт"),
		time.Now().UTC().Add(time.Second))
	_ = ts.conn.Close()
	ts.wmu.Unlock()
	s.auditTermClose(ts)
}

// closePtyByHost — узел отключился: закрыть все его терминалы (PTY ушёл с ним).
func (s *Server) closePtyByHost(host string) {
	s.termMu.Lock()
	var chans []int
	for c, ts := range s.terminals {
		if ts.host == host {
			chans = append(chans, c)
		}
	}
	s.termMu.Unlock()
	for _, c := range chans {
		s.closePty(c)
	}
}

// sendPtyClose — best-effort pty_close узлу (убить tmux attach, 13.4).
func (s *Server) sendPtyClose(host string, chan_ int) {
	ctx, cancel := context.WithTimeout(context.Background(), ptyOpenTimeout)
	defer cancel()
	m := proto.New(proto.KindPtyClose)
	m.Chan = chan_
	_, _ = s.hub.DispatchToNode(ctx, host, m)
}

// reapIdleTerminals — закрыть терминалы, бездействующие дольше termIdle (13.4).
// Чистая функция по s.clk — тестируется виртуальными часами.
func (s *Server) reapIdleTerminals() []int {
	now := s.clk.Now()
	s.termMu.Lock()
	var stale []int
	for c, ts := range s.terminals {
		if now.Sub(time.Unix(0, ts.last.Load())) > s.termIdle {
			stale = append(stale, c)
		}
	}
	s.termMu.Unlock()
	for _, c := range stale {
		s.closePty(c)
	}
	return stale
}

// TermIdleReaper — фоновая проверка бездействия терминалов (13.4).
func (s *Server) TermIdleReaper(ctx context.Context) {
	t := time.NewTicker(termReaperTick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.reapIdleTerminals()
		}
	}
}

// termOpen — первое сообщение браузеру: канал открыт (номер PTY-канала).
func (s *Server) termOpen(conn *websocket.Conn, chan_ int) {
	_ = conn.WriteJSON(map[string]any{"type": "open", "chan": chan_})
}

// termError — отказ после апгрейда: код ошибки, затем закрытие.
func (s *Server) termError(conn *websocket.Conn, code string) {
	_ = conn.WriteJSON(map[string]any{"type": "error", "code": code})
	_ = conn.WriteControl(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseInternalServerErr, code),
		time.Now().UTC().Add(time.Second))
}

// auditTermOpen — TERM_OPEN (13.4): sid, host, operator (R5: без данных).
func (s *Server) auditTermOpen(ts *terminalSession) {
	payload, _ := json.Marshal(map[string]any{
		"host":     ts.host,
		"operator": ts.actor,
	})
	_ = s.store.EventRecord(model.Event{
		TS: s.clk.Now(), Kind: "term_open", SID: ts.sid, Payload: payload,
	})
}

// auditTermClose — TERM_CLOSE (13.4): sid, host, operator, длительность, байты.
func (s *Server) auditTermClose(ts *terminalSession) {
	payload, _ := json.Marshal(map[string]any{
		"host":        ts.host,
		"operator":    ts.actor,
		"duration_ms": s.clk.Now().Sub(ts.openedAt).Milliseconds(),
		"bytes_in":    ts.bytesIn.Load(),
		"bytes_out":   ts.bytesOut.Load(),
	})
	_ = s.store.EventRecord(model.Event{
		TS: s.clk.Now(), Kind: "term_close", SID: ts.sid, Payload: payload,
	})
}
