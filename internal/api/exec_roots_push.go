// EXEC-корни сессий (8.1 ТЗ): координатор → узел (KindExecRoots).
//
// Правило «кодер вне очереди»: кодер управляем, если он потомок EXEC-корня
// (PID) И время старта корневого процесса совпадает с сохранённым (защита от
// переиспользования PID). Корни хранятся в БД (exec_pid + exec_starttime);
// узлу присылается полный набор живых корней (broadcast): узел сверяет
// /proc/<pid> локально — PIDs с других узлов не совпадают по времени старта и
// игнорируются.
//
// Триггеры: событие изменения (регистрация/завершение exec) + тик планировщика
// + переподключение узла. Кэш last = канонический отпечаток набора: неизменив-
// шийся набор повторно не отправляется (по модели пуша статусов, set_status).
//
// Транспорт: fire-and-forget (Hub.PushToHost, без ожидания reply); reply узла
// отбрасывается координатором (пустой CmdID).

package api

import (
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"

	"runpilot/internal/proto"
	"runpilot/internal/store"
)

// ExecRootsSource — источник живых EXEC-корней. Продакшн — store; тесты —
// фэйк.
type ExecRootsSource interface {
	LiveExecRoots() ([]store.ExecRootRecord, error)
}

// ExecRootsPusher — шлёт узлу живые EXEC-корни ЕГО хоста, только при изменении
// (кэш lastByHost). Корень пушится только узлу, на котором запущен exec (host
// из записи сессии) — не broadcast: узел сверяет /proc/<pid> локально, и только
// он вправе сообщить о смерти корня (PID исчез/время старта не совпало).
// PushAll — событие изменения / тик; ResendHost — переподключение узла.
type ExecRootsPusher struct {
	hub *Hub
	src ExecRootsSource
	log *slog.Logger

	mu         sync.Mutex
	lastByHost map[string]string // канон. отпечаток на хост
}

// NewExecRootsPusher создаёт пушер. log nil → slog.Default().
func NewExecRootsPusher(hub *Hub, src ExecRootsSource, log *slog.Logger) *ExecRootsPusher {
	if log == nil {
		log = slog.Default()
	}
	return &ExecRootsPusher{hub: hub, src: src, log: log, lastByHost: map[string]string{}}
}

// rootsByHost — живые корни, сгруппированные по хосту узла (внутри хоста
// отсортировано по PID). Хост — из записи сессии (имя узла, где запущен exec).
func (p *ExecRootsPusher) rootsByHost() (map[string][]proto.ExecRoot, error) {
	recs, err := p.src.LiveExecRoots()
	if err != nil {
		return nil, err
	}
	out := map[string][]proto.ExecRoot{}
	for _, r := range recs {
		out[r.Host] = append(out[r.Host], proto.ExecRoot{PID: r.PID, Starttime: r.Starttime, SID: r.SID, PGID: r.PGID})
	}
	for _, roots := range out {
		sort.Slice(roots, func(i, j int) bool { return roots[i].PID < roots[j].PID })
	}
	return out, nil
}

// canon — канонический отпечаток набора (детерминирован: PID-отсортирован).
func execRootsCanon(roots []proto.ExecRoot) string {
	var b strings.Builder
	for _, r := range roots {
		b.WriteString(r.SID)
		b.WriteByte(':')
		b.WriteString(strconv.Itoa(r.PID))
		b.WriteByte(':')
		b.WriteString(strconv.FormatInt(r.Starttime, decimalBase))
		b.WriteByte(':')
		b.WriteString(strconv.Itoa(r.PGID))
		b.WriteByte(';')
	}
	return b.String()
}

// PushAll — для каждого хоста: набор изменился (или корни хоста исчезли) →
// пуш только этому узлу. Хост без корней получает пустой набор (сброс).
func (p *ExecRootsPusher) PushAll() {
	byHost, err := p.rootsByHost()
	if err != nil {
		p.log.Warn("api: execroots: load: " + err.Error())
		return
	}
	p.mu.Lock()
	changed := map[string][]proto.ExecRoot{}
	for host, roots := range byHost {
		c := execRootsCanon(roots)
		if p.lastByHost[host] == c {
			continue
		}
		p.lastByHost[host] = c
		changed[host] = roots
	}
	// Хост, где корни исчезли (финиш/смерть) — сбросим его map узлу.
	for host := range p.lastByHost {
		if _, ok := byHost[host]; !ok {
			delete(p.lastByHost, host)
			changed[host] = nil
		}
	}
	p.mu.Unlock()
	for host, roots := range changed {
		p.send(host, roots)
	}
}

// ResendHost — переподключение узла: отправить ему текущий набор его хоста.
func (p *ExecRootsPusher) ResendHost(host string) {
	byHost, err := p.rootsByHost()
	if err != nil {
		p.log.Warn("api: execroots: resend: " + err.Error())
		return
	}
	roots, ok := byHost[host]
	if !ok {
		roots = nil
	}
	p.mu.Lock()
	p.lastByHost[host] = execRootsCanon(roots)
	p.mu.Unlock()
	p.send(host, roots)
}

// send — fire-and-forget KindExecRoots узлу.
func (p *ExecRootsPusher) send(nodeID string, roots []proto.ExecRoot) {
	m := proto.New(proto.KindExecRoots)
	m.ExecRoots = roots
	if err := p.hub.PushToNode(nodeID, m); err != nil {
		p.log.Warn("api: execroots: push: " + err.Error(), "node", nodeID)
	}
}
