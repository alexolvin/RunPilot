// PTY-менеджер узла (раздел 13.4 ТЗ): открытые PTY-каналы терминала,
// tmux attach в псевдотерминале, маршрут нажатий клавиш (PtyWrite) и вывода
// PTY в координатор (двоичные кадры с номером канала).
package node

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	nodepty "runpilot/internal/node/pty"
	"runpilot/internal/proto"
)

// ptySession — один открытый PTY-канал: мастер псевдотерминала + процесс attach.
// done — процесс завершён и реапнут (ptyReap); страховка SIGKILL проверяет его.
type ptySession struct {
	ses       *nodepty.Session
	closeOnce sync.Once
	done      atomic.Bool
}

// Close — завершить tmux attach (idempotent): SIGTERM + закрытие мастера
// (pty.Session.Close), затем SIGKILL через ptyKillGrace, если SIGTERM не
// сработал. tmux-сессия не затрагивается (13.4 ТЗ).
func (ps *ptySession) Close() {
	ps.closeOnce.Do(func() {
		ps.ses.Close()
		ses := ps.ses
		go func() {
			time.Sleep(ptyKillGrace)
			if !ps.done.Load() && ses.Cmd != nil && ses.Cmd.Process != nil {
				_ = ses.Cmd.Process.Kill()
			}
		}()
	})
}

// SetPtySender — установит обратный вызов вывода PTY (двоичные кадры) в
// координатор. Ставится до Run; nil-безопасно (без отправителя вывод дропится).
func (n *Node) SetPtySender(fn func(int, []byte) error) {
	n.ptyMu.Lock()
	n.ptySender = fn
	n.ptyMu.Unlock()
}

// ptyOpen — tmux attach к сессии в PTY (13.4 ТЗ). PTY_OPENED при успехе,
// PANE_GONE если сессия не найдена.
func (n *Node) ptyOpen(ctx context.Context, m proto.Msg) (*Reply, error) {
	ses, err := nodepty.Open(ctx, n.ex, m.Socket, m.TmuxSession)
	if err != nil {
		n.log.Warn("node: pty_open: "+err.Error(), "chan", m.Chan, "session", m.TmuxSession)
		return &Reply{CmdID: m.CmdID, Result: ResPaneGone, Detail: err.Error()}, nil
	}
	ps := &ptySession{ses: ses}
	n.ptyMu.Lock()
	n.ptySes[m.Chan] = ps
	n.ptyMu.Unlock()
	go n.ptyReadLoop(ctx, m.Chan, ses)  // вывод PTY → координатор
	go n.ptyReap(m.Chan, ses, ps)        // единственный Wait + pty_exit
	n.log.Info("node: pty открыт", "chan", m.Chan, "session", m.TmuxSession)
	return &Reply{CmdID: m.CmdID, Result: proto.ResPtyOpened, Detail: m.TmuxSession}, nil
}

// ptyClose — завершить tmux attach (13.4 ТЗ); tmux-сессия не затрагивается.
func (n *Node) ptyClose(m proto.Msg) (*Reply, error) {
	n.ptyMu.Lock()
	ps := n.ptySes[m.Chan]
	n.ptyMu.Unlock()
	if ps != nil {
		ps.Close() // SIGTERM → ptyReap реапнет и снимет канал
	}
	n.log.Info("node: pty закрыт", "chan", m.Chan)
	return &Reply{CmdID: m.CmdID, Result: ResSent, Detail: fmt.Sprintf("chan=%d", m.Chan)}, nil
}

// PtyWrite — данные терминала от координатора (нажатия клавиш) → PTY-мастер.
// Вызывается транспортом (WS/встроенный) при двоичном кадре с каналом.
func (n *Node) PtyWrite(chan_ int, data []byte) {
	n.ptyMu.Lock()
	ps := n.ptySes[chan_]
	n.ptyMu.Unlock()
	if ps == nil {
		return
	}
	if _, err := ps.ses.Master.Write(data); err != nil {
		ps.Close() // PTY закрыт — завершить attach (ptyReap очистит канал).
	}
}

// ptyReadLoop — читает вывод PTY и шлёт его в координатор (двоичные кадры).
// При EOF (attach завершился) просто останавливается — очистку ведёт ptyReap.
func (n *Node) ptyReadLoop(ctx context.Context, chan_ int, ses *nodepty.Session) {
	buf := make([]byte, ptyReadBuf)
	for {
		nread, err := ses.Master.Read(buf)
		if nread > 0 {
			n.ptyMu.Lock()
			snd := n.ptySender
			n.ptyMu.Unlock()
			if snd != nil {
				_ = snd(chan_, buf[:nread])
			}
		}
		if err != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		default:
		}
	}
}

// ptyReap — единственный Wait: реапит процесс (нет zombie) и снимает канал;
// уведомляет координатор pty_exit (13.4: tmux attach завершился).
func (n *Node) ptyReap(chan_ int, ses *nodepty.Session, ps *ptySession) {
	_ = ses.Cmd.Wait() // завершение процесса (SIGTERM/SIGKILL/выход tmux)
	ps.done.Store(true)
	n.ptyMu.Lock()
	sm := n.sendMsg
	n.ptyMu.Unlock()
	if sm != nil {
		m := proto.New(proto.KindPtyExit)
		m.Chan = chan_
		_ = sm(m)
	}
	n.ptyDrop(chan_)
}

// ptyDrop — снять PTY-канал из реестра (idempotent; Close здесь не ведёт).
func (n *Node) ptyDrop(chan_ int) {
	n.ptyMu.Lock()
	delete(n.ptySes, chan_)
	n.ptyMu.Unlock()
}

// ptyCloseAll — закрыть все PTY-каналы (остановка узла, 13.4 ТЗ).
func (n *Node) ptyCloseAll() {
	n.ptyMu.Lock()
	chans := make([]int, 0, len(n.ptySes))
	for c := range n.ptySes {
		chans = append(chans, c)
	}
	n.ptyMu.Unlock()
	for _, c := range chans {
		n.ptyMu.Lock()
		ps := n.ptySes[c]
		n.ptyMu.Unlock()
		if ps != nil {
			ps.Close()
		}
	}
}
