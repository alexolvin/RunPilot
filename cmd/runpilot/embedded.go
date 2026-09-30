package main

// Встроенный узел хоста координатора (v2 раздел 2.5): тот же цикл runpilot-node
// (node.New + node.Run + node.RunGPU), но транспорт внутренний — снимки и
// команды ходят напрямую в api.Hub, без WS. Сессии хоста координатора видны
// в панели без отдельного сервиса runpilot-node.

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"runpilot/internal/api"
	"runpilot/internal/clock"
	"runpilot/internal/config"
	"runpilot/internal/node"
	"runpilot/internal/proto"
	"runpilot/profiles"
)

// memConn — внутренний транспорт команды «координатор → узел»: hub.Dispatch
// пишет proto.Msg через WriteJSON, он уходит в канал; reply — через send()
// обратно в hub (ApplyNodeMsg → DeliverReply). Удовлетворяет api.NodeConn.
type memConn struct {
	cmds chan proto.Msg
	done <-chan struct{}
}

func (c *memConn) WriteJSON(v any) error {
	m, ok := v.(proto.Msg)
	if !ok {
		return fmt.Errorf("memConn: ожидаю proto.Msg, а не %T", v)
	}
	select {
	case c.cmds <- m:
		return nil
	case <-c.done:
		return context.Canceled
	}
}

// WriteControl — WS-пинг координатора (hub.StartPinger); для внутреннего
// транспорта не нужен read deadline — ничего не делаем.
func (c *memConn) WriteControl(messageType int, data []byte, deadline time.Time) error {
	return nil
}

// embeddedNode — запускает встроённый узел: регистрация в hub + полный
// список панелей + цикл node.Run (снимки) + цикл команд + GPU. Завершается
// по отмене ctx.
func embeddedNode(ctx context.Context, srv *api.Server, hub *api.Hub,
	cfg *config.Config, prof *profiles.Qwen, clk clock.Clock, log *slog.Logger) {

	ex := node.CmdExecer{}
	n, err := node.New(cfg, prof, ex, clk, log)
	if err != nil {
		log.Warn("embedded: node: " + err.Error())
		return
	}
	// W9 доп-3c: эндпоинт qwen code — встроенный узел без WS-канала,
	// hello-ответ не приходит; правим конфиг на старте.
	n.EnsureQwenSettings(cfg.Profiles.Qwen.ModelAlias)
	host := node.Hostname()
	im := &memConn{cmds: make(chan proto.Msg, embeddedCmdChanLen), done: ctx.Done()}
	info := &api.NodeInfo{
		Host:        host,
		HostIP:      loopbackIP,
		RUNPILOTVersion:  version,
		TmuxVersion: node.TmuxVersion(ctx, ex),
		OS:          node.OS(),
		Sockets:     cfg.Node.TmuxSockets,
		Conn:        im,
		// 13.4: нажатия клавиш терминала → PTY-мастер узла (внутренний транспорт).
		PtySend: func(c int, d []byte) error { n.PtyWrite(c, d); return nil },
	}
	hub.Register(info)
	srv.NodeSeen(host)
	defer func() {
		srv.NodeLost(host)
		hub.Unregister(host)
	}()
	log.Info("embedded: узел хоста подключен", "host", host, "sockets", cfg.Node.TmuxSockets)

	// node → координатор: тот же путь, что и WS-транспорт (ApplyNodeMsg).
	send := func(m proto.Msg) error {
		srv.ApplyNodeMsg(host, m)
		return nil
	}

	// Стартовый полный список панелей (как KindPanes после hello в WS).
	// Prefill ДО списка: без первичного скана список пуст (цикл ещё не
	// отработал) → координатор сверкой с БД помечает живые сессии GONE
	// (ложная потеря на чистом рестарте).
	if err := n.Prefill(ctx); err != nil {
		log.Warn("embedded: prefill: " + err.Error())
	}
	_ = send(proto.Msg{Type: proto.KindPanes, Panes: n.PaneList()})

	// Координатор → узел: команды (dispatch/keys/…) → node.Handle → reply.
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case m := <-im.cmds:
				if err := n.HandleAndSend(ctx, m, send); err != nil {
					log.Warn("embedded: команда: " + err.Error())
				}
			}
		}
	}()

	// GPU-телеметрия (раздел 10 ТЗ), если включена.
	if n.GPUEnabled() {
		go func() { _ = n.RunGPU(ctx, send) }()
	}

	// 13.4: вывод PTY узла → координатор (в браузер-мост). Ставится до Run.
	n.SetPtySender(func(c int, d []byte) error { srv.ApplyPtyData(c, d); return nil })

	if err := n.Run(ctx, send); err != nil && ctx.Err() == nil {
		log.Warn("embedded: цикл: " + err.Error())
	}
}
