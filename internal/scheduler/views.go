package scheduler

import (
	"context"
	"fmt"
	"sort"
	"time"

	"runpilot/internal/model"
	"runpilot/internal/proto"
)

// QueueRow — строка очереди для TUI/CLI (адресация, ранг, причина пропуска).
type QueueRow struct {
	Rank     int    `json:"rank"`
	Mode     string `json:"mode"`
	Class    string `json:"class"`
	Session  string `json:"session"`
	Host     string `json:"host"`
	SID      string `json:"sid"`
	Why      string `json:"why,omitempty"`
	WaitSec  int    `json:"wait_sec"`
	Attempts int    `json:"attempts"`
	Prompt   string `json:"prompt,omitempty"` // первые 80 символов ввода (раздел 7 ТЗ)
	Actions  []model.Action `json:"actions"`  // v2 15.2
}

// ServerSlotRow — слот сервера (занят/свободен/cool-down).
type ServerSlotRow struct {
	Slot      int    `json:"slot"`
	State     string `json:"state"`
	Session   string `json:"session,omitempty"`
	TurnSince string `json:"turn_since,omitempty"`
}

// ServerRow — сервер для TUI/CLI (раздел 11 ТЗ). Метрики Э6:
// nil/пусто = «—» (метрики отсутствуют / GPU не подключён).
type ServerRow struct {
	Name     string          `json:"name"`
	Priority int             `json:"priority"`
	State    string          `json:"state"`
	Slots    int             `json:"slots"`
	Used     int             `json:"used"`
	SlotInfo []ServerSlotRow `json:"slots_info"`

	// Метрики (раздел 10 ТЗ).
	GPU   *int     `json:"gpu,omitempty"`    // утилизация, max по картам, %
	KV    *float64 `json:"kv_pct,omitempty"` // kv_cache_usage_perc, %
	GEN   *float64 `json:"gen_tok_s,omitempty"`
	EXT   *int     `json:"ext,omitempty"` // внешняя нагрузка; отсутствует = metrics_missing
	Missing bool   `json:"metrics_missing,omitempty"`

	// W6 (5.2): сервер помечен на удаление (after_turns) — новые ходы не
	// выдаются; UI показывает «Удаляется» до завершения (J6).
	Removing bool `json:"removing,omitempty"`

	GPUCards []proto.GPUCard `json:"gpu_cards,omitempty"`
	Actions  []model.Action  `json:"actions"` // v2 15.2
}

// QueueView — очередь с рангом и причиной пропуска (колонка WHY, раздел 11 ТЗ).
// Ранг — как в выдаче (eff-класс, enqueued_at).
func (s *Scheduler) QueueView() []QueueRow {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clk.Now()
	entries, err := s.st.QueueList()
	if err != nil {
		return nil
	}
	sessions := s.sessionsMap()
	// Сортировка по рангу (eff, enqueued_at) — как тик выдачи.
	sort.SliceStable(entries, func(i, j int) bool {
		return s.rankLess(entries[i], entries[j], now)
	})
	out := make([]QueueRow, 0, len(entries))
	for i, e := range entries {
		sess, ok := sessions[e.SID]
		if !ok {
			continue
		}
		row := QueueRow{
			Rank: i + 1, Mode: string(e.Mode), Class: e.Class.String(),
			Session: sess.Name, Host: sess.Host, SID: e.SID,
			WaitSec:  int(now.Sub(e.EnqueuedAt).Seconds()), Attempts: sess.Attempts,
			Actions:  model.QueueActions(),
		}
		if rec, ok := s.why[e.SID]; ok && now.Sub(rec.ts) < s.whyFresh() {
			row.Why = rec.reason
		} else if e.IneligibleReason != "" && !e.IneligibleSince.IsZero() {
			row.Why = e.IneligibleReason
		}
		out = append(out, row)
	}
	return out
}

