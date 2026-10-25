// Package api — API координатора (раздел 13 ТЗ).
//
// Э2: канал узла (WS proto=1), регистрация сессий (runpilot run) с коллизиями
// раздела 8, снимки панелей с received_at координатора, dispatch с
// измерением задержки. Шлюз и планировщик — Э3/Э4.
package api

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"runpilot/internal/clock"
	"runpilot/internal/model"
	"runpilot/internal/proto"
	"runpilot/internal/store"

	"github.com/gorilla/websocket"
)

// PaneInfo — снимок панели, известный координатору.
// ReceivedAt ставит координатор по часам координатора (строгое правило ТЗ).
type PaneInfo struct {
	Pane       proto.Pane
	Host       string
	ReceivedAt time.Time
}

// Age — возраст снимка по received_at.
func (p *PaneInfo) Age(now time.Time) time.Duration { return now.Sub(p.ReceivedAt) }

// NodeConn — транспорт команды «координатор → узел» (hub.Dispatch):
// WS-соединение (реальный узел) или внутренний канал (встроенный узел,
// v2 раздел 2.5). *websocket.Conn удовлетворяет интерфейсу.
type NodeConn interface {
	WriteJSON(v any) error
	WriteControl(messageType int, data []byte, deadline time.Time) error
}

// lockedNodeConn — WS-соединение узла с мьютексом записи: команды (WriteJSON)
// и кадры PTY (writeBinary) не пишутся параллельно — gorilla/websocket
// допускает одного писателя. WriteControl (ping) безопасен и без мьютекса.
type lockedNodeConn struct {
	*websocket.Conn
	wmu sync.Mutex
}

// WriteJSON реализует NodeConn под мьютексом записи.
func (c *lockedNodeConn) WriteJSON(v any) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return c.Conn.WriteJSON(v)
}

// writeBinary — двоичный кадр PTY (13.4 ТЗ) под тем же мьютексом записи.
func (c *lockedNodeConn) writeBinary(data []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return c.Conn.WriteMessage(websocket.BinaryMessage, data)
}

// NodeInfo — узел, подключённый к координатору.
type NodeInfo struct {
	Host        string
	// OsUser — пользователь ОС узла. Идентичность узла = host:os_user:
	// два узла с одним host и разными os_user сосуществуют.
	OsUser      string
	HostIP      string
	RUNPILOTVersion  string
	TmuxVersion string
	OS          string
	Sockets     []string
	Conn        NodeConn
	// PtySend — доставка нажатий клавиш терминала узлу (двоичный кадр с
	// номером канала, 13.4 ТЗ). WS-узел — бинарный кадр на соединении;
	// встроенный узел — прямой PtyWrite. nil — терминал недоступен.
	// json:"-" — func не сериализуется (SA1026).
	PtySend func(chan_ int, data []byte) error `json:"-"`
}

// ID — идентичность узла: host:os_user (ТЗ «запуск сессий на узле выбранного
// пользователя»). Пустой OsUser (старые узлы) → host.
func (n *NodeInfo) ID() string {
	if n.OsUser == "" {
		return n.Host
	}
	return n.Host + ":" + n.OsUser
}

// GPUInfo — телеметрия GPU от узла (раздел 10 ТЗ): карты сервера
// по последнему сообщению gpu.
type GPUInfo struct {
	Server string
	Host   string
	Cards  []proto.GPUCard
	At     time.Time
}

// NodeHealthView — здоровье узла (N9/N10, 14.4): проблемы, расхождение часов
// с координатором, место на диске, версия qwen — для страницы узла и [WARN].
type NodeHealthView struct {
	Host        string    `json:"host"`
	Problems    []string  `json:"problems,omitempty"`
	TimeSkewMS  int64     `json:"time_skew_ms,omitempty"`
	DiskFreeMB  int       `json:"disk_free_mb,omitempty"`
	QwenVersion string    `json:"qwen_version,omitempty"`
	At          time.Time `json:"at"`
}

// Hub — реестр узлов, снимков панелей, GPU-телеметрии и ожидателей reply.
// Панели ключуются model.PaneKey (узел + tmux pane_id): голый pane_id
// уникален только в пределах tmux-сервера, а у каждого узла свой сервер.
type Hub struct {
	clk clock.Clock

	mu        sync.Mutex
	nodes     map[string]*NodeInfo             // host:os_user → узел
	panes     map[model.PaneKey]*PaneInfo      // ключ панели → снимок
	bySID     map[string]model.PaneKey         // sid → ключ панели
	replies   map[string]chan proto.Msg        // cmd_id → канал ответа
	gpus      map[string]*GPUInfo              // server → последняя телеметрия
	unmanaged map[model.PaneKey]*PaneInfo      // ключ панели → отцепленная панель (узел удалён, J7)
	// unmanagedLive — живые UNMANAGED-панели по узлам (8.3 X1): полный
	// список каждого цикла узла; в отличие от unmanaged (J7) узлы онлайн.
	unmanagedLive map[string][]proto.UnmanagedPane
	healths   map[string]*NodeHealthView // host → node_health (N9/N10)
	cmdSeq    int
}

