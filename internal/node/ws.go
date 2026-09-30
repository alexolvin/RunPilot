package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"runpilot/internal/clock"
	"runpilot/internal/proto"

	"github.com/gorilla/websocket"
)

// ErrUpdated — self-update завершён успешно (раздел 14.2 ТЗ): бинарник
// заменён (runpilot.prev + rename), процесс должен завершиться кодом 75, чтобы
// systemd (Restart) перезапустил НОВЫЙ бинарник. tmux и кодеры не
// затрагиваются; сессии находятся заново по @runpilot_sid.
var ErrUpdated = errors.New("node: self-update: бинарник заменён")

// WSClient — канал узла к координатору (раздел 13 ТЗ).
type WSClient struct {
	url        string
	token      string
	runpilotVersion string
	clk        clock.Clock
	log        *slog.Logger
	node       *Node
}

// NewWSClient собирает клиент; coordURL — адрес координатора
// (http://host:api_port), токен — Bearer (раздел 14 ТЗ).
func NewWSClient(coordURL, token, runpilotVersion string, clk clock.Clock, log *slog.Logger, node *Node) *WSClient {
	u, err := url.Parse(coordURL)
	if err != nil {
		u = &url.URL{Scheme: "http", Host: coordURL}
	}
	scheme := "ws"
	if u.Scheme == "https" {
		scheme = "wss"
	}
	u.Scheme = scheme
	u.Path = "/api/v1/node"
	u.RawQuery = ""
	return &WSClient{url: u.String(), token: token, runpilotVersion: runpilotVersion, clk: clk, log: log, node: node}
}

// Run — цикл: dial → hello → panes → read/write; переподключение
// с экспоненциальной задержкой 1 → 30 с. Возвращает ошибку ctx.
func (w *WSClient) Run(ctx context.Context) error {
	backoff := wsBackoffBase
	for {
		err := w.session(ctx)
		if errors.Is(err, ErrUpdated) {
			return ErrUpdated // 14.2: переподключение не нужно — узел завершится кодом 75
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			w.log.Warn("node: канал: " + err.Error())
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		backoff *= wsBackoffFactor
		if backoff > wsBackoffMax {
			backoff = wsBackoffMax
		}
	}
}

// session — одно соединение.
func (w *WSClient) session(ctx context.Context) error {
	dialer := websocket.Dialer{}
	hdr := http.Header{}
	if w.token != "" {
		hdr.Set("Authorization", "Bearer "+w.token)
	}
	conn, _, err := dialer.Dial(w.url, hdr)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()

	// Э6: ping координатора (WS-контрольный кадр) продлевает read deadline.
	// Пинг потребляется внутри ReadJSON (авто-pong) и до цикла чтения не
	// доходит — без этого узел реконнектится через wsReadDeadline при
	// простое (координатор молчит, dispatch не ходит).
	conn.SetPingHandler(func(appData string) error {
		_ = conn.SetReadDeadline(time.Now().Add(wsReadDeadline))
		return conn.WriteControl(websocket.PongMessage, []byte(appData),
			time.Now().Add(wsPongWriteTimeout))
	})

	// После подключения — hello и полный список панелей (раздел 13 ТЗ).
	if err := w.hello(ctx, conn); err != nil {
		return err
	}

	// Цикл снимков и горутина записи живут только пока живо соединение:
	// connCtx отменяется при любой ошибке чтения/записи, чтобы readErr мог
	// дождаться и writerDone, и nodeDone. Иначе writer, наблюдающий лишь
	// внешний ctx, не завершался, session() вешался навсегда, и узел больше
	// не переподключался после wsReadDeadline (молчание координатора > 2 мин).
	connCtx, cancelConn := context.WithCancel(ctx)
	defer cancelConn()

	out := make(chan proto.Msg, wsOutBufLen)
	outBin := make(chan []byte, wsOutBufLen) // двоичные кадры PTY (13.4 ТЗ)
	// flushReq — барьер записи (14.2): писатель — одна горутина, обрабатывает
	// сообщения FIFO, поэтому барьер, поставленный после ответа, закрыт
	// только после того, как все предшествующие записи завершены.
	// Ожидание лишь освобождения out недостаточно: писатель мог забрать
	// сообщение из канала, но ещё не дописать кадр — ранний conn.Close
	// оборвёт ответ (найден на W9: координатор не видел UPDATED).
	flushReq := make(chan chan struct{})
	writeErr := make(chan error, 1)
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for {
			select {
			case m := <-out:
				if err := conn.WriteJSON(&m); err != nil {
					select {
					case writeErr <- err:
					default:
					}
					return
				}
			case d := <-outBin:
				if err := conn.WriteMessage(websocket.BinaryMessage, d); err != nil {
					select {
					case writeErr <- err:
					default:
					}
					return
				}
			case ack := <-flushReq:
				close(ack)
			case <-writeErr:
				return
			case <-connCtx.Done():
				return
			}
		}
	}()

	send := func(m proto.Msg) error {
		select {
		case out <- m:
			return nil
		case <-connCtx.Done():
			return connCtx.Err()
		}
	}

	// Вывод PTY → координатор (двоичные кадры с номером канала, 13.4 ТЗ).
	w.node.SetPtySender(func(chan_ int, data []byte) error {
		select {
		case outBin <- proto.EncodeFrame(chan_, data):
			return nil
		case <-connCtx.Done():
			return connCtx.Err()
		}
	})

	nodeDone := make(chan error, 1)
	go func() { nodeDone <- w.node.Run(connCtx, send) }()

	// GPU-телеметрия (Э6): только пока живо соединение; завершается
	// вместе с node (connCtx).
	var gpuDone <-chan error
	if w.node.GPUEnabled() {
		ch := make(chan error, 1)
		go func() { ch <- w.node.RunGPU(connCtx, send) }()
		gpuDone = ch
	}

	readErr := func(err error) error {
		cancelConn()
		<-nodeDone
		if gpuDone != nil {
			<-gpuDone
		}
		<-writerDone
		return err
	}
	for {
		// Ограничение на молчание канала: мёртвое соединение (FIN потерян,
		// NAT сросся) не должно жить вечно.
		_ = conn.SetReadDeadline(time.Now().Add(wsReadDeadline))
		msgType, data, err := conn.ReadMessage()
		if err != nil {
			return readErr(fmt.Errorf("read: %w", err))
		}
		// Двоичный кадр = данные PTY (нажатия клавиш) с номером канала (13.4).
		if msgType == websocket.BinaryMessage {
			chan_, ptyData := proto.DecodeFrame(data)
			w.node.PtyWrite(chan_, ptyData)
			continue
		}
		var m proto.Msg
		if err := json.Unmarshal(data, &m); err != nil {
			return readErr(fmt.Errorf("read: %w", err))
		}
		reply, err := w.handleCommand(ctx, m, out)
		if err != nil {
			return readErr(err)
		}
		// 14.2 ТЗ: self-update успешен — бинарник уже заменён; дождаться
		// ЗАПИСИ ответа UPDATED в сокет и завершиться (процесс — кодом 75).
		if reply != nil && m.Type == proto.KindUpdate && reply.Result == proto.ResUpdated {
			w.awaitReplyWritten(flushReq, out)
			return ErrUpdated
		}
		select {
		case err := <-writeErr:
			return readErr(fmt.Errorf("write: %w", err))
		default:
		}
	}
}

