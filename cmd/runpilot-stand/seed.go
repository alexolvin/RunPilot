// Сид стенда W4 (v2 раздел 19.1): статичные серверы + сессии/очередь/аренды/
// ходы/события/узлы для скриншотов всех экранов и CONTROL (SSE/RESYNC).
// Данные — только через публичные методы store/hub (R3). Числа — consts.go (R2).
package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"runpilot/internal/api"
	"runpilot/internal/config"
	"runpilot/internal/model"
	"runpilot/internal/proto"
	"runpilot/internal/scheduler"
	"runpilot/internal/store"
)

var jsonMarshal = json.Marshal

// standServers — статичные серверы стенда (scheduler.Servers).
type standServers struct{ views []scheduler.ServerView }

func (a standServers) List() []scheduler.ServerView { return a.views }

// dbServers — динамический источник серверов (J6): читает таблицу server из
// БД на каждый List() и намеряет живые метрики из статичного среза по имени.
// CRUD веб-API (мастер, удаление) попадает в БД → серверы появляются/
// «удаляются»/исчезают в /state, как в продакшн (реестр шлюза + монитор).
// Метрики (KV/Gen/GPU/VRAM) не хранятся в таблице server — их несёт монитор;
// в стенде это фиксированный срез met (только для сидированных серверов).
type dbServers struct {
	st  *store.Store
	met map[string]scheduler.ServerView
}

func (d *dbServers) List() []scheduler.ServerView {
	srvs, err := d.st.ListServers()
	if err != nil {
		return []scheduler.ServerView{}
	}
	out := make([]scheduler.ServerView, 0, len(srvs))
	for _, s := range srvs {
		v := scheduler.ServerView{Name: s.Name, Priority: s.Priority, Slots: s.Slots,
			Accept: s.Accept, State: model.ServerUp}
		if m, ok := d.met[s.Name]; ok {
			v.Running, v.Waiting = m.Running, m.Waiting
			v.KV, v.GenTokS, v.Ext, v.Missing = m.KV, m.GenTokS, m.Ext, m.Missing
			v.GPUPct, v.VRAMUsed, v.VRAMTotal = m.GPUPct, m.VRAMUsed, m.VRAMTotal
			v.GPUCards, v.State = m.GPUCards, m.State
		}
		out = append(out, v)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Priority > out[j].Priority })
	return out
}

// noopPanes — планировщик стенда не тикает (статичный срез); снимки панелей
// не нужны для /state и /queue.
type noopPanes struct{}

func (noopPanes) PaneBySID(string) (scheduler.PaneSnap, bool) { return scheduler.PaneSnap{}, false }

func (noopPanes) PaneByPaneID(string) (scheduler.PaneSnap, bool) { return scheduler.PaneSnap{}, false }

func pInt(v int) *int           { return &v }
func pFloat(v float64) *float64 { return &v }

func srvView(name string, prio, slots int, state model.ServerState,
	kv, gen float64, gpu int, vu, vt float64) scheduler.ServerView {
	return scheduler.ServerView{
		Name: name, Priority: prio, Slots: slots, State: state,
		Accept: []string{"*"},
		KV: pFloat(kv), GenTokS: pFloat(gen), GPUPct: pInt(gpu),
		VRAMUsed: pFloat(vu), VRAMTotal: pFloat(vt),
	}
}

// seedWorld — наполняет БД и хаб по профилю; возвращает источник серверов.
func seedWorld(st *store.Store, hub *api.Hub, now time.Time, profile string) scheduler.Servers {
	switch profile {
	case "empty":
		return &standServers{views: []scheduler.ServerView{}}
	case "many":
		return seedMany(st, hub, now)
	case "faults":
		return seedFaults(st, hub, now)
	case "attention":
		return seedAttention(st, hub, now)
	case "w7-ext":
		return seedW7Ext(st, hub, now)
	case "w7-node":
		return seedW7Node(st, hub, now)
	case "w7-untested":
		return seedW7Untested(st, hub, now)
	default:
		return seedNormal(st, hub, now)
	}
}

// --- вспомогательные сиды ---

func newSID(i int) string { return fmt.Sprintf("SS%08d", i) }