// NewHub создаёт hub.
func NewHub(clk clock.Clock) *Hub {
	return &Hub{
		clk:             clk,
		nodes:           map[string]*NodeInfo{},
		panes:           map[model.PaneKey]*PaneInfo{},
		bySID:           map[string]model.PaneKey{},
		replies:         map[string]chan proto.Msg{},
		gpus:            map[string]*GPUInfo{},
		unmanaged:       map[model.PaneKey]*PaneInfo{},
		unmanagedLive:   map[string][]proto.UnmanagedPane{},
		healths:         map[string]*NodeHealthView{},
	}
}

// SetNodeHealth — node_health узла (N9/N10): сохраняет снимок здоровья.
func (h *Hub) SetNodeHealth(nodeID string, v NodeHealthView) {
	h.mu.Lock()
	defer h.mu.Unlock()
	v.Host = nodeID
	v.At = h.clk.Now()
	h.healths[nodeID] = &v
}

// NodeHealth — последнее node_health узла.
func (h *Hub) NodeHealth(nodeID string) (NodeHealthView, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	v, ok := h.healths[nodeID]
	if !ok {
		return NodeHealthView{}, false
	}
	cp := *v
	cp.Problems = append([]string(nil), v.Problems...)
	return cp, true
}

// SetGPU — сообщение gpu узла (раздел 10 ТЗ): server → карты.
func (h *Hub) SetGPU(server, host string, cards []proto.GPUCard) {
	if server == "" || len(cards) == 0 {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.gpus[server] = &GPUInfo{Server: server, Host: host, Cards: cards, At: h.clk.Now()}
}

// GPU — последняя телеметрия сервера.
func (h *Hub) GPU(server string) (GPUInfo, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	g, ok := h.gpus[server]
	if !ok {
		return GPUInfo{}, false
	}
	cp := *g
	cp.Cards = append([]proto.GPUCard(nil), g.Cards...)
	return cp, true
}

// NewCmdID — уникальный идентификатор команды.
func (h *Hub) NewCmdID() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cmdSeq++
	return fmt.Sprintf("c%d-%d", time.Now().UnixNano(), h.cmdSeq)
}

// Register — узел прошёл hello. Ключ реестра — идентичность узла (host:os_user).
func (h *Hub) Register(info *NodeInfo) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.nodes[info.ID()] = info
}

// Unregister — узел отключился: его снимки панелей остаются (стареют по
// received_at), узла в реестре больше нет. Живые UNMANAGED-панели
// очищаем (стали неживыми; придут снова при переподключении).
func (h *Hub) Unregister(nodeID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.nodes, nodeID)
	delete(h.unmanagedLive, nodeID)
}

// Registered — узел уже в реестре (N7: конфликт имени хоста).
func (h *Hub) Registered(nodeID string) (NodeInfo, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	n, ok := h.nodes[nodeID]
	if !ok {
		return NodeInfo{}, false
	}
	cp := *n
	return cp, true
}

// HasHost — есть ли в реестре узел с данным host (любой os_user). Для N7:
// конфликт имени хоста (реестр ключируется по host:os_user).
func (h *Hub) HasHost(host string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, n := range h.nodes {
		if n.Host == host {
			return true
		}
	}
	return false
}

// DetachHost — узел удалён: его панели становятся UNMANAGED (кодеры живут в
// tmux, runpilot больше не управляет — J7). SID снимается, снимок сохраняется.
func (h *Hub) DetachHost(nodeID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for id, p := range h.panes {
		if p.Host != nodeID {
			continue
		}
		up := *p
		up.Pane.SID = ""
		up.Pane.State = string(model.PaneUnmanaged)
		h.unmanaged[id] = &up
		delete(h.panes, id)
		for s, k := range h.bySID {
			if k == id {
				delete(h.bySID, s)
			}
		}
	}
	delete(h.unmanagedLive, nodeID)
}

