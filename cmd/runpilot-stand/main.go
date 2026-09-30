// Command runpilot-stand — каркас стенда (v2 раздел 19.1; W1 — каркас, полный
// стенд с узлами/сценариями/скриншотами — W3).
//
// В одном процессе: координатор (API + веб) на петлевых адресах, ВИРТУАЛЬНЫЕ
// часы (скриншоты воспроизводимы) и временная БД. Управляющий порт существует
// ТОЛЬКО здесь — в рабочем бинарнике runpilot его нет. Сценарии (W3) приводят
// систему в состояние только через публичный API и управляющие порты узлов.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	runpilotweb "runpilot"
	"runpilot/internal/api"
	"runpilot/internal/clock"
	"runpilot/internal/config"
	"runpilot/internal/model"
	"runpilot/internal/proto"
	"runpilot/internal/scheduler"
	"runpilot/internal/store"
	"runpilot/internal/web"
)

var version = "0.0.0-dev" // подставляется ldflags, как в runpilot

func main() {
	var (
		webPort     = flag.Int("web-port", 0, "порт веб-слушателя (0 = случайный)")
		controlPort = flag.Int("control-port", 0, "порт управляющего порта (0 = случайный)")
		token       = flag.String("token", "", "операторский токен (или RUNPILOT_STAND_TOKEN)")
		start       = flag.String("start", defaultClockStart, "старт виртуальных часов (RFC3339)")
		keepDB      = flag.Bool("keep-db", false, "не удалять временную БД при выходе")
		infoFile    = flag.String("info-file", "", "записать JSON {web_url, control_url, token} по готовности")
		scenario    = flag.String("scenario", "normal", "сценарий W4: empty/normal/many/faults")
	)
	flag.Parse()

	log := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	die := func(format string, args ...any) {
		log.Error(fmt.Sprintf(format, args...))
		os.Exit(1)
	}

	if *token == "" {
		*token = os.Getenv(tokenEnv)
	}
	if err := config.Defaults().ValidateToken(*token); err != nil {
		die("нужен операторский токен: --token или %s (%v)", tokenEnv, err)
	}

	startT, err := time.Parse(time.RFC3339, *start)
	if err != nil {
		die("некорректный --start (RFC3339): %v", err)
	}
	clk := clock.NewVirtual(startT)

	dir, err := os.MkdirTemp("", "runpilot-stand-*")
	if err != nil {
		die("временная БД: %v", err)
	}
	if !*keepDB {
		defer func() { _ = os.RemoveAll(dir) }()
	}
	st, err := store.Open(filepath.Join(dir, dbFile))
	if err != nil {
		die("БД: %v", err)
	}
	defer func() { _ = st.Close() }()

	cfg := config.Defaults()
	cfg.Web.Bind = loopback
	cfg.Web.PublicURL = "" // http → cookie без Secure (петлевой стенд)

	hub := api.NewHub(clk)
	srv := api.NewServer(cfg, st, hub, clk, log)
	srv.SetVersion(version)
	// W4: статичные серверы + планировщик (без тиков) → /state, /queue,
	// /monitoring, /notifications. Данные — через публичные методы store/hub.
	// emergency/safe_mode/offline — поверх базового сценария normal (режим
	// принудительный; offline снимается конвейером после обрыва SSE).
	// Сценарий снимка может нести суффикс после "~" (уникальный стенд на шаг
	// J-сценария: окна 1440/390 не делят деструктивное состояние). Сид и
	// режим — по базовой части до "~".
	seedScen := strings.SplitN(*scenario, "~", scenarioParts)[0]
	// W7-алиасы: уникальные имена снимков (не сталкиваются с golden W4 при том
	// же сценарии), но тот же сид/режим.
	switch seedScen {
	case "w7-faults":
		seedScen = "faults"
	case "w7-emerg":
		seedScen = "emergency"
	case "w7-safe":
		seedScen = "safe_mode"
	case "w7-offline":
		seedScen = "offline"
	}
	baseScenario := seedScen
	if baseScenario == "emergency" || baseScenario == "safe_mode" || baseScenario == "offline" {
		baseScenario = "normal"
	}
	// Базовая ревизия настроек (source=migration): точка отката для J8, как
	// в продакшн (раздел 4.8). Без неё «откат ревизии» не вернул бы исходное
	// значение (откат применяет снимок целевой ревизии). Пустой diff (полный
	// снимок без «old:null» в превью). Публичный метод store (R3).
	if snap, err := cfg.WorkingSnapshot(); err == nil {
		_, _ = st.InsertWorkingFull(snap, []store.SettingChange{}, "migration", "migration", clk.Now())
	}
	servers := seedWorld(st, hub, clk.Now(), baseScenario)
	sch := scheduler.New(scheduler.Options{
		Cfg:     cfg,
		Store:   st,
		Clk:     clk,
		Servers: servers,
		Panes:   noopPanes{},
		Dispatch: func(context.Context, string, proto.Msg) (proto.Msg, error) {
			return proto.Msg{}, nil
		},
		Log: log,
	})
	switch seedScen {
	case "emergency":
		sch.SetModeForced("EMERGENCY")
	case "safe_mode":
		sch.SetModeForced("SAFE_MODE")
	}
	srv.SetScheduler(sch)
	// Те же маршруты /api/v1/*, что и у runpilot, но cookie-аутентификация + CSRF.
	webSrv := web.New(cfg, st, srv.Routes(), clk, log)
	webSrv.SetToken(*token)
	webSrv.SetVersion(version)
	webSrv.SetEnrollHandler(srv.EnrollHandler())
	if webFS, err := runpilotweb.FS(); err == nil {
		webSrv.SetStaticFS(webFS)
	}
	// W8 (13.4): терминал-эхо для скриншотов/е2е (реального tmux в стенде нет).
	webSrv.SetTerminalHandler(mockTerminal)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	webLn, err := net.Listen("tcp", net.JoinHostPort(loopback, strconv.Itoa(*webPort)))
	if err != nil {
		die("web listen: %v", err)
	}
	ctrlLn, err := net.Listen("tcp", net.JoinHostPort(loopback, strconv.Itoa(*controlPort)))
	if err != nil {
		die("control listen: %v", err)
	}
	webURL := httpScheme + webLn.Addr().String()
	ctrlURL := httpScheme + ctrlLn.Addr().String()

	// Управляющий порт стенда: статус (только /status).
	ctrlMux := http.NewServeMux()
	ctrlMux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"version":   version,
			"web_url":   webURL,
			"control_url": ctrlURL,
			"clock":     clk.Now().UTC().Format(time.RFC3339),
			"db":        filepath.Join(dir, dbFile),
		})
	})

	// Веб-порт: служебные /control/* (CONTROL W4; same-origin — страница
	// дергает их fetch'ем на своих часах) + веб/API (cookie-аутентификация).
	rootMux := http.NewServeMux()
	registerControl(rootMux, st, srv, clk)
	rootMux.Handle("/", webSrv.Handler())

	go func() {
		s := &http.Server{Handler: rootMux, WriteTimeout: 0}
		if err := s.Serve(webLn); err != nil && err != http.ErrServerClosed {
			log.Error("stand: web: " + err.Error())
		}
	}()
	go func() {
		s := &http.Server{Handler: ctrlMux}
		if err := s.Serve(ctrlLn); err != nil && err != http.ErrServerClosed {
			log.Error("stand: control: " + err.Error())
		}
	}()

	log.Info("stand: готов",
		"web", webURL, "control", ctrlURL, "scenario", *scenario,
		"clock", clk.Now().UTC().Format(time.RFC3339))
	fmt.Printf("веб:      %s\nуправл:  %s\nсценарий: %s\n", webURL, ctrlURL, *scenario)

	if *infoFile != "" {
		info := map[string]string{"web_url": webURL, "control_url": ctrlURL, "token": *token}
		if b, err := json.MarshalIndent(info, "", "  "); err == nil {
			if err := os.WriteFile(*infoFile, b, infoFileMode); err != nil {
				die("info-file: %v", err)
			}
		}
	}

	<-ctx.Done()
	log.Info("stand: остановлен")
}