func addSession(st *store.Store, i int, name, host string,
	state model.SessionState, class model.QueueClass, hold model.HoldReason, now time.Time) string {
	sid := newSID(i)
	rec := store.SessionRecord{
		SID: sid, Name: name, Host: host, HostIP: nNodeIP,
		TmuxSession: name, Profile: "qwen", State: state,
		StateChangedAt: now, HoldReason: hold, Class: class,
		ConstraintKind: model.ConstraintNone, CreatedAt: now,
	}
	_ = st.CreateSession(rec)
	return sid
}

func addLease(st *store.Store, sid, server string, slot int, now time.Time) {
	_, _ = st.LeaseCreate(model.Lease{
		SID: sid, Server: server, Slot: slot, State: model.LeaseActive,
		Origin: model.LeaseOriginDispatch, GrantedAt: now,
	})
}

func addQueue(st *store.Store, sid string, class model.QueueClass,
	ineligible string, now time.Time) {
	_ = st.QueueUpsert(model.QueueEntry{
		SID: sid, Class: class, EnqueuedAt: now, Mode: model.QueueModeSubmit,
		IneligibleReason: ineligible, IneligibleSince: now,
	})
}

func addTurn(st *store.Store, sid string, start, end time.Time,
	outcome model.TurnOutcome, requests int, server string) {
	id, err := st.TurnStart(sid, start)
	if err != nil {
		return
	}
	_ = st.TurnClose(id, end, outcome, requests, server)
}

func addEvent(st *store.Store, kind, sid, server string, payload map[string]any, ts time.Time) {
	b, _ := jsonMarshal(payload)
	_ = st.EventRecord(model.Event{TS: ts, Kind: kind, SID: sid, Server: server, Payload: b})
}

// seedNode — узел в хабе (для /state и экрана «Узлы»).
func seedNode(hub *api.Hub, host, ip, ver string, sockets []string) {
	hub.Register(&api.NodeInfo{Host: host, HostIP: ip, RUNPILOTVersion: ver,
		TmuxVersion: "3.4", OS: "linux", Sockets: sockets})
}

// seedPane — снимок панели узла (PROMPT → уведомление «ждёт разрешения»).
func seedPane(hub *api.Hub, host, sid, name, state string) {
	hub.SetPanes(host, []proto.Pane{{
		PaneID: "p-" + sid, SID: sid, State: state, InputEmpty: state != "BUSY",
	}})
}

// --- профили ---

func seedNormal(st *store.Store, hub *api.Hub, now time.Time) *dbServers {
	views := []scheduler.ServerView{
		srvView(nSrvOne, nOnePrio, nOneSlots, model.ServerUp, nOneKV, nOneGen, nOneGPU, nOneVU, nOneVT),
		srvView(nSrvTwo, nTwoPrio, nTwoSlots, model.ServerUp, nTwoKV, nTwoGen, nTwoGPU, nTwoVU, nTwoVT),
	}
	met := make(map[string]scheduler.ServerView, len(views))
	for _, v := range views {
		met[v.Name] = v
	}
	// Серверы в таблице server (БД): CRUD веб-API (мастер/удаление, J6)
	// отражается в /state через dbServers.List.
	_ = st.UpsertServer(config.Server{Name: nSrvOne, Priority: nOnePrio, Slots: nOneSlots, Accept: []string{"*"}}, now)
	_ = st.UpsertServer(config.Server{Name: nSrvTwo, Priority: nTwoPrio, Slots: nTwoSlots, Accept: []string{"*"}}, now)

	// Узел + сессии всех состояний.
	seedNode(hub, nNodeHost, nNodeIP, nNodeVer, []string{nSockA, nSockB})
	var idx int
	next := func() int { idx++; return idx }

	// Работающие (аренды на srv-01).
	for i := 0; i < nRunningN; i++ {
		sid := addSession(st, next(), fmt.Sprintf("task-run-%d", i+1), nNodeHost,
			model.SessionRunning, model.ClassNormal, "", now)
		addLease(st, sid, nSrvOne, nLeaseSlotA+i, now)
		seedPane(hub, nNodeHost, sid, fmt.Sprintf("task-run-%d", i+1), "BUSY")
	}
	// Запускающаяся.
	addSession(st, next(), "task-dispatch", nNodeHost, model.SessionDispatching, model.ClassHigh, "", now)
	// Очередь (одна — непригодна).
	queueNames := []string{"task-q1", "task-q2", "task-q3"}
	for i, nm := range queueNames {
		sid := addSession(st, next(), nm, nNodeHost, model.SessionQueued, model.ClassNormal, "", now)
		inf := ""
		if i == 1 {
			inf = "kv_full"
		}
		addQueue(st, sid, model.ClassNormal, inf, now.Add(-queueWait(i)))
	}
	// Требует внимания (HOLD) + панель в PROMPT.
	holdSid := addSession(st, next(), "task-hold", nNodeHost,
		model.SessionHold, model.ClassNormal, model.HoldOperator, now)
	seedPane(hub, nNodeHost, holdSid, "task-hold", "PROMPT")
	// Свободные.
	for i := 0; i < nIdleN; i++ {
		addSession(st, next(), fmt.Sprintf("task-idle-%d", i+1), nNodeHost,
			model.SessionIdle, model.ClassNormal, "", now)
	}
	// Отсоединённая.
	addSession(st, next(), "task-detached", nNodeHost, model.SessionDetached, model.ClassLow, "", now)

	seedTurns(st, now, nTurnN, []string{nSrvOne, nSrvTwo})
	seedEvents(st, now, nEventN, views)
	return &dbServers{st: st, met: met}
}