// UnmanagedCount — число отцепленных панелей узла.
func (h *Hub) UnmanagedCount(nodeID string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, p := range h.unmanaged {
		if p.Host == nodeID {
			n++
		}
	}
	return n
}

// UnmanagedOfHost — отцепленные панели узла (для UI).
func (h *Hub) UnmanagedOfHost(nodeID string) []PaneInfo {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []PaneInfo
	for _, p := range h.unmanaged {
		if p.Host == nodeID {
			out = append(out, *p)
		}
	}
	return out
}

// SetPanes — полный список панелей узла (сообщение panes). nodeID —
// идентичность узла (host:os_user), берётся из аутентифицированного соединения.
func (h *Hub) SetPanes(nodeID string, list []proto.Pane) {
	now := h.clk.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, p := range list {
		if p.SID == "" {
			continue
		}
		k := model.PaneKeyOf(nodeID, p.PaneID)
		h.panes[k] = &PaneInfo{Pane: p, Host: nodeID, ReceivedAt: now}
		h.bySID[p.SID] = k
	}
}

// SetPane — один снимок (сообщение pane). nodeID — идентичность узла.
func (h *Hub) SetPane(nodeID string, p proto.Pane) {
	if p.SID == "" {
		return
	}
	now := h.clk.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	k := model.PaneKeyOf(nodeID, p.PaneID)
	h.panes[k] = &PaneInfo{Pane: p, Host: nodeID, ReceivedAt: now}
	h.bySID[p.SID] = k
}

// Pane — снимок панели по ключу.
func (h *Hub) Pane(key model.PaneKey) (*PaneInfo, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	p, ok := h.panes[key]
	return p, ok
}

// PanesOfHost — все снимки панелей узла (diff для PaneGone, Э4). nodeID —
// идентичность узла (host:os_user).
func (h *Hub) PanesOfHost(nodeID string) []PaneInfo {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []PaneInfo
	for _, p := range h.panes {
		if p.Host == nodeID {
			out = append(out, *p)
		}
	}
	return out
}

// DropPanesOfHost — панели узла, исчезнувшие из полного нового списка:
// убрать их снимки, вернуть их sids (PaneGone, Э4). newIDs — голые pane_id
// из полного скана узла.
func (h *Hub) DropPanesOfHost(nodeID string, newIDs map[string]bool) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var sids []string
	for k, p := range h.panes {
		if p.Host != nodeID || newIDs[p.Pane.PaneID] {
			continue
		}
		if p.Pane.SID != "" {
			sids = append(sids, p.Pane.SID)
		}
		delete(h.panes, k)
		for s, pk := range h.bySID {
			if pk == k {
				delete(h.bySID, s)
			}
		}
	}
	return sids
}

// MarkUnmanaged — C10: панели, у которых снят @runpilot_sid. Ранее управляемая
// панель (SID) в новом скане без SID → сессия ведётся в GONE (PaneGone),
// панель — UNMANAGED. Возвращает такие SID.
func (h *Hub) MarkUnmanaged(nodeID string, list []proto.Pane) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	unmanagedNow := map[string]bool{}
	for _, p := range list {
		if p.SID == "" {
			unmanagedNow[p.PaneID] = true
		}
	}
	var sids []string
	for k, p := range h.panes {
		if p.Host != nodeID || !unmanagedNow[p.Pane.PaneID] || p.Pane.SID == "" {
			continue
		}
		sids = append(sids, p.Pane.SID)
		delete(h.panes, k)
		for s, pk := range h.bySID {
			if pk == k {
				delete(h.bySID, s)
			}
		}
	}
	return sids
}

// DropPane — снять снимок панели (pane_gone / удаление узла): вернуть SID,
// если панель была управляемой.
func (h *Hub) DropPane(key model.PaneKey) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var sid string
	if p, ok := h.panes[key]; ok {
		sid = p.Pane.SID
		delete(h.panes, key)
	}
	for s, k := range h.bySID {
		if k == key {
			delete(h.bySID, s)
		}
	}
	return sid
}

// SetUnmanaged — полный список UNMANAGED-панелей узла (8.3 X1): живое
// множество заменяется (пустой список очищает — панель стала управляемой
// или умерла). Ключ — идентичность узла (host:os_user).
func (h *Hub) SetUnmanaged(nodeID string, list []proto.UnmanagedPane) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.unmanagedLive[nodeID] = list
}

// Unmanaged — живые UNMANAGED-панели всех узлов (для /state и карточек
// «Вне runpilot» в вебе).
func (h *Hub) Unmanaged() []UnmanagedView {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []UnmanagedView
	for host, list := range h.unmanagedLive {
		for _, p := range list {
			out = append(out, UnmanagedView{UnmanagedPane: p, Host: host})
		}
	}
	return out
}

