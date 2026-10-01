package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	runpilotweb "runpilot"
	"runpilot/internal/api"
	"runpilot/internal/backup"
	"runpilot/internal/clock"
	"runpilot/internal/gateway"
	"runpilot/internal/model"
	"runpilot/internal/monitor"
	"runpilot/internal/scheduler"
	"runpilot/internal/store"
	"runpilot/internal/web"
	"runpilot/profiles"
)

// httpService — HTTP-слушатель координатора (web/api/gateway) для штатной
// остановки (раздел 14, K1).
type httpService struct {
	name string
	ln   net.Listener
	srv  *http.Server
}

// shutdownServices — штатная остановка (K1: SIGTERM): Shutdown всех
// HTTP-серверов в пределах grace; активные SSE/запросы дочитываются,
// новые подключения не принимаются. Возвращает true, если останов штатная.
func shutdownServices(services []httpService, grace time.Duration, log *slog.Logger) {
	sctx, cancel := context.WithTimeout(context.Background(), grace)
	defer cancel()
	for _, s := range services {
		if err := s.srv.Shutdown(sctx); err != nil {
			log.Warn("serve: shutdown " + s.name + ": " + err.Error())
		}
	}
}

// newServeCmd — координатор: API (канал узла WS proto=1, runpilot run,
// state) + шлюз gateway_port (Э3). Планировщик — Э4.
func newServeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Координатор runpilot (API, канал узла, шлюз; планировщик — Э4)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cfg.ValidateBind(); err != nil {
				return err
			}
			// v2 раздел 2.1/16: веб — только петлевой; пустой/короткий токен
			// запрещён (локальный режим удалён).
			if err := cfg.ValidateWeb(); err != nil {
				return err
			}
			if err := cfg.ValidateToken(os.Getenv(cfg.Coordinator.TokenEnv)); err != nil {
				return err
			}
			log := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
			dbPath, err := cfg.DBPath()
			if err != nil {
				return err
			}
			clk := clock.NewReal()
			// v2 (K6, раздел 2.1): quick_check перед открытием БД. Повреждена —
			// старт отклонён, [CRIT] в журнал + путь к последней копии.
			if _, serr := os.Stat(dbPath); serr == nil {
				if err := backup.QuickCheck(dbPath); err != nil {
					log.Error("runpilot: [CRIT] БД повреждена", "err", err.Error())
					if b, berr := backup.Latest(dbPath); berr == nil {
						log.Error("runpilot: восстановление из копии", "backup", b, "cmd", "runpilot restore "+b)
					}
					return fmt.Errorf("serve: БД повреждена: %w", err)
				}
			}
			// v2 раздел 4.8: первый старт переносит рабочие секции и серверы
			// из config.yaml в БД (копии файлов, откат при ошибке; второй
			// запуск — no-op). Конфиг переписывается в bootstrap-форму.
			if mig, err := store.MigrateFromYAML(dbPath, resolvedPath, version, clk); err != nil {
				return fmt.Errorf("serve: миграция настроек: %w", err)
			} else if !mig.Skipped {
				log.Info("serve: настройки перенесены из YAML в БД",
					"keys", mig.MigratedKeys,
					"db_backup", mig.DBBackup, "cfg_backup", mig.ConfigBackup)
			}
			// v2 (раздел 6.6): копия БД перед миграцией схемы.
			if cur, exists, err := store.SchemaVersionOf(dbPath); err == nil && exists && cur < store.LatestMigrationVersion() {
				if b, berr := backup.PreMigrationBackup(dbPath, version, clk.Now()); berr == nil && b != "" {
					log.Info("serve: копия БД перед миграцией", "backup", b)
				}
			}
			st, err := store.Open(dbPath)
			if err != nil {
				return err
			}
			defer func() { _ = st.Close() }()
			// v2 раздел 4: рабочие настройки — слой БД поверх defaults;
			// серверы — из таблицы server (если в БД есть).
			if err := st.LoadWorkingInto(cfg); err != nil {
				return fmt.Errorf("serve: настройки из БД: %w", err)
			}
			if servers, err := st.ListServers(); err != nil {
				return fmt.Errorf("serve: серверы из БД: %w", err)
			} else if len(servers) > 0 {
				cfg.Servers = servers
			}
			hub := api.NewHub(clk)
			srv := api.NewServer(cfg, st, hub, clk, log)
			srv.SetVersion(version)
			// v2 раздел 2.1: веб-слушатель — те же маршруты /api/v1/*,
			// cookie-аутентификация + CSRF (отдельной логики данных нет).
			webSrv := web.New(cfg, st, srv.Routes(), clk, log)
			webSrv.SetToken(os.Getenv(cfg.Coordinator.TokenEnv))
			webSrv.SetVersion(version)
			// Подключение узла (14.2): /enroll/<token>/… публично и на вебе
			// (curl -fsSL … | bash с чистой машины не имеет cookie веба).
			webSrv.SetEnrollHandler(srv.EnrollHandler())
			if webFS, err := runpilotweb.FS(); err == nil {
				webSrv.SetStaticFS(webFS)
			}
			// Терминал в браузере (13.4 ТЗ): /web/ws/term/{sid} → мост координатора.
			webSrv.SetTerminalHandler(func(w http.ResponseWriter, r *http.Request, sid, actor string) {
				srv.TerminalWS(w, r, sid, actor)
			})

			// Шлюз (раздел 6 ТЗ): WriteTimeout = 0 — длинные SSE-потоки.
			gw, err := gateway.NewGateway(cfg, st, clk, log)
			if err != nil {
				return fmt.Errorf("serve: шлюз: %w", err)
			}
			// v2 (S11): ключ обязателен (key_env задан), но переменная пуста в
			// окружении службы — [CRIT] + сервер не годится (KEY_MISSING).
			for _, sv := range gw.Servers().All() {
				if sv.State() != model.ServerKeyMissing {
					continue
				}
				env := sv.Cfg.Upstreams.OpenAI.KeyEnv
				log.Error("runpilot: [CRIT] сервер " + sv.Cfg.Name + ": переменная " + env +
					" не задана — добавьте `export " + env + "=…` в ~/.config/runpilot/secrets.env")
			}

			// Планировщик (раздел 5 ТЗ): адаптеры + хуки шлюза.
			prof, err := profiles.LoadQwen()
			if err != nil {
				return fmt.Errorf("serve: профиль: %w", err)
			}
			// Планировщик → монитор (раздел 10 ТЗ): health-серии +
			// метрики vLLM + ext; GPU-телеметрия — в hub (KindGPU).
			srvs := &gwServers{gw: gw.Servers(), hub: hub}
			sch := scheduler.New(scheduler.Options{
				Cfg:        cfg,
				Store:      st,
				Clk:        clk,
				Servers:    srvs,
				Panes:      &hubPanes{h: hub},
				Dispatch:   hub.Dispatch,
				ResumeText: prof.ResumeText,
				CancelKeys: []string{prof.CancelKeys},
				Notify:     func(text string) { log.Info("runpilot: " + text) },
				Wake:       gw.Wake,
				Log:        log,
				// v2 раздел 4.4: рабочие настройки пере-read'ятся из БД на
				// каждом тике (изменения действуют со следующего тика).
				Reload: func() error { return st.LoadWorkingInto(cfg) },
				// W6 (5.2): сервер, помеченный на удаление, стал свободен.
				OnServerIdle: srv.FinalizeServerRemoval,
			})
			mon := monitor.New(cfg, clk, monitor.HTTPFetcher(), sch,
				gw.Servers(), gw, log)
			srvs.mon = mon // после обоих: List() вызывается только в рантайме
			// v2 (S7/S8): наблюдение моделей — monitor → шлюз (сравнение,
			// SERVER_MODEL_CHANGED / MODEL_PROBLEM).
			mon.SetModelObserver(gateway.NewModelWatch(gw, clk))
			// item 3: наблюдение контекста — monitor → координатор (min по
			// серверам → msg.ContextWindow → qwen contextWindowSize).
			ctxObs := &ctxObserver{}
			mon.SetContextObserver(ctxObs)
			srv.SetMinContextWindow(ctxObs.Min)
			// v2 (S4/S5): отказы OOM/ENGINE_DEAD — шлюз → монитор (окно
			// отказов + карантин); SERVER_FAULT → журнал.
			gw.SetFaultReporter(mon)
			mon.SetEventSink(faultEventSink{st: st, clk: clk})
			// K4: место на диске БД (каждые monitor.disk_check_sec).
			mon.SetDiskPath(dbPath)
			gw.SetRequestHook(sch.RequestTick)
			gw.Servers().OnChange(sch.ServerState)
			srv.SetScheduler(sch)
			srv.SetMonitor(mon)
			srv.SetProfile(prof)
			srv.SetServerReg(gw.Servers())
			// W7 (6.2): аварийная остановка отменяет идущие потоки шлюза;
			// режим безопасности (EMERGENCY/SAFE_MODE) восстанавливается из
			// БД (переживает рестарт).
			sch.SetEmergencyHook(gw.CancelAll)
			if err := sch.LoadMode(); err != nil {
				log.Warn("serve: чтение режима из БД: " + err.Error())
			}
			// W7 (K6): ошибка записи в БД → SAFE_MODE (6.4).
			sch.ArmWriteErrorHook()
			// W7 (6.5): аварийное завершение координатора (kill -9/OOM) —
			// идущие ходы LOST → RESUME. До Start (без конкурентных тиков).
			if lost, err := sch.RecoverFromCrash(); err != nil {
				log.Warn("serve: восстановление после простоя: " + err.Error())
			} else if lost > 0 {
				log.Warn("serve: восстановление: " + strconv.Itoa(lost) + " ходов LOST → RESUME")
			}
			srv.SetServerDrain(func(name string, on bool) error {
				if on {
					gw.Servers().SetState(name, model.ServerDraining)
				} else {
					gw.Servers().SetState(name, model.ServerUp)
				}
				return nil
			})

			var (
				mu       sync.Mutex
				services []httpService
			)
			add := func(s httpService) {
				mu.Lock()
				services = append(services, s)
				mu.Unlock()
			}
			snapshot := func() []httpService {
				mu.Lock()
				defer mu.Unlock()
				out := make([]httpService, len(services))
				copy(out, services)
				return out
			}
			// WriteTimeout = 0: длинные SSE-потоки шлюза (раздел 6 ТЗ).
			newHTTP := func(name string, ln net.Listener, h http.Handler) httpService {
				return httpService{name, ln, &http.Server{Handler: h, WriteTimeout: 0}}
			}

			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			errCh := make(chan error, serveErrChanLen)

			// Веб: петлевой (web.bind проверен петлевым) — сразу доступен.
			webAddr := net.JoinHostPort(cfg.Web.Bind, strconv.Itoa(cfg.Web.Port))
			webLn, err := net.Listen("tcp", webAddr)
			if err != nil {
				return fmt.Errorf("serve: web listen %s: %w", webAddr, err)
			}
			webSvc := newHTTP("web", webLn, webSrv.Handler())
			add(webSvc)
			log.Info("serve: слушает", "role", "web", "addr", webLn.Addr().String())
			go func() {
				if err := webSvc.srv.Serve(webLn); err != nil && err != http.ErrServerClosed {
					errCh <- fmt.Errorf("web: %w", err)
				}
			}()

			// API и шлюз: coordinator.bind может появиться с задержкой (v2 2.3) —
			// повтор привязки с интервалом bind_retry_sec; веб при этом отвечает.
			bindRetry := time.Duration(cfg.Coordinator.BindRetrySec) * time.Second
			retryServe := func(name string, port int, h http.Handler) {
				addr := net.JoinHostPort(cfg.Coordinator.Bind, strconv.Itoa(port))
				ln, err := web.ListenWithRetry(ctx, addr, bindRetry, log)
				if err != nil {
					errCh <- fmt.Errorf("serve: %s listen %s: %w", name, addr, err)
					return
				}
				s := newHTTP(name, ln, h)
				add(s)
				log.Info("serve: слушает", "role", name, "addr", ln.Addr().String())
				if err := s.srv.Serve(ln); err != nil && err != http.ErrServerClosed {
					errCh <- fmt.Errorf("%s: %w", name, err)
				}
			}
			go retryServe("api", cfg.Coordinator.APIPort, srv.Handler())
			go retryServe("gateway", cfg.Coordinator.GatewayPort, gw.Handler())

			sch.Start(ctx)
			// W7 (6.5): «сердцебиение» — meta.last_alive_at каждые heartbeat_sec
			// (простой при старте → восстановление раздела 6.5).
			go heartbeatLoop(ctx, st, clk,
				time.Duration(cfg.Coordinator.HeartbeatSec)*time.Second, log)
			go mon.Run(ctx)              // Э6: health + /metrics + ext
			go hub.StartPinger(ctx, log) // Э6: ping координатор→узел
			go srv.TermIdleReaper(ctx)   // 13.4: закрытие терминалов по бездействию
			go srv.RetentionLoop(ctx)    // retention.*: очистка хранилища (раздел 14)
			// v2 2.5: встроённый узел (embedded_node=true) — сессии хоста
			// координатора видны без отдельного runpilot-node (внутренний транспорт).
			if cfg.Coordinator.EmbeddedNode {
				go embeddedNode(ctx, srv, hub, cfg, prof, clk, log)
			}
			log.Info("serve: координатор запущен", "version", version)

			select {
			case <-ctx.Done():
				// Штатная остановка (K1: SIGTERM): grace по shutdown_grace_sec.
				shutdownServices(snapshot(),
					time.Duration(cfg.Coordinator.ShutdownGraceSec)*time.Second, log)
				log.Info("serve: остановлен")
				return nil
			case err := <-errCh:
				return fmt.Errorf("serve: %w", err)
			}
		},
	}
}