// ServersView — серверы с состоянием и слотами (раздел 11 ТЗ).
func (s *Scheduler) ServersView() []ServerRow {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clk.Now()
	servers := s.src.List()
	leases, _ := s.st.LeaseListActive()
	byServer := map[string]map[int]model.Lease{}
	for _, l := range leases {
		if byServer[l.Server] == nil {
			byServer[l.Server] = map[int]model.Lease{}
		}
		byServer[l.Server][l.Slot] = l
	}
	out := make([]ServerRow, 0, len(servers))
	for _, sv := range servers {
		row := ServerRow{
			Name: sv.Name, Priority: sv.Priority, State: string(sv.State), Slots: sv.Slots,
			GPU: sv.GPUPct,
			KV: sv.KV, GEN: sv.GenTokS, EXT: sv.Ext, Missing: sv.Missing,
			Removing: s.serverRemoving[sv.Name],
			GPUCards: sv.GPUCards,
			Actions:  model.ServerActions(sv.State),
		}
		slotInfo := make([]ServerSlotRow, 0, sv.Slots)
		for slot := 1; slot <= sv.Slots; slot++ {
			sr := ServerSlotRow{Slot: slot, State: "free"}
			if l, ok := byServer[sv.Name][slot]; ok {
				sr.State = string(l.State)
				if sess, err := s.st.GetSession(l.SID); err == nil {
					sr.Session = sess.Name
				}
				if !l.GrantedAt.IsZero() {
					sr.TurnSince = l.GrantedAt.UTC().Format(time.RFC3339)
				}
				row.Used++
			} else if until, ok := s.cooldown[sv.Name][slot]; ok && now.Before(until) {
				sr.State = "cooldown"
			}
			slotInfo = append(slotInfo, sr)
		}
		// EXTERNAL (раздел 5 ТЗ) — резерв по числу: первые N свободных
		// слотов (планировщик не привязан к конкретным номерам).
		if n := s.extCount[sv.Name]; n > 0 {
			for i := range slotInfo {
				if n == 0 {
					break
				}
				if slotInfo[i].State == "free" {
					slotInfo[i].State = "external"
					n--
				}
			}
		}
		row.SlotInfo = slotInfo
		out = append(out, row)
	}
	return out
}

// Peek — последние n строк экрана панели (runpilot peek, /peek).
func (s *Scheduler) Peek(target string, n int) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, err := s.resolveSession(target)
	if err != nil {
		return "", err
	}
	pr := s.paneRec[sess.SID]
	if pr == nil || pr.paneID == "" {
		return "", fmt.Errorf("peek: нет панели у %s", target)
	}
	if n <= 0 {
		return "", fmt.Errorf("peek: n должно быть > 0")
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.nodeReplyTimeout())
	defer cancel()
	m := proto.New(proto.KindCapture)
	m.SID, m.PaneID, m.Lines = sess.SID, pr.paneID, n
	reply, err := s.dispatch(ctx, pr.paneID, m)
	if err != nil {
		return "", fmt.Errorf("peek: %w", err)
	}
	if reply.Result == proto.ResPaneGone {
		return "", fmt.Errorf("peek: панель исчезла")
	}
	return reply.Detail, nil
}

// Cancel — прервать ход: отправка cancel-клавиш панели (runpilot cancel).
func (s *Scheduler) Cancel(target string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, err := s.resolveSession(target)
	if err != nil {
		return err
	}
	pr := s.paneRec[sess.SID]
	if pr == nil || pr.paneID == "" {
		return fmt.Errorf("cancel: нет панели у %s", target)
	}
	if len(s.cancelKeys) == 0 {
		return fmt.Errorf("cancel: нет cancel-клавиш в профиле")
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.nodeReplyTimeout())
	defer cancel()
	m := proto.New(proto.KindSendKeys)
	m.SID, m.PaneID, m.Keys = sess.SID, pr.paneID, s.cancelKeys
	reply, err := s.dispatch(ctx, pr.paneID, m)
	if err != nil {
		return fmt.Errorf("cancel: %w", err)
	}
	if reply.Result == proto.ResPaneGone {
		return fmt.Errorf("cancel: панель исчезла")
	}
	s.event(model.KindNotify, sess.SID, "", map[string]any{"action": "cancel"})
	return nil
}

// StatsServer — итоги по серверу (runpilot stats).
type StatsServer struct {
	Server   string  `json:"server"`
	Turns    int     `json:"turns"`
	OK       int     `json:"ok"`
	Err      int     `json:"err"`
	TotalSec int64   `json:"total_sec"`
	AvgSec   float64 `json:"avg_sec"`
}