// awaitReplyWritten — дождаться, пока горутина записи ДОПИШЕТ в сокет все
// сообщения, поставленные до барьера (включая ответ UPDATED). Сначала
// сток out (писатель забрал сообщения; FIFO одной горытины гарантирует,
// что барьер обработается после их записи), затем барьер.
func (w *WSClient) awaitReplyWritten(flushReq chan chan struct{}, out chan proto.Msg) {
	deadline := time.Now().Add(flushBarrierMax)
	for len(out) > 0 && time.Now().Before(deadline) {
		time.Sleep(flushDrainPoll)
	}
	ack := make(chan struct{})
	select {
	case flushReq <- ack:
	case <-time.After(flushBarrierMax):
		return
	}
	select {
	case <-ack:
	case <-time.After(flushBarrierMax):
	}
}

// hello — приветствие и полный список панелей (раздел 13 ТЗ).
func (w *WSClient) hello(ctx context.Context, conn *websocket.Conn) error {
	m := proto.New(proto.KindHello)
	m.Host = Hostname()
	m.HostIP = HostIP(hostPort(w.url))
	m.RUNPILOTVersion = w.runpilotVersion
	m.TmuxVersion = TmuxVersion(ctx, w.node.ex)
	m.OS = OS()
	m.Sockets = w.node.cfg.Node.TmuxSockets
	// proto 2: возможности узла (15.5). tmux-узел: spawn/paste/pty/update/
	// agent_run; external — концепт шлюза, не узла.
	m.Features = []string{"spawn", "paste", "pty", "update", "agent_run"}
	if err := conn.WriteJSON(&m); err != nil {
		return fmt.Errorf("hello: %w", err)
	}
	// Prefill ДО списка: без первичного скана список пуст (цикл ещё не
	// отработал) → координатор сверкой с БД помечает живые сессии GONE
	// (ложная потеря на чистом рестарте).
	if err := w.node.Prefill(ctx); err != nil {
		w.log.Warn("node: prefill: " + err.Error())
	}
	pm := proto.New(proto.KindPanes)
	pm.Panes = w.node.PaneList()
	if err := conn.WriteJSON(&pm); err != nil {
		return fmt.Errorf("panes: %w", err)
	}
	return nil
}

// handleCommand — команда координатора; ответ уходит в out. Возвращает
// ответ (для проверки self-update, 14.2 ТЗ).
func (w *WSClient) handleCommand(ctx context.Context, m proto.Msg, out chan<- proto.Msg) (*Reply, error) {
	if m.Type == proto.KindDrain {
		w.log.Info("node: drain", "on", m.On)
		return nil, nil
	}
	reply, err := w.node.Handle(ctx, m)
	if err != nil {
		return nil, err
	}
	if reply != nil {
		select {
		case out <- reply.msg():
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return reply, nil
}

// hostPort — host:port из ws-адреса (для HostIP).
func hostPort(u string) string {
	s := strings.TrimPrefix(u, wsScheme)
	s = strings.TrimPrefix(s, wssScheme)
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i]
	}
	return s
}