// gwServers — scheduler.Servers поверх gateway.Servers (Э4) + метрики
// монитора и GPU-телеметрия hub (Э6).
type gwServers struct {
	gw  *gateway.Servers
	mon *monitor.Monitor
	hub *api.Hub
}

func (a *gwServers) List() []scheduler.ServerView {
	all := a.gw.All()
	out := make([]scheduler.ServerView, 0, len(all))
	for _, s := range all {
		v := scheduler.ServerView{
			Name:     s.Cfg.Name,
			Priority: s.Cfg.Priority,
			Slots:    s.Cfg.Slots,
			Accept:   s.Cfg.Accept,
			State:    s.State(),
		}
		if a.mon != nil {
			sm := a.mon.Sample(s.Cfg.Name)
			v.Running, v.Waiting, v.KV, v.GenTokS, v.Ext, v.Missing =
				sm.Running, sm.Waiting, sm.KV, sm.GenTokS, sm.Ext, sm.Missing
		}
		if a.hub != nil {
			if g, ok := a.hub.GPU(s.Cfg.Name); ok {
				v.GPUCards = g.Cards
				maxUtil := g.Cards[0].UtilPercent
				var used, total float64
				for _, c := range g.Cards {
					if c.UtilPercent > maxUtil {
						maxUtil = c.UtilPercent
					}
					used += c.VRAMUsedGB
					total += c.VRAMTotalGB
				}
				u := maxUtil
				v.GPUPct = &u
				vu, vt := used, total
				v.VRAMUsed, v.VRAMTotal = &vu, &vt
			}
		}
		out = append(out, v)
	}
	return out
}