// UnmanagedView — UNMANAGED-панель с узлом (для /state).
type UnmanagedView struct {
	proto.UnmanagedPane
	Host string `json:"host"`
}

// AllPanes — все снимки панелей всех узлов (для пуша статусов, п.6c).
func (h *Hub) AllPanes() []PaneInfo {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]PaneInfo, 0, len(h.panes))
	for _, p := range h.panes {
		out = append(out, *p)
	}
	return out
}

// PushPane — fire-and-forget команда панели (через её узел) БЕЗ ожидания
// reply (set_status/set_summary, п.6c: ответ не нужен). Ошибка — узел/панель
// недоступны (вызывающий логгирует; узел переподключится и попросит заново).
func (h *Hub) PushPane(key model.PaneKey, m proto.Msg) error {
	h.mu.Lock()
	info, ok := h.panes[key]
	host := ""
	if ok {
		host = info.Host
	}
	node, okNode := h.nodes[host]
	h.mu.Unlock()
	if !ok || !okNode || node.Conn == nil {
		return fmt.Errorf("hub: панель %s/%s или узел недоступны", key.Node, key.Pane)
	}
	return node.Conn.WriteJSON(m)
}

// PushToNode — fire-and-forget сообщение узлу по идентичности (host:os_user)
// БЕЗ ожидания reply (EXEC-корни, KindExecRoots) — по модели PushPane.
// Ошибка — узел недоступен. Reply узла отбрасывается координатором (пустой CmdID).
func (h *Hub) PushToNode(nodeID string, m proto.Msg) error {
	h.mu.Lock()
	node, ok := h.nodes[nodeID]
	h.mu.Unlock()
	if !ok || node.Conn == nil {
		return fmt.Errorf("hub: узел %s недоступен", nodeID)
	}
	return node.Conn.WriteJSON(m)
}

// PaneBySID — снимок панели сессии.
func (h *Hub) PaneBySID(sid string) (*PaneInfo, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	id, ok := h.bySID[sid]
	if !ok {
		return nil, false
	}
	p, ok := h.panes[id]
	return p, ok
}

// Node — узел по идентичности (host:os_user).
func (h *Hub) Node(nodeID string) (*NodeInfo, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	n, ok := h.nodes[nodeID]
	return n, ok
}

// PtySendToNode — нажатия клавиш терминала узлу (13.4 ТЗ): двоичный кадр с
// номером канала. Ошибка, если узла нет или PtySend не задан.
func (h *Hub) PtySendToNode(nodeID string, chan_ int, data []byte) error {
	h.mu.Lock()
	node, ok := h.nodes[nodeID]
	h.mu.Unlock()
	if !ok || node.PtySend == nil {
		return fmt.Errorf("hub: узел %s: PTY недоступен", nodeID)
	}
	return node.PtySend(chan_, data)
}

// Nodes — список узлов.
func (h *Hub) Nodes() []NodeInfo {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]NodeInfo, 0, len(h.nodes))
	for _, n := range h.nodes {
		cp := *n
		cp.Conn = nil
		cp.PtySend = nil
		out = append(out, cp)
	}
	return out
}

// WaitReply — канал ответа на cmd_id (вызывается ДО отправки команды).
func (h *Hub) WaitReply(cmdID string) <-chan proto.Msg {
	ch := make(chan proto.Msg, 1)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.replies[cmdID] = ch
	return ch
}

// DeliverReply — reply от узла.
func (h *Hub) DeliverReply(m proto.Msg) {
	h.mu.Lock()
	ch := h.replies[m.CmdID]
	delete(h.replies, m.CmdID)
	h.mu.Unlock()
	if ch != nil {
		ch <- m
	}
}

// Dispatch — отправить команду dispatch узлу и дождаться reply
// (таймаут dispatch.node_reply_timeout_sec держит вызывающий). key — ключ
// панели (узел + tmux pane_id); m.PaneID — голый tmux pane_id для узла.
func (h *Hub) Dispatch(ctx context.Context, key model.PaneKey, m proto.Msg) (proto.Msg, error) {
	h.mu.Lock()
	info, ok := h.panes[key]
	host := ""
	if ok {
		host = info.Host
	}
	node, okNode := h.nodes[host]
	h.mu.Unlock()
	if !ok || !okNode || node.Conn == nil {
		return proto.Msg{}, fmt.Errorf("hub: панель %s/%s или узел недоступны", key.Node, key.Pane)
	}
	m.CmdID = h.NewCmdID()
	wait := h.WaitReply(m.CmdID)
	if err := node.Conn.WriteJSON(m); err != nil {
		h.mu.Lock()
		delete(h.replies, m.CmdID)
		h.mu.Unlock()
		return proto.Msg{}, fmt.Errorf("hub: отправка dispatch: %w", err)
	}
	select {
	case r := <-wait:
		return r, nil
	case <-ctx.Done():
		return proto.Msg{}, ctx.Err()
	}
}