func queueWait(i int) time.Duration { return time.Duration(nEventStepSec*(i+nSlotStep)) * time.Second }

// seedAttention — сценарий «attention» (19.1): HOLD разных кодов, панель
// PROMPT (ждёт подтверждения), UNMANAGED-панель, внешняя нагрузка (cron).
func seedAttention(st *store.Store, hub *api.Hub, now time.Time) *standServers {
	views := []scheduler.ServerView{
		srvView(nSrvOne, nOnePrio, nOneSlots, model.ServerUp, nOneKV, nOneGen, nOneGPU, nOneVU, nOneVT),
		srvView(nSrvTwo, nTwoPrio, nTwoSlots, model.ServerUp, nTwoKV, nTwoGen, nTwoGPU, nTwoVU, nTwoVT),
	}
	views[0].Ext = pInt(aExtLoad) // внешняя нагрузка от cron на srv-01

	seedNode(hub, nNodeHost, nNodeIP, nNodeVer, []string{nSockA, nSockB})
	var idx int
	next := func() int { idx++; return idx }

	// PROMPT: работающая сессия ждёт подтверждения (PROMPT-панель).
	promptSid := addSession(st, next(), "task-prompt", nNodeHost,
		model.SessionRunning, model.ClassNormal, "", now)
	addLease(st, promptSid, nSrvOne, nLeaseSlotA, now)
	seedPane(hub, nNodeHost, promptSid, "task-prompt", "PROMPT")

	// HOLD разных кодов.
	holdCases := []struct {
		name   string
		reason model.HoldReason
	}{
		{"task-hold-op", model.HoldOperator},
		{"task-hold-noc", model.HoldStartNotConfirmed},
		{"task-hold-agent", model.HoldAgentExited},
	}
	for _, hc := range holdCases {
		addSession(st, next(), hc.name, nNodeHost, model.SessionHold, model.ClassNormal, hc.reason, now)
	}

	// UNMANAGED: панель с кодером, запущенным вне runpilot run.
	seedPane(hub, nNodeHost, "unmanaged-cron", "cron-job", "UNMANAGED")

	seedTurns(st, now, nTurnN, []string{nSrvOne, nSrvTwo})
	seedEvents(st, now, nEventN, views)
	return &standServers{views: views}
}

