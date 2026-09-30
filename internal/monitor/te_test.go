package monitor

// TE-<ID> — по одному тесту на каждую строку раздела 7 ТЗ (CONTROL W7).
// Группа S (серверы): обнаружение и базовая реакция. Детальные переходы
// (DETACHED/RESUME/миграция) — в scheduler/gateway; здесь — реакция узла
// обнаружения монитора, на которую опирается вся строка.

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"

	"runpilot/internal/clock"
	"runpilot/internal/config"
)

// TestTE_S1 — сервер перестал отвечать: /health не проходит health_down_after
// раз подряд → DOWN.
func TestTE_S1(t *testing.T) {
	h := newMHarn(t, 1)
	h.cfg.Monitor.HealthIntervalSec = 5
	h.fetch.metricsBody = metricsBody(0, 0)
	h.fetch.healthStatus = 503
	n := h.cfg.Monitor.HealthDownAfter
	for i := 0; i < n; i++ {
		h.advance(5 * time.Second)
		h.mon.Tick()
	}
	if got := h.sink.last(); got != "s=DOWN" {
		t.Fatalf("после %d health-неудач: переходы=%v, хочу s=DOWN в конце", n, h.sink.changes)
	}
}

// TestTE_S3 — сервер нестабилен: ≥ faults.flap_count переходов UP↔DOWN за
// faults.flap_window_sec → карантин на faults.quarantine_sec.
func TestTE_S3(t *testing.T) {
	h := newMHarn(t, 1)
	h.cfg.Monitor.HealthIntervalSec = 5
	h.cfg.Monitor.HealthDownAfter = 1
	h.cfg.Monitor.HealthUpAfter = 1
	h.cfg.Faults.FlapCount = 3
	h.cfg.Faults.FlapWindowSec = 600
	h.cfg.Faults.QuarantineSec = 300
	h.fetch.metricsBody = metricsBody(0, 0)
	h.fetch.healthStatus = 503
	h.advance(5 * time.Second)
	h.mon.Tick() // DOWN (переход 1)
	h.fetch.healthStatus = 200
	h.advance(5 * time.Second)
	h.mon.Tick() // UP (переход 2)
	h.fetch.healthStatus = 503
	h.advance(5 * time.Second)
	h.mon.Tick() // DOWN (переход 3) → карантин
	if got := h.sink.last(); got != "s=QUARANTINED" {
		t.Fatalf("после 3 переходов UP↔DOWN: %v, хочу s=QUARANTINED", h.sink.changes)
	}
}

// TestTE_S2 — сервер вернулся: /health проходит health_up_after раз → UP.
func TestTE_S2(t *testing.T) {
	h := newMHarn(t, 1)
	h.cfg.Monitor.HealthIntervalSec = 5
	h.fetch.metricsBody = metricsBody(0, 0)
	// сначала DOWN, затем возврат.
	h.fetch.healthStatus = 503
	for i := 0; i < h.cfg.Monitor.HealthDownAfter; i++ {
		h.advance(5 * time.Second)
		h.mon.Tick()
	}
	if h.sink.last() != "s=DOWN" {
		t.Fatalf("предпосылка: %v", h.sink.changes)
	}
	h.fetch.healthStatus = 200
	for i := 0; i < h.cfg.Monitor.HealthUpAfter; i++ {
		h.advance(5 * time.Second)
		h.mon.Tick()
	}
	if got := h.sink.last(); got != "s=UP" {
		t.Fatalf("после %d health-успехов: переходы=%v, хочу s=UP", h.cfg.Monitor.HealthUpAfter, h.sink.changes)
	}
}

// TestTE_S9 — метрики недоступны → metrics_missing=true, поля «—»
// (Running/KV/GenTokS/Ext отсутствуют — не молчаливое 0).
func TestTE_S9(t *testing.T) {
	h := newMHarn(t, 1)
	h.fetch.metricsBody = ""
	// metrics не отвечает: нет нужных имён.
	h.fetch.metricsStatus = 500
	h.mon.Tick()
	s := h.mon.Sample("s")
	if !s.Missing {
		t.Fatal("metrics_missing=false, хочу true (/metrics не отвечает)")
	}
	if s.Running != nil || s.KV != nil || s.GenTokS != nil || s.Ext != nil {
		t.Fatalf("при metrics_missing поля должны быть «—» (nil): %+v", s)
	}
}