// DispatchToNode — команда узлу по идентичности (host:os_user; spawn/
// list_dirs/update/uninstall/config — не зависят от панели, раздел 13.2/14.2 v2).
// Ответ приходит как KindReply и доставляется в WaitReply.
func (h *Hub) DispatchToNode(ctx context.Context, nodeID string, m proto.Msg) (proto.Msg, error) {
	h.mu.Lock()
	node, ok := h.nodes[nodeID]
	h.mu.Unlock()
	if !ok || node.Conn == nil {
		return proto.Msg{}, fmt.Errorf("hub: узел %s недоступен", nodeID)
	}
	m.CmdID = h.NewCmdID()
	wait := h.WaitReply(m.CmdID)
	if err := node.Conn.WriteJSON(m); err != nil {
		h.mu.Lock()
		delete(h.replies, m.CmdID)
		h.mu.Unlock()
		return proto.Msg{}, fmt.Errorf("hub: отправка узлу %s: %w", nodeID, err)
	}
	select {
	case r := <-wait:
		return r, nil
	case <-ctx.Done():
		return proto.Msg{}, ctx.Err()
	}
}

// Sessions — для GET /api/v1/state (Э2: минимальный срез; v2: mode).
type State struct {
	Mode      string                `json:"mode"`
	Sessions  []store.SessionRecord `json:"sessions"`
	Panes     []PaneInfo            `json:"panes"`
	Nodes     []NodeInfo            `json:"nodes"`
	Unmanaged []UnmanagedView       `json:"unmanaged,omitempty"` // 8.3 X1
}

// State — текущее состояние координатора.
func (h *Hub) State(sessions []store.SessionRecord) State {
	h.mu.Lock()
	panes := make([]PaneInfo, 0, len(h.panes))
	for _, p := range h.panes {
		panes = append(panes, *p)
	}
	nodes := make([]NodeInfo, 0, len(h.nodes))
	for _, n := range h.nodes {
		cp := *n
		cp.Conn = nil
		cp.PtySend = nil
		nodes = append(nodes, cp)
	}
	unmanaged := h.unmanagedAll()
	h.mu.Unlock()
	return State{Sessions: sessions, Panes: panes, Nodes: nodes, Unmanaged: unmanaged}
}

// unmanagedAll — живые UNMANAGED-панели всех узлов (вызывать под h.mu).
func (h *Hub) unmanagedAll() []UnmanagedView {
	var out []UnmanagedView
	for host, list := range h.unmanagedLive {
		for _, p := range list {
			out = append(out, UnmanagedView{UnmanagedPane: p, Host: host})
		}
	}
	return out
}

// PaneStates — состояния панелей (для статусных строк, Э4/Э5). Ключ —
// «узел/pane_id».
func (h *Hub) PaneStates() map[string]model.PaneState {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := map[string]model.PaneState{}
	for k, p := range h.panes {
		out[k.Node+"/"+k.Pane] = model.PaneState(p.Pane.State)
	}
	return out
}

// StartPinger — периодические WS ping-кадры всем подключённым узлам
// (раздел 13 ТЗ: узел автоматически отвечает pong). Ошибка записи —
// WARN: само соединение обрывается, read-цикл узла/координатора
// разобьётся и переподключится.
func (h *Hub) StartPinger(ctx context.Context, log *slog.Logger) {
	t := time.NewTicker(nodePingInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			h.pingAll(log)
		}
	}
}

func (h *Hub) pingAll(log *slog.Logger) {
	h.mu.Lock()
	nodes := make([]*NodeInfo, 0, len(h.nodes))
	for _, n := range h.nodes {
		nodes = append(nodes, n)
	}
	h.mu.Unlock()
	deadline := time.Now().Add(pingWriteDeadlineSec * time.Second)
	for _, n := range nodes {
		if err := n.Conn.WriteControl(websocket.PingMessage, nil, deadline); err != nil {
			log.Warn("api: ping: отправка не удалась", "host", n.Host, "err", err)
		}
	}
}
