package store

import (
	"encoding/json"
	"testing"
	"time"

	"runpilot/internal/config"

	"gopkg.in/yaml.v3"
)

// yamlMarshalExport — YAML экспорта прямо из конфига (для тестов импорта).
func yamlMarshalExport(cfg *config.Config) ([]byte, error) {
	snap, err := cfg.WorkingSnapshot()
	if err != nil {
		return nil, err
	}
	m := make(map[string]any, len(snap))
	for p, v := range snap {
		var x any
		if err := json.Unmarshal(v, &x); err != nil {
			return nil, err
		}
		m[p] = x
	}
	return yaml.Marshal(SettingsExport{Settings: m, Servers: cfg.Servers})
}

// snapEqual — равенство рабочих снимков (путь→значение, побайтово).
func snapEqual(a, b map[string]json.RawMessage) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		bv, ok := b[k]
		if !ok || string(v) != string(bv) {
			return false
		}
	}
	return true
}

func validServer() config.Server {
	return config.Server{
		Name: "srv1", Priority: 10, Slots: 1,
		Accept: []string{"resume", "high", "normal", "low"},
		HealthURL:       "http://192.0.2.1:8004/health",
		MetricsURL:      "http://192.0.2.1:8004/metrics",
		MaxOutputTokens: 16384, FirstByteTimeoutSec: 900,
		Upstreams: config.Upstreams{OpenAI: config.UpstreamOpenAI{
			URL: "http://192.0.2.1:8004", Model: "m1", KeyEnv: "RUNPILOT_KEY",
		}},
	}
}

// Сохранение и загрузка рабочих настроек: загрузка в cfg даёт те же значения.
func TestSaveAndLoadWorkingSettings(t *testing.T) {
	s, _ := openTest(t)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	cfg := config.Defaults()
	cfg.Scheduler.AgingSec = 777
	cfg.Turn.KillGraceSec = 42
	snap, err := cfg.WorkingSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveWorkingSettings(snap, "op", "web", now); err != nil {
		t.Fatal(err)
	}

	got := config.Defaults()
	if err := s.LoadWorkingInto(got); err != nil {
		t.Fatal(err)
	}
	if got.Scheduler.AgingSec != 777 || got.Turn.KillGraceSec != 42 {
		t.Fatalf("загрузка: aging=%d kill=%d", got.Scheduler.AgingSec, got.Turn.KillGraceSec)
	}
	// Полное совхождение снимков.
	cur, err := s.LoadWorkingSettings()
	if err != nil {
		t.Fatal(err)
	}
	if !snapEqual(cur, snap) {
		t.Fatal("загруженные настройки != сохранённым снимку")
	}
}

// CONTROL 4: откат к ревизии даёт действующие настройки, равные исходным
// (пустой diff).
func TestRevertYieldsOriginal(t *testing.T) {
	s, _ := openTest(t)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// Исходное состояние A.
	a := config.Defaults()
	a.Scheduler.AgingSec = 111
	snapA, _ := a.WorkingSnapshot()
	idA, err := s.SaveWorkingSettings(snapA, "op1", "web", now)
	if err != nil {
		t.Fatal(err)
	}
	// Изменение до B.
	b := config.Defaults()
	b.Scheduler.AgingSec = 222
	snapB, _ := b.WorkingSnapshot()
	if _, err := s.SaveWorkingSettings(snapB, "op2", "web", now); err != nil {
		t.Fatal(err)
	}
	// Б подтверждено: текущее != A.
	cur, _ := s.LoadWorkingSettings()
	if snapEqual(cur, snapA) {
		t.Fatal("после сохранения B текущее уже равно A — тест некорректен")
	}
	// Откат к ревизии A.
	if _, err := s.RevertToRevision(idA, "op3", now); err != nil {
		t.Fatal(err)
	}
	cur, _ = s.LoadWorkingSettings()
	if !snapEqual(cur, snapA) {
		t.Fatal("откат не вернул исходные настройки A (diff не пуст)")
	}
}

// CONTROL 5: экспорт → импорт → пустой diff.
func TestExportImportEmptyDiff(t *testing.T) {
	s, _ := openTest(t)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	cfg := config.Defaults()
	cfg.Scheduler.AgingSec = 500
	cfg.Servers = []config.Server{validServer()}
	snap, _ := cfg.WorkingSnapshot()
	if _, err := s.SaveWorkingSettings(snap, "op", "migration", now); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceServers(cfg.Servers, now); err != nil {
		t.Fatal(err)
	}

	exp, err := s.ExportSettings()
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	// dry_run: diff пуст (экспорт соответствует текущему состоянию).
	res, err := s.ImportSettings(exp, true, "op", now)
	if err != nil {
		t.Fatalf("import dry_run: %v", err)
	}
	if len(res.Diff) != 0 {
		t.Fatalf("dry_run diff не пуст: %d изменений", len(res.Diff))
	}
	// apply: diff пуст, состояние не изменилось.
	res2, err := s.ImportSettings(exp, false, "op", now)
	if err != nil {
		t.Fatalf("import apply: %v", err)
	}
	if !res2.Applied {
		t.Fatal("import не применён")
	}
	if len(res2.Diff) != 0 {
		t.Fatalf("apply diff не пуст: %d изменений", len(res2.Diff))
	}
	// Серверы сохранились.
	servers, _ := s.ListServers()
	if len(servers) != 1 || servers[0].Name != "srv1" {
		t.Fatalf("серверы после импорта: %+v", servers)
	}
}

// Невалидный импорт отклоняется (раздел 4.5): сервер с slots=0.
func TestImportInvalidRejected(t *testing.T) {
	s, _ := openTest(t)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	cfg := config.Defaults()
	cfg.Servers = []config.Server{{
		Name: "bad", Slots: 0, Accept: []string{"high"},
		Upstreams: config.Upstreams{OpenAI: config.UpstreamOpenAI{
			URL: "http://192.0.2.1:8004", Model: "m", KeyEnv: "K",
		}},
	}}
	// Экспортируем невалидное состояние вручную.
	data, err := yamlMarshalExport(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ImportSettings(data, false, "op", now); err == nil {
		t.Fatal("невалидный импорт (slots=0) должен быть отклонён")
	}
}

// Ревизии создаются в порядке и несут источник.
func TestRevisionsOrdered(t *testing.T) {
	s, _ := openTest(t)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	a := config.Defaults()
	a.Scheduler.AgingSec = 1
	sa, _ := a.WorkingSnapshot()
	if _, err := s.SaveWorkingSettings(sa, "op", "migration", now); err != nil {
		t.Fatal(err)
	}
	b := config.Defaults()
	b.Scheduler.AgingSec = 2
	sb, _ := b.WorkingSnapshot()
	if _, err := s.SaveWorkingSettings(sb, "op", "web", now); err != nil {
		t.Fatal(err)
	}
	revs, err := s.ListRevisions(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(revs) != 2 {
		t.Fatalf("ревизий = %d, ждём 2", len(revs))
	}
	// Новые сверху: revs[0] — web, revs[1] — migration.
	if revs[0].Source != "web" || revs[1].Source != "migration" {
		t.Fatalf("порядок/источники: %s, %s", revs[0].Source, revs[1].Source)
	}
	if revs[0].ID < revs[1].ID {
		t.Fatal("id ревизий не по возрастанию")
	}
}