// TestTE_S10 — внешняя нагрузка: ext > 0 держится external_confirm_sec →
// слоты EXTERNAL (новые выдачи не выдаются, reason=external).
func TestTE_S10(t *testing.T) {
	h := newMHarn(t, 1)
	confirm := time.Duration(h.cfg.Scheduler.ExternalConfirmSec) * time.Second
	h.fetch.metricsBody = metricsBody(1, 0) // ext = 1 − 0 = 1 (все 1 слот)
	h.mon.Tick()
	h.addSession("E1")
	h.idlePane("E1")
	h.enqueue("E1")
	h.advance(confirm)
	h.tick()
	h.advance(2 * time.Second) // стабильность панели
	h.tick()
	if h.leased("E1") {
		t.Fatal("при ext=1 (все слоты) запись выдана, а должна ждать (EXTERNAL)")
	}
	if got := h.reason("E1"); got == "" {
		t.Fatalf("reason пуст, хочу external (EXTERNAL-резерв)")
	}
}

// TestTE_K4 — мало места на диске БД: проверка (monitor.disk_check_sec)
// видит свободное < monitor.disk_free_min_mb → [WARN] «Свободно N МБ»;
// достаточно места — предупреждения нет. Проверка идёт раз в интервал.
func TestTE_K4(t *testing.T) {
	cfg := config.Defaults()
	cfg.Servers = nil // только диск-проверка, без серверов
	cfg.Monitor.DiskFreeMinMB = 1024
	cfg.Monitor.DiskCheckSec = 300

	clk := clock.NewVirtual(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	mon := New(cfg, clk, nil, nil, nil, nil, log)
	mon.SetDiskPath("/tmp/runpilot.db")

	// Мало места (512 < 1024) → [WARN] с фактическим размером.
	mon.SetDiskFreeSpace(func(string) (int64, error) { return 512, nil })
	free, warned := mon.CheckDisk()
	if free != 512 || !warned {
		t.Fatalf("CheckDisk: free=%d warned=%v, хочу free=512 warned=true", free, warned)
	}
	if !strings.Contains(buf.String(), "[WARN]") || !strings.Contains(buf.String(), "512") {
		t.Fatalf("нет [WARN] с размером в логе: %q", buf.String())
	}

	// Достаточно места (2048 > 1024) → предупреждения нет.
	buf.Reset()
	mon.SetDiskFreeSpace(func(string) (int64, error) { return 2048, nil })
	if _, warned := mon.CheckDisk(); warned {
		t.Fatal("ложное [WARN] при достаточно свободном месте")
	}
	if strings.Contains(buf.String(), "[WARN]") {
		t.Fatalf("неожиданный [WARN]: %q", buf.String())
	}

	// Периодичность: проверка раз в disk_check_sec. Первый Tick — сразу
	// (после SetDiskPath), второй сразу — не проверяет; после интервала — снова.
	buf.Reset()
	mon.SetDiskFreeSpace(func(string) (int64, error) { return 100, nil })
	mon.Tick() // due (diskNext = now)
	n1 := strings.Count(buf.String(), "[WARN]")
	mon.Tick() // раньше diskNext — не проверяет
	if n2 := strings.Count(buf.String(), "[WARN]"); n2 != n1 {
		t.Fatalf("в пределах интервала проверка повторилась: %d → %d", n1, n2)
	}
	clk.Advance(300 * time.Second)
	mon.Tick() // интервал истёк — проверка снова
	if n3 := strings.Count(buf.String(), "[WARN]"); n3 != n1+1 {
		t.Fatalf("после интервала нет повторной проверки: %d → %d (хочу %d)", n1, n3, n1+1)
	}
}