// hubPanes — scheduler.PaneSource поверх api.Hub (Э4).
type hubPanes struct{ h *api.Hub }

func (a *hubPanes) PaneBySID(sid string) (scheduler.PaneSnap, bool) {
	p, ok := a.h.PaneBySID(sid)
	if !ok {
		return scheduler.PaneSnap{}, false
	}
	return toSnap(p), true
}

func (a *hubPanes) PaneByPaneID(paneID string) (scheduler.PaneSnap, bool) {
	p, ok := a.h.Pane(paneID)
	if !ok {
		return scheduler.PaneSnap{}, false
	}
	return toSnap(p), true
}

func toSnap(p *api.PaneInfo) scheduler.PaneSnap {
	return scheduler.PaneSnap{
		PaneID:     p.Pane.PaneID,
		SID:        p.Pane.SID,
		State:      model.PaneState(p.Pane.State),
		Hash:       p.Pane.Hash,
		InputEmpty: p.Pane.InputEmpty,
		ReceivedAt: p.ReceivedAt,
		Host:       p.Host,
	}
}

// ctxObserver — item 3: наблюдатель контекста модели (monitor.ContextObserver).
// На каждом успешном GET /v1/models монитор сообщает max_model_len настроенной
// модели сервера. Держим последнее известное значение по каждому серверу и
// отдаём минимум по всем (>0) — именно меньший из доступных контекстов
// объявляется Qwen Code (чтобы не обещать лишнего).
type ctxObserver struct {
	mu     sync.Mutex
	byName map[string]int
}

