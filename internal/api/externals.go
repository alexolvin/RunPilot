package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"runpilot/internal/model"
	"runpilot/internal/proto"
)

// externalView — JSON-представление внешнего кодера для web/API.
type externalView struct {
	Host      string `json:"host"`
	PID       int    `json:"pid"`
	UID       int    `json:"uid"`
	StartTime int64  `json:"start_time"`
	Source    string `json:"source"`
	Exe       string `json:"exe"`
	Flags     string `json:"flags,omitempty"`
	Target    string `json:"target"`
	Status    string `json:"status"`
	FirstSeen string `json:"first_seen"`
	LastSeen  string `json:"last_seen"`
}

// handleExternals — GET /api/v1/externals: список внешних кодеров + правила.
func (s *Server) handleExternals(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ExternalList()
	if err != nil {
		httpError(w, http.StatusInternalServerError, "STORE", err.Error())
		return
	}
	list = dedupExternals(list)
	views := make([]externalView, 0, len(list))
	for _, p := range list {
		views = append(views, externalView{
			Host: p.Host, PID: p.PID, UID: p.UID, StartTime: p.StartTime,
			Source: p.Source, Exe: p.Exe, Flags: p.Flags, Target: p.Target,
			Status: p.Status, FirstSeen: p.FirstSeen.Format(time.RFC3339),
			LastSeen: p.LastSeen.Format(time.RFC3339),
		})
	}
	rules, _ := s.store.IgnoreList()
	writeJSON(w, map[string]any{"externals": views, "ignore_rules": rules})
}

// dedupExternals — одна строка на (host, pid). Корень дубля: start_time =
// now − etimes «дребезжит» на ±1 с (floor ps etimes), поэтому в БД на один
// PID два ключа (s0/s0+1) в статусах ACTIVE/GONE — в UI дубль. Считаем кодер
// одним по PID и показываем активную запись (иначе самую свежую).
func dedupExternals(list []model.ExternalProcess) []model.ExternalProcess {
	best := map[string]int{} // host\x00pid → индекс в out
	out := make([]model.ExternalProcess, 0, len(list))
	for _, p := range list {
		k := p.Host + "\x00" + strconv.Itoa(p.PID)
		if i, ok := best[k]; ok {
			if externalBetter(p, out[i]) {
				out[i] = p
			}
			continue
		}
		best[k] = len(out)
		out = append(out, p)
	}
	return out
}

// externalBetter — a предпочтительнее b для отображения: ACTIVE важнее
// остальных статусов, при равенстве — более свежий last_seen.
func externalBetter(a, b model.ExternalProcess) bool {
	aActive := a.Status == model.ExtActive
	if aActive != (b.Status == model.ExtActive) {
		return aActive
	}
	return a.LastSeen.After(b.LastSeen)
}

// handleExternalIgnore — POST /api/v1/externals/ignore: правило «Игнорировать».
func (s *Server) handleExternalIgnore(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Host  string `json:"host"`
		Exe   string `json:"exe"`
		Flags string `json:"flags"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Exe == "" {
		httpError(w, http.StatusBadRequest, "BAD_REQUEST", "exe обязателен")
		return
	}
	id, err := s.store.IgnoreAdd(req.Host, req.Exe, req.Flags, s.clk.Now())
	if err != nil {
		httpError(w, http.StatusInternalServerError, "STORE", err.Error())
		return
	}
	writeJSON(w, map[string]any{"id": id})
}

// handleExternalIgnoreDelete — DELETE /api/v1/externals/ignore/{id}.
func (s *Server) handleExternalIgnoreDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		httpError(w, http.StatusBadRequest, "BAD_ID", err.Error())
		return
	}
	if err := s.store.IgnoreDelete(id); err != nil {
		httpError(w, http.StatusInternalServerError, "STORE", err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// handleExternalKill — POST /api/v1/externals/kill: «Завершить». Узел сверяет
// идентичность (UID + start_time) и шлёт TERM; координатор ставит KILLED.
func (s *Server) handleExternalKill(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Host      string `json:"host"`
		PID       int    `json:"pid"`
		StartTime int64  `json:"start_time"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.PID <= 0 || req.Host == "" {
		httpError(w, http.StatusBadRequest, "BAD_REQUEST", "host и pid обязательны")
		return
	}
	var uid int
	if list, err := s.store.ExternalList(); err == nil {
		for _, p := range list {
			if p.Host == req.Host && p.PID == req.PID {
				uid = p.UID
				break
			}
		}
	}
	m := proto.New(proto.KindKillProcess)
	m.PID = req.PID
	m.StartTime = req.StartTime
	m.Signal = "TERM"
	m.KProcUID = uid
	ctx, cancel := context.WithTimeout(r.Context(), nodeCmdTimeout)
	defer cancel()
	rep, err := s.hub.DispatchToNode(ctx, req.Host, m)
	if err != nil {
		httpError(w, http.StatusBadGateway, "NODE_UNAVAILABLE", err.Error())
		return
	}
	switch rep.Result {
	case proto.ResKilled:
		_ = s.store.ExternalSetStatus(req.Host, req.PID, req.StartTime, model.ExtKilled, s.clk.Now())
		writeJSON(w, map[string]any{"result": "KILLED"})
	case proto.ResProcessGone, proto.ResProcessChanged:
		httpError(w, http.StatusConflict, rep.Result, rep.Detail)
	default:
		httpError(w, http.StatusInternalServerError, rep.Result, rep.Detail)
	}
}