func seedMany(st *store.Store, hub *api.Hub, now time.Time) *standServers {
	// 4 сервера, 8 узлов.
	views := []scheduler.ServerView{}
	for i := 0; i < mServerN; i++ {
		name := fmt.Sprintf("%s%d", mSrvPrefix, i+1)
		views = append(views, srvView(name, mSrvPrio, mSlotEach, model.ServerUp,
			mKVBase+float64(i)*mKVStep, mGenBase+float64(i)*mGenStep,
			mGPUBase+i*mGPUStep, mVU, mVT))
	}
	nodes := make([]string, mNodeN)
	for i := 0; i < mNodeN; i++ {
		host := fmt.Sprintf("node-%d", i+1)
		nodes[i] = host
		seedNode(hub, host, mNodeIPPref+fmt.Sprint(i+1), nNodeVer, []string{"/tmp/ssh9999/a"})
	}
	// 60 сессий по узлам: работающие/очередь/свободные/внимание.
	var idx int
	next := func() int { idx++; return idx }
	for i := 0; i < mSessionN; i++ {
		host := nodes[i%mNodeN]
		name := fmt.Sprintf("job-%02d", i+1)
		var state model.SessionState
		var class model.QueueClass
		switch {
		case i%mSlotEach < mRunCut:
			state = model.SessionRunning
			class = model.ClassNormal
			sid := addSession(st, next(), name, host, state, class, "", now)
			addLease(st, sid, views[i%mServerN].Name, (i%mSlotEach)+nLeaseSlotA, now)
		case i%mSlotEach < mQueueCut:
			state = model.SessionQueued
			class = model.ClassNormal
			sid := addSession(st, next(), name, host, state, class, "", now)
			addQueue(st, sid, class, "", now.Add(-queueWait(i)))
		case i%mSlotEach == mHoldAt:
			state = model.SessionHold
			class = model.ClassHigh
			addSession(st, next(), name, host, state, class, model.HoldOperator, now)
		default:
			state = model.SessionIdle
			class = model.ClassLow
			addSession(st, next(), name, host, state, class, "", now)
		}
	}
	seedTurns(st, now, mTurnN, []string{views[0].Name, views[1].Name})
	seedEvents(st, now, mEventN, views)
	return &standServers{views: views}
}

func seedFaults(st *store.Store, hub *api.Hub, now time.Time) *standServers {
	views := []scheduler.ServerView{
		srvView(fSrvOne, fSrvPrioUp, fSlots, model.ServerUp, fOneKV, fOneGen, fOneGPU, fOneVU, fOneVT),
		srvView(fSrvTwo, fSrvPrioUp, fSlots, model.ServerState("DOWN"), 0, 0, 0, 0, 0),
		srvView(fSrvThr, fSrvPrioUp, fSlots, model.ServerState("QUARANTINED"), 0, 0, 0, 0, 0),
		srvView(fSrvFou, fSrvPrioLow, fSlots, model.ServerState("MODEL_PROBLEM"), 0, 0, 0, 0, 0),
	}
	seedNode(hub, nNodeHost, nNodeIP, nNodeVer, []string{nSockA})
	var idx int
	next := func() int { idx++; return idx }
	addSession(st, next(), "ok-run", nNodeHost, model.SessionRunning, model.ClassNormal, "", now)
	addSession(st, next(), "oom-hold", nNodeHost, model.SessionHold, model.ClassNormal, model.HoldEmergency, now)
	addSession(st, next(), "blocked-q", nNodeHost, model.SessionQueued, model.ClassNormal, "", now)
	addQueue(st, newSID(idx), model.ClassNormal, "server_down", now.Add(-queueWait(0)))
	seedTurns(st, now, nTurnN, []string{fSrvOne})
	seedEvents(st, now, nEventN, views)
	return &standServers{views: views}
}

// seedTurns — ходы за 24 часа (исходы, длительности) для статистики.
func seedTurns(st *store.Store, now time.Time, n int, servers []string) {
	outcomes := []model.TurnOutcome{model.TurnOK, model.TurnOK, model.TurnOK,
		model.TurnError, model.TurnOK, model.TurnCancelled}
	for i := 0; i < n; i++ {
		sid := newSID(sidBase + i)
		end := now.Add(-time.Duration(i) * time.Duration(nTurnStepMin) * time.Minute)
		start := end.Add(-time.Duration(nTurnDurSec) * time.Second / turnDurDiv)
		addTurn(st, sid, start, end, outcomes[i%len(outcomes)], (i%turnReqMod)+1, servers[i%len(servers)])
	}
}

