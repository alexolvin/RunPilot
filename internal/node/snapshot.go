package node

import (
	"strings"
	"sync"
	"time"

	"runpilot/internal/clock"
	"runpilot/internal/detect"
	"runpilot/internal/proto"
)

// paneSnap — состояние снимка панели между циклами.
type paneSnap struct {
	info   PaneInfo
	state  detect.State
	hash   uint64
	input  string
	sentAt time.Time
	nextAt time.Time
}

// period — период следующего снимка для текущего состояния панели.
func period(st detect.State) time.Duration {
	switch st {
	case detect.Busy:
		return periodBusy
	case detect.Unknown:
		return periodUnknown
	default: // Idle, Prompt, WaitUI
		return periodWaiting
	}
}

// snapshotter — снимки управляемых панелей и отправка координатору.
type snapshotter struct {
	clk      clock.Clock
	agentVer string
	// pulseInterval — период пульса НЕизменённой Busy-панели (из конфига,
	// W13 этап 4); обязан быть < snapshot_max_age_sec (инвариант загрузки).
	pulseInterval time.Duration

	mu    sync.Mutex
	panes map[string]*paneSnap // по pane_id
}

func newSnapshotter(clk clock.Clock, agentVer string, pulseInterval time.Duration) *snapshotter {
	return &snapshotter{
		clk:           clk,
		agentVer:      agentVer,
		pulseInterval: pulseInterval,
		panes:         map[string]*paneSnap{},
	}
}

// sync сравнивает результат съёмки с предыдущим и возвращает true,
// если нужно отправить сообщение pane (смена состояния, хеша или ввода).
func (s *snapshotter) sync(info PaneInfo, st detect.State, hash uint64, input string, now time.Time) (proto.Pane, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ps := s.panes[info.PaneID]
	if ps == nil {
		ps = &paneSnap{info: info}
		s.panes[info.PaneID] = ps
	}
	changed := false
	if ps.info.SID != info.SID || ps.info.Session != info.Session ||
		ps.info.Window != info.Window || ps.info.PaneIndex != info.PaneIndex {
		ps.info = info
		changed = true
	}
	if hash != ps.hash {
		// stable_since — время последней смены хеша (раздел 7 ТЗ).
		ps.hash, ps.sentAt = hash, now
		changed = true
	}
	if st != ps.state {
		ps.state = st
		changed = true
	}
	if input != ps.input {
		ps.input = input
		changed = true
	}
	ps.nextAt = now.Add(period(st))
	return paneMsg(info, st, hash, input, ps.sentAt, s.agentVer), changed
}

// pulse — время пульса наступило, хотя состояние не менялось (Busy-панель:
// период из конфига, W13 этап 4).
func (s *snapshotter) pulse(info PaneInfo, now time.Time) (proto.Pane, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ps := s.panes[info.PaneID]
	if ps == nil || now.Sub(ps.sentAt) < s.pulseInterval {
		return proto.Pane{}, false
	}
	ps.sentAt = now
	return paneMsg(info, ps.state, ps.hash, ps.input, ps.sentAt, s.agentVer), true
}

// heartbeat — non-Busy панель (QUEUED/DISPATCHING): снимок каждый scan-цикл,
// хотя состояние не менялось (W13 этап 4) — иначе снимок устаревает
// (snapshot_max_age_sec). Обновляет sentAt и возвращает сообщение.
func (s *snapshotter) heartbeat(info PaneInfo, now time.Time) (proto.Pane, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ps := s.panes[info.PaneID]
	if ps == nil {
		return proto.Pane{}, false
	}
	ps.sentAt = now
	return paneMsg(info, ps.state, ps.hash, ps.input, ps.sentAt, s.agentVer), true
}

// forget удаляет панель из состояния (панель исчезла).
func (s *snapshotter) forget(paneID string) { s.mu.Lock(); defer s.mu.Unlock(); delete(s.panes, paneID) }

// LiveSIDs — SIDs живых управляемых панелей (W15 этап 3: смена пула →
// переписать per-сессии system-settings.json). Чтение под мьютексом (конфиг-
// хэндлер в отдельном горутины от скан-цикла).
func (s *snapshotter) LiveSIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, ps := range s.panes {
		if ps != nil && ps.info.SID != "" {
			out = append(out, ps.info.SID)
		}
	}
	return out
}

// paneMsg — сообщение pane для координатора.
func paneMsg(info PaneInfo, st detect.State, hash uint64, input string, stableSince time.Time, agentVer string) proto.Pane {
	preview := input
	if len([]rune(preview)) > inputPreviewLen {
		preview = string([]rune(preview)[:inputPreviewLen])
	}
	return proto.Pane{
		PaneID:       info.PaneID,
		SID:          info.SID,
		State:        st.String(),
		Hash:         hash,
		StableSince:  proto.StableSince(stableSince),
		InputPreview: preview,
		InputEmpty:   strings.TrimSpace(input) == "",
		AgentVersion: agentVer,
		Session:      info.Session,
		Socket:       info.Socket,
	}
}
