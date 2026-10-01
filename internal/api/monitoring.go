package api

import (
	"net/http"
	"sort"
	"strconv"
	"time"

	"runpilot/internal/scheduler"
)

// Мониторинг (v2 раздел 11.9): живые графики за monitor.history_window_min
// по серверам (генерация ток/с, KV%, GPU, EXT) + за 24 часа из таблицы
// ходов (исходы, средняя длительность) + p95 задержки диспетчеризации и TTFB
// шлюза. Данные — из монитора, планировщика и хранилища (R3).

// monCurrent — текущие метрики сервера (nil/пусто = «—»).
type monCurrent struct {
	GPU     *int     `json:"gpu,omitempty"`
	KV      *float64 `json:"kv_pct,omitempty"`
	GEN     *float64 `json:"gen_tok_s,omitempty"`
	EXT     *int     `json:"ext,omitempty"`
	Missing bool     `json:"missing"`
}

// monPoint — точка истории (спарклайн; шаг = интервал метрик).
type monPoint struct {
	TS      string   `json:"ts"`
	Running *int     `json:"running"`
	Waiting *int     `json:"waiting"`
	KV      *float64 `json:"kv"`
	GEN     *float64 `json:"gen_tok_s"`
	EXT     *int     `json:"ext"`
}

// monServer — сервер: состояние, текущие метрики, история.
type monServer struct {
	Name    string    `json:"name"`
	State   string    `json:"state"`
	Current monCurrent `json:"current"`
	History []monPoint `json:"history"`
}

// monTurns24H — ходы за 24 часа (исходы, средняя длительность).
type monTurns24H struct {
	Turns          int     `json:"turns"`
	OK             int     `json:"ok"`
	Error          int     `json:"error"`
	Cancelled      int     `json:"cancelled"`
	Lost           int     `json:"lost"`
	AvgDurationSec float64 `json:"avg_duration_sec"`
}

// monLatency — p95 задержек (мс).
type monLatency struct {
	DispatchP95MS  int64 `json:"dispatch_p95_ms"`
	GatewayTTFBP95 int64 `json:"gateway_ttfb_p95_ms"`
}

// monitoringResponse — ответ GET /api/v1/monitoring.
type monitoringResponse struct {
	Servers []monServer  `json:"servers"`
	Turns   monTurns24H  `json:"turns_24h"`
	Latency monLatency   `json:"latency"`
	Window  string       `json:"window_min"`
}

// handleMonitoring — GET /api/v1/monitoring (раздел 11.9).
func (s *Server) handleMonitoring(w http.ResponseWriter, r *http.Request) {
	resp := monitoringResponse{Servers: []monServer{}}

	// Текущие метрики и состояние серверов — из вида планировщика (раздел 11.6).
	serverRows := []scheduler.ServerRow{}
	if s.sched != nil {
		serverRows = s.sched.ServersView()
	}
	for _, sr := range serverRows {
		m := monServer{Name: sr.Name, State: sr.State, Current: monCurrent{
			GPU: sr.GPU, KV: sr.KV, GEN: sr.GEN,
			EXT: sr.EXT, Missing: sr.Missing,
		}}
		// История — из монитора (окно monitor.history_window_min).
		if s.mon != nil {
			for _, p := range s.mon.HistoryPoints(sr.Name) {
				m.History = append(m.History, monPoint{
					TS:      p.TS.UTC().Format(time.RFC3339),
					Running: ptrInt(p.Running),
					Waiting: ptrInt(p.Waiting),
					KV:      p.KV,
					GEN:     p.GenTokS,
					EXT:     ptrInt(p.Ext),
				})
			}
		}
		resp.Servers = append(resp.Servers, m)
	}

	// Ходы за 24 часа + p95 задержек.
	if s.sched != nil {
		if res, err := s.sched.Stats(s.clk.Now().Add(-statsDefaultHours * time.Hour)); err == nil {
			resp.Turns = monTurns24H{
				Turns: res.Summary.Turns, OK: res.Summary.OK, Error: res.Summary.Error,
				Cancelled: res.Summary.Cancelled, Lost: res.Summary.Lost,
				AvgDurationSec: res.Summary.AvgDurationSec,
			}
		}
		resp.Latency.DispatchP95MS = s.dispatchP95MS()
	}
	if s.store != nil {
		if p95, err := s.store.RequestTTFBP95MS(s.clk.Now().Add(-statsDefaultHours * time.Hour)); err == nil {
			resp.Latency.GatewayTTFBP95 = p95
		}
	}
	if s.cfg.Monitor.HistoryWindowMin > 0 {
		resp.Window = strconv.Itoa(s.cfg.Monitor.HistoryWindowMin)
	}
	writeJSON(w, resp)
}

// ptrInt — *int из int (история: 0 = значение, отличает отсутствие).
func ptrInt(v int) *int { return &v }

// dispatchP95MS — p95 задержки диспетчеризации (раздел 11.9).
func (s *Server) dispatchP95MS() int64 {
	if s.sched == nil {
		return 0
	}
	samples := s.sched.DispatchLatency()
	if len(samples) == 0 {
		return 0
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	idx := len(samples) * p95Percent / percentWhole
	if idx >= len(samples) {
		idx = len(samples) - 1
	}
	return samples[idx]
}
