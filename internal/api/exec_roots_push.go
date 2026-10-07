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

// ExecRootsPusher — шлёт узлам полный набор живых EXEC-корней, только при
// изменении (кэш last). PushAll — событие изменения / тик; ResendHost —
// переподключение узла.
type ExecRootsPusher struct {
	hub *Hub
	src ExecRootsSource
	log *slog.Logger

	mu   sync.Mutex
	last string // канон. отпечаток последнего отправленного набора
}

// NewExecRootsPusher создаёт пушер. log nil → slog.Default().
func NewExecRootsPusher(hub *Hub, src ExecRootsSource, log *slog.Logger) *ExecRootsPusher {
	if log == nil {
		log = slog.Default()
	}
	return &ExecRootsPusher{hub: hub, src: src, log: log}
}

// rootsNow — полный набор живых корней (отсортирован по PID).
func (p *ExecRootsPusher) rootsNow() ([]proto.ExecRoot, error) {
	recs, err := p.src.LiveExecRoots()
	if err != nil {
		return nil, err
	}
	out := make([]proto.ExecRoot, 0, len(recs))
	for _, r := range recs {
		out = append(out, proto.ExecRoot{PID: r.PID, Starttime: r.Starttime, SID: r.SID})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PID < out[j].PID })
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
		b.WriteByte(';')
	}
	return b.String()
}

// PushAll — пересчитать набор и разослать всем узлам, только при изменении.
func (p *ExecRootsPusher) PushAll() {
	roots, err := p.rootsNow()
	if err != nil {
		p.log.Warn("api: execroots: load: " + err.Error())
		return
	}
	c := execRootsCanon(roots)
	p.mu.Lock()
	changed := p.last != c
	p.last = c
	p.mu.Unlock()
	if !changed {
		return
	}
	for _, n := range p.hub.Nodes() {
		p.send(n.Host, roots)
	}
}

// ResendHost — переподключение узла: отправить ему текущий набор (кэш глобальный
// и не сбрасывается — узел просто получает актуальный).
func (p *ExecRootsPusher) ResendHost(host string) {
	roots, err := p.rootsNow()
	if err != nil {
		p.log.Warn("api: execroots: resend: " + err.Error())
		return
	}
	p.send(host, roots)
}

// send — fire-and-forget KindExecRoots узлу.
func (p *ExecRootsPusher) send(host string, roots []proto.ExecRoot) {
	m := proto.New(proto.KindExecRoots)
	m.ExecRoots = roots
	if err := p.hub.PushToHost(host, m); err != nil {
		p.log.Warn("api: execroots: push: " + err.Error(), "host", host)
	}
}