// seedEvents — лента событий (для журнала и SSE-дозагрузки).
func seedEvents(st *store.Store, now time.Time, n int, views []scheduler.ServerView) {
	kinds := []string{model.KindSessionState, model.KindQueueEnqueue,
		model.KindTurnEnd, model.KindServerState}
	for i := 0; i < n; i++ {
		ts := now.Add(-time.Duration(i) * time.Duration(nEventStepSec) * time.Second)
		kind := kinds[i%len(kinds)]
		sid := ""
		server := ""
		payload := map[string]any{}
		switch kind {
		case model.KindSessionState:
			sid = newSID(sidBase + i%sidModA)
			payload = map[string]any{"from": "IDLE", "to": "RUNNING"}
		case model.KindServerState:
			server = views[i%len(views)].Name
			payload = map[string]any{"from": "UP", "to": "UP"}
		case model.KindQueueEnqueue:
			sid = newSID(sidBaseQ + i%sidModQ)
			payload = map[string]any{"class": "normal"}
		default:
			sid = newSID(sidBase + i%sidModA)
		}
		addEvent(st, kind, sid, server, payload, ts)
	}
}

// --- W7 (Устойчивость): внешние кодеры, здоровье узла, версия qwen ---

// addSessionVer — сессия с указанием версии qwen (C6/U3: untested-флаг).
func addSessionVer(st *store.Store, i int, name, host, ver string,
	state model.SessionState, class model.QueueClass, hold model.HoldReason, now time.Time) string {
	sid := newSID(i)
	rec := store.SessionRecord{
		SID: sid, Name: name, Host: host, HostIP: nNodeIP,
		TmuxSession: name, Profile: "qwen", State: state,
		StateChangedAt: now, HoldReason: hold, Class: class,
		ConstraintKind: model.ConstraintNone, CreatedAt: now, AgentVersion: ver,
	}
	_ = st.CreateSession(rec)
	return sid
}

// addExternal — внешний кодер (8.2) в БД: host/pid/source/exe/flags/target/status.
func addExternal(st *store.Store, host string, pid int, source, exe, flags, target, status string,
	startSec int, now time.Time) {
	_, _ = st.ExternalUpsert(&model.ExternalProcess{
		Host: host, PID: pid, UID: xUID, StartTime: now.Unix() - int64(startSec),
		Source: source, Exe: exe, Flags: flags, Target: target, Status: status,
		FirstSeen: now.Add(-time.Duration(startSec) * time.Second), LastSeen: now,
	})
}

// seedW7Ext — X-rows + J9: cron запускает qwen -p (обход) и runpilot exec (JOB),
// один процесс завершён оператором («Завершить»).
func seedW7Ext(st *store.Store, hub *api.Hub, now time.Time) *standServers {
	views := []scheduler.ServerView{
		srvView(nSrvOne, nOnePrio, nOneSlots, model.ServerUp, nOneKV, nOneGen, nOneGPU, nOneVU, nOneVT),
		srvView(nSrvTwo, nTwoPrio, nTwoSlots, model.ServerUp, nTwoKV, nTwoGen, nTwoGPU, nTwoVU, nTwoVT),
	}
	views[0].Ext = pInt(aExtLoad) // S10: внешняя нагрузка на srv-01

	seedNode(hub, nNodeHost, nNodeIP, nNodeVer, []string{nSockA})
	var idx int
	next := func() int { idx++; return idx }

	// JOB от runpilot exec (X3): работающая сессия на srv-02.
	jobSid := addSession(st, next(), "job-cron", nNodeHost, model.SessionRunning, model.ClassNormal, "", now)
	addLease(st, jobSid, nSrvTwo, nLeaseSlotA, now)
	// Ходящее задание в очереди.
	qsid := addSession(st, next(), "task-q1", nNodeHost, model.SessionQueued, model.ClassNormal, "", now)
	addQueue(st, qsid, model.ClassNormal, "", now.Add(-queueWait(0)))

	// X2: cron qwen -p в обход (target = server:srv-01) — активен.
	addExternal(st, nNodeHost, xPIDCron, "cron", "qwen", "-p", model.ExtTargetServer+nSrvOne, model.ExtActive, xStartSec, now)
	// X3: cron runpilot exec (target = server:srv-02) — активен.
	addExternal(st, nNodeHost, xPIDExec, "cron", "runpilot", "exec", model.ExtTargetServer+nSrvTwo, model.ExtActive, xStartSec+xStartOffExec, now)
	// «Завершить»: процесс, которого больше нет после TERM.
	addExternal(st, nNodeHost, xPIDKilled, "cron", "qwen", "-p", model.ExtTargetServer+nSrvOne, model.ExtKilled, xStartSec+xStartOffKilled, now)

	seedTurns(st, now, nTurnN, []string{nSrvOne, nSrvTwo})
	seedEvents(st, now, nEventN, views)
	return &standServers{views: views}
}