// registerControl — служебные точки CONTROL W4 (петлевой, same-origin для
// страницы): fire — N событий SSE; state — смена состояния сессии
// (SESSION_STATE + HOLD); drop-sse — обрыв всех SSE-клиентов (RESYNC).
func registerControl(mux *http.ServeMux, st *store.Store, srv *api.Server, clk clock.Clock) {
	mux.HandleFunc("POST /control/fire", func(w http.ResponseWriter, r *http.Request) {
		n, _ := strconv.Atoi(r.URL.Query().Get("n"))
		if n <= 0 {
			n = 1
		}
		for i := 0; i < n; i++ {
			b, _ := jsonMarshal(map[string]any{"kind": "NOTIFY", "i": i})
			_ = st.EventRecord(model.Event{TS: clk.Now(), Kind: model.KindNotify, SID: "ctl", Payload: b})
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /control/state", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			SID  string `json:"sid"`
			To   string `json:"to"`
			Hold string `json:"hold"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.SID == "" || req.To == "" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		from := "IDLE"
		if rec, err := st.GetSession(req.SID); err == nil {
			from = string(rec.State)
		}
		if err := st.SetSessionState(req.SID, model.SessionState(req.To), model.HoldReason(req.Hold), clk.Now()); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		b, _ := jsonMarshal(map[string]any{"from": from, "to": req.To})
		_ = st.EventRecord(model.Event{TS: clk.Now(), Kind: model.KindSessionState, SID: req.SID, Payload: b})
		if req.Hold != "" {
			bh, _ := jsonMarshal(map[string]any{"reason": req.Hold})
			_ = st.EventRecord(model.Event{TS: clk.Now(), Kind: model.KindHold, SID: req.SID, Payload: bh})
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /control/drop-sse", func(w http.ResponseWriter, r *http.Request) {
		srv.DropSSE()
		w.WriteHeader(http.StatusNoContent)
	})
}