// classifyExternalTarget — target внешнего кодера (8.2 ТЗ) по OPENAI_BASE_URL:
// gateway:<sid> (префикс gateway_url + /s/<sid>), server:<name> (host:port
// совпал с upstream сервера), иначе unknown.
func (s *Server) classifyExternalTarget(baseURL string) string {
	if baseURL == "" {
		return model.ExtTargetUnknown
	}
	gw := strings.TrimRight(s.cfg.Coordinator.GatewayURL, "/")
	if strings.HasPrefix(baseURL, gw) {
		rest := baseURL[len(gw):]
		if i := strings.Index(rest, "/s/"); i >= 0 {
			sid := rest[i+len("/s/"):]
			if j := strings.IndexByte(sid, '/'); j >= 0 {
				sid = sid[:j]
			}
			if sid != "" {
				return model.ExtTargetGateway + sid
			}
		}
	}
	if hp := hostPortOf(baseURL); hp != "" {
		for _, srv := range s.cfg.Servers {
			up := srv.Upstreams.OpenAI.URL
			if up != "" && hostPortOf(up) == hp {
				return model.ExtTargetServer + srv.Name
			}
		}
	}
	return model.ExtTargetUnknown
}

// hostPortOf — host:port из URL (для сравнения upstream'ов).
func hostPortOf(u string) string {
	p, err := url.Parse(u)
	if err != nil {
		return ""
	}
	return p.Host
}

// applyExternals — полный список внешних кодеров узла за цикл (8.2 ТЗ):
// классификация target, ignore_rule, upsert, первое наблюдение → WARN +
// EXTERNAL_PROCESS; исчезнувшие → GONE.
func (s *Server) applyExternals(host string, exts []proto.ExternalProc) {
	now := s.clk.Now()
	keep := make(map[string]bool, len(exts))
	for _, e := range exts {
		startTime := now.Unix() - e.StartSec
		key := strconv.Itoa(e.PID) + ":" + strconv.FormatInt(startTime, decimalBase)
		keep[key] = true
		flags := strings.Join(e.Flags, " ")
		target := s.classifyExternalTarget(e.OpenAIBaseURL)
		status := model.ExtActive
		if s.store.IgnoreMatch(host, e.Exe, flags) {
			status = model.ExtIgnored
		}
		p := &model.ExternalProcess{
			Host: host, PID: e.PID, UID: e.UID, StartTime: startTime,
			Source: e.Source, Exe: e.Exe, Flags: flags, Target: target,
			Status: status, FirstSeen: now, LastSeen: now,
		}
		isNew, err := s.store.ExternalUpsert(p)
		if err != nil {
			s.log.Warn("api: external: "+err.Error(), "host", host, "pid", e.PID)
			continue
		}
		if isNew && status == model.ExtActive {
			s.notify(fmt.Sprintf("runpilot: [WARN] внешний кодер вне runpilot: host=%s pid=%d exe=%s source=%s target=%s",
				host, e.PID, e.Exe, e.Source, target))
			_ = s.store.EventRecord(model.Event{
				TS: now, Kind: model.KindExternalProc,
				Payload: mustJSON(map[string]any{
					"host": host, "pid": e.PID, "source": e.Source,
					"target": target, "exe": e.Exe,
				}),
			})
			// C12: target = gateway:<sid> существующей сессии → SID_SHARED.
			if sid := strings.TrimPrefix(target, model.ExtTargetGateway); sid != "" {
				if _, err := s.store.GetSession(sid); err == nil {
					_ = s.store.EventRecord(model.Event{
						TS: now, Kind: model.KindExternalProc,
						Payload: mustJSON(map[string]any{
							"host": host, "pid": e.PID, "source": e.Source,
							"target": target, "subtype": "SID_SHARED", "sid": sid,
						}),
					})
					s.notify(fmt.Sprintf("runpilot: [CRIT] процесс %d на %s использует sid сессии %s (SID_SHARED)",
						e.PID, host, sid))
				}
			}
		}
	}
	if err := s.store.ExternalMarkGone(host, keep, now); err != nil {
		s.log.Warn("api: external: gone: "+err.Error(), "host", host)
	}
}