// seedW7Node — N9/N10: node-02 без tmux/qwen, мало места, часы впереди на
// wnSkewMS; node-01 — штатный.
func seedW7Node(st *store.Store, hub *api.Hub, now time.Time) *standServers {
	views := []scheduler.ServerView{
		srvView(nSrvOne, nOnePrio, nOneSlots, model.ServerUp, nOneKV, nOneGen, nOneGPU, nOneVU, nOneVT),
		srvView(nSrvTwo, nTwoPrio, nTwoSlots, model.ServerUp, nTwoKV, nTwoGen, nTwoGPU, nTwoVU, nTwoVT),
	}
	seedNode(hub, nNodeHost, nNodeIP, nNodeVer, []string{nSockA, nSockB})
	seedNode(hub, wnNodeHost, wnNodeIP, nNodeVer, []string{nSockA})
	// Проблемы + расхождение часов (N9/N10) — напрямую в хаб (14.4).
	hub.SetNodeHealth(wnNodeHost, api.NodeHealthView{
		Problems:   []string{"no_tmux", "no_qwen", "disk_low"},
		TimeSkewMS: wnSkewMS, DiskFreeMB: wnDiskFree, QwenVersion: wnQwenVer,
	})

	var idx int
	next := func() int { idx++; return idx }
	for i := 0; i < nRunningN; i++ {
		sid := addSession(st, next(), fmt.Sprintf("task-run-%d", i+1), nNodeHost,
			model.SessionRunning, model.ClassNormal, "", now)
		addLease(st, sid, nSrvOne, nLeaseSlotA+i, now)
	}
	qsid := addSession(st, next(), "task-q1", nNodeHost, model.SessionQueued, model.ClassNormal, "", now)
	addQueue(st, qsid, model.ClassNormal, "", now.Add(-queueWait(0)))
	// Сессии на проблемном узле продолжают (N9: «идущие сессии продолжают»).
	addSession(st, next(), "task-run-9", wnNodeHost, model.SessionRunning, model.ClassNormal, "", now)

	seedTurns(st, now, nTurnN, []string{nSrvOne, nSrvTwo})
	seedEvents(st, now, nEventN, views)
	return &standServers{views: views}
}

// seedW7Untested — C6/U3: сессии с разной версией qwen (tested → без бейджа,
// untested → бейдж «версия не проверена»).
func seedW7Untested(st *store.Store, hub *api.Hub, now time.Time) *standServers {
	views := []scheduler.ServerView{
		srvView(nSrvOne, nOnePrio, nOneSlots, model.ServerUp, nOneKV, nOneGen, nOneGPU, nOneVU, nOneVT),
	}
	seedNode(hub, nNodeHost, nNodeIP, nNodeVer, []string{nSockA})
	var idx int
	next := func() int { idx++; return idx }
	addSessionVer(st, next(), "task-tested", nNodeHost, vTested,
		model.SessionIdle, model.ClassNormal, "", now)
	addSessionVer(st, next(), "task-untested", nNodeHost, vUntested,
		model.SessionIdle, model.ClassNormal, "", now)
	addSessionVer(st, next(), "task-untested2", nNodeHost, vUntested2,
		model.SessionQueued, model.ClassNormal, "", now)
	addQueue(st, newSID(idx), model.ClassNormal, "", now.Add(-queueWait(0)))
	seedTurns(st, now, nTurnN, []string{nSrvOne})
	seedEvents(st, now, nEventN, views)
	return &standServers{views: views}
}