// StatsSession — итоги по сессии (runpilot stats).
type StatsSession struct {
	Session  string `json:"session"`
	SID      string `json:"sid"`
	Turns    int    `json:"turns"`
	OK       int    `json:"ok"`
	Err      int    `json:"err"`
	TotalSec int64  `json:"total_sec"`
}

// StatsSummary — итоги за период (статистика «за сегодня», раздел 11.2).
type StatsSummary struct {
	Turns          int     `json:"turns"`
	OK             int     `json:"ok"`
	Error          int     `json:"error"`
	Cancelled      int     `json:"cancelled"`
	Lost           int     `json:"lost"`
	TotalSec       int64   `json:"total_sec"`
	AvgDurationSec float64 `json:"avg_duration_sec"`
}

// StatsResult — ходы и длительности по серверам и сессиям (без токенов).
type StatsResult struct {
	Summary   StatsSummary `json:"summary"`
	ByServer  []StatsServer  `json:"by_server"`
	BySession []StatsSession `json:"by_session"`
}

// Stats — ходы за период (runpilot stats, раздел 11 ТЗ) + сводка (11.2).
func (s *Scheduler) Stats(since time.Time) (StatsResult, error) {
	turns, err := s.st.ListTurnsSince(since)
	if err != nil {
		return StatsResult{}, err
	}
	var out StatsResult
	srvAgg := map[string]*StatsServer{}
	sessAgg := map[string]*StatsSession{}
	sessName := map[string]string{}
	for _, t := range turns {
		dur := int64(0)
		if !t.EndedAt.IsZero() && !t.StartedAt.IsZero() {
			dur = int64(t.EndedAt.Sub(t.StartedAt).Seconds())
		}
		ok := t.Outcome == model.TurnOK
		// сводка за период (11.2).
		out.Summary.Turns++
		out.Summary.TotalSec += dur
		switch t.Outcome {
		case model.TurnOK:
			out.Summary.OK++
		case model.TurnError:
			out.Summary.Error++
		case model.TurnCancelled:
			out.Summary.Cancelled++
		case model.TurnLost:
			out.Summary.Lost++
		}
		// по сессии
		ss := sessAgg[t.SID]
		if ss == nil {
			if sess, err := s.st.GetSession(t.SID); err == nil {
				sessName[t.SID] = sess.Name
			}
			ss = &StatsSession{Session: sessName[t.SID], SID: t.SID}
			sessAgg[t.SID] = ss
		}
		ss.Turns++
		if ok {
			ss.OK++
		} else {
			ss.Err++
		}
		ss.TotalSec += dur
		// по серверам (ход мог мигрировать; учитываем каждый)
		for _, srvName := range t.Servers {
			sr := srvAgg[srvName]
			if sr == nil {
				sr = &StatsServer{Server: srvName}
				srvAgg[srvName] = sr
			}
			sr.Turns++
			if ok {
				sr.OK++
			} else {
				sr.Err++
			}
			sr.TotalSec += dur
		}
	}
	if out.Summary.Turns > 0 {
		out.Summary.AvgDurationSec = float64(out.Summary.TotalSec) / float64(out.Summary.Turns)
	}
	out.ByServer = make([]StatsServer, 0, len(srvAgg))
	for _, sr := range srvAgg {
		if sr.Turns > 0 {
			sr.AvgSec = float64(sr.TotalSec) / float64(sr.Turns)
		}
		out.ByServer = append(out.ByServer, *sr)
	}
	sort.Slice(out.ByServer, func(i, j int) bool { return out.ByServer[i].Server < out.ByServer[j].Server })
	out.BySession = make([]StatsSession, 0, len(sessAgg))
	for _, ss := range sessAgg {
		out.BySession = append(out.BySession, *ss)
	}
	sort.Slice(out.BySession, func(i, j int) bool { return out.BySession[i].SID < out.BySession[j].SID })
	return out, nil
}

// SetAutoEnqueue — включить/выключить автопостановку (runpilot set auto-enqueue).
func (s *Scheduler) SetAutoEnqueue(target string, on bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, err := s.resolveSession(target)
	if err != nil {
		return err
	}
	if err := s.st.SetSessionAutoEnqueue(sess.SID, on); err != nil {
		return err
	}
	if !on {
		delete(s.autoSince, sess.SID)
	}
	s.event(model.KindSessionState, sess.SID, "", map[string]any{"auto_enqueue": on})
	return nil
}