// OnModelContext — max_model_len настроенной модели сервера.
func (o *ctxObserver) OnModelContext(name string, maxModelLen int) {
	if maxModelLen <= 0 {
		return
	}
	o.mu.Lock()
	if o.byName == nil {
		o.byName = map[string]int{}
	}
	o.byName[name] = maxModelLen
	o.mu.Unlock()
}

// Min — наименьший известный max_model_len по серверам (0 — ещё неизвестен).
func (o *ctxObserver) Min() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	min := 0
	for _, v := range o.byName {
		if v > 0 && (min == 0 || v < min) {
			min = v
		}
	}
	return min
}

// heartbeatLoop — W7 (6.5): meta.last_alive_at каждые interval (монотонные
// часы internal/clock). При старте интервал [last_alive_at, now] > heartbeat
// даёт простой после аварийного завершения → восстановление 6.5.
func heartbeatLoop(ctx context.Context, st *store.Store, clk clock.Clock,
	interval time.Duration, log *slog.Logger) {
	if interval <= 0 {
		interval = time.Second
	}
	_ = st.SetLastAliveAt(clk.Now())
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := st.SetLastAliveAt(clk.Now()); err != nil {
				log.Warn("serve: heartbeat: " + err.Error())
			}
		}
	}
}

// faultEventSink — v2 (S4/S5): запись SERVER_FAULT (журнал) при переходе
// сервера в карантин. Реализует monitor.EventSink.
type faultEventSink struct {
	st  *store.Store
	clk clock.Clock
}

func (f faultEventSink) RecordServerFault(name string, fc model.FaultClass, count int) {
	payload, _ := json.Marshal(map[string]any{"class": string(fc), "count": count})
	_ = f.st.EventRecord(model.Event{
		TS: f.clk.Now(), Kind: model.KindServerFault, Server: name, Payload: payload,
	})
}
