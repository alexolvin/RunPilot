package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"runpilot/internal/clock"
	"runpilot/internal/config"

	_ "modernc.org/sqlite"
)

// v1Config — репрезентативный v1-конфиг: bootstrap + 13 рабочих ключей +
// сервер. Используются конкретные значения для проверки переноса.
const v1Config = `coordinator:
  bind: 192.0.2.10
  gateway_port: 8787
  api_port: 8788
  token_env: RUNPILOT_TOKEN
  db_path: /tmp/runpilot/runpilot.db
web:
  bind: 127.0.0.1
  port: 8790
  public_url: http://127.0.0.1:8790
  session_ttl_days: 20
node:
  host: ""
  gpu: none
  scan_interval_sec: 3
notify:
  telegram:
    bot_token_env: RUNPILOT_TG_TOKEN
    chat_ids: [123, 456]
    min_turn_sec: 90
scheduler:
  tick_ms: 500
  aging_sec: 1234
dispatch:
  start_confirm_sec: 25
turn:
  done_quiet_sec: 7
gateway:
  hold_max_sec: 60
monitor:
  health_interval_sec: 6
limits:
  max_queue_length: 512
retention:
  requests_days: 45
profiles:
  qwen:
    model_alias: test-model
servers:
  - name: srv1
    priority: 10
    slots: 1
    accept: [resume, high, normal, low]
    health_url: http://192.0.2.1:8004/health
    metrics_url: http://192.0.2.1:8004/metrics
    max_output_tokens: 16384
    first_byte_timeout_sec: 900
    upstreams:
      openai:
        url: http://192.0.2.1:8004
        model: m1
        key_env: RUNPILOT_KEY
`

// createV1DB — БД v1 (001+002, без 003) с одной сессией.
func createV1DB(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, f := range []string{"migrations/001_init.sql", "migrations/002_web_session.sql"} {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("чтение %s: %v", f, err)
		}
		if _, err := db.Exec(string(data)); err != nil {
			t.Fatalf("применение %s: %v", f, err)
		}
	}
	now := "2026-01-01T00:00:00Z"
	_, err = db.Exec(
		`INSERT INTO session (sid,name,host,host_ip,tmux_session,window,pane_id,
		 profile,state,state_changed_at,created_at)
		 VALUES ('s1','s1','h1','127.0.0.1','s1',0,'p1','qwen','IDLE',?,?)`,
		now, now)
	if err != nil {
		t.Fatalf("вставка сессии: %v", err)
	}
}

func fileExists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	return err == nil
}

// CONTROL 1: миграция на копии реальных БД и конфига: перенесено ключей =
// ключей в исходном YAML, копии созданы, конфиг переписан; повтор — no-op.
func TestMigrateFromYAML(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "runpilot.db")
	cfgPath := filepath.Join(dir, "config.yaml")
	createV1DB(t, dbPath)
	if err := os.WriteFile(cfgPath, []byte(v1Config), 0o600); err != nil {
		t.Fatal(err)
	}
	clk := clock.NewVirtual(time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC))

	res, err := MigrateFromYAML(dbPath, cfgPath, "2.0.0", clk)
	if err != nil {
		t.Fatalf("миграция: %v", err)
	}
	if res.Skipped {
		t.Fatal("миграция пропущена, хотя должна была работать")
	}
	// Перенесено ключей = число рабочих ключей в исходном YAML (13).
	if res.MigratedKeys != 13 {
		t.Fatalf("перенесено ключей = %d, ждём 13", res.MigratedKeys)
	}
	// Копии созданы.
	if !fileExists(t, res.DBBackup) {
		t.Fatalf("нет копии БД: %s", res.DBBackup)
	}
	if !fileExists(t, res.ConfigBackup) {
		t.Fatalf("нет копии конфига: %s", res.ConfigBackup)
	}

	// Рабочие ключи в БД с верными значениями.
	st, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	if err := st.LoadWorkingInto(cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Scheduler.AgingSec != 1234 {
		t.Errorf("aging_sec = %d, ждём 1234", cfg.Scheduler.AgingSec)
	}
	if cfg.Web.SessionTTLDays != 20 {
		t.Errorf("session_ttl_days = %d, ждём 20", cfg.Web.SessionTTLDays)
	}
	if cfg.Node.ScanIntervalSec != 3 {
		t.Errorf("scan_interval_sec = %d, ждём 3", cfg.Node.ScanIntervalSec)
	}
	if cfg.Profiles.Qwen.ModelAlias != "test-model" {
		t.Errorf("model_alias = %q, ждём test-model", cfg.Profiles.Qwen.ModelAlias)
	}
	// Сервер в БД.
	servers, err := st.ListServers()
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 1 || servers[0].Name != "srv1" {
		t.Fatalf("серверы: %+v", servers)
	}
	// Сессия v1 сохранена (данные не потеряны).
	if _, err := st.GetSession("s1"); err != nil {
		t.Fatalf("сессия s1 потеряна: %v", err)
	}
	_ = st.Close()

	// Конфиг переписан: рабочие секции удалены, bootstrap сохранён.
	rewritten, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := parseRawMap(rewritten)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"scheduler", "dispatch", "turn", "gateway",
		"monitor", "limits", "retention", "profiles", "servers"} {
		if _, ok := raw[k]; ok {
			t.Errorf("конфиг после миграции содержит рабочую секцию %s", k)
		}
	}
	// bootstrap coordinator и web.bind сохранены.
	if _, ok := raw["coordinator"]; !ok {
		t.Error("coordinator потерян")
	}
	if web, ok := raw["web"].(map[string]any); ok {
		if _, ok := web["session_ttl_days"]; ok {
			t.Error("web.session_ttl_days должен быть удалён")
		}
		if _, ok := web["bind"]; !ok {
			t.Error("web.bind должен остаться")
		}
	}
	// Переписанный конфиг валиден (bootstrap-only).
	if err := os.WriteFile(filepath.Join(dir, "rewritten.yaml"), rewritten, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(filepath.Join(dir, "rewritten.yaml")); err != nil {
		t.Fatalf("переписанный конфиг невалиден: %v", err)
	}

	// Повторный запуск — no-op.
	res2, err := MigrateFromYAML(dbPath, cfgPath, "2.0.0", clk)
	if err != nil {
		t.Fatalf("повторная миграция: %v", err)
	}
	if !res2.Skipped {
		t.Fatal("повторная миграция должна быть no-op")
	}
}

// 4.8: свежая установка — файла БД нет. Миграция создаёт БД, копию делает
// только для конфига; повтор — no-op.
func TestMigrateFromYAMLFreshDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "runpilot.db") // файла нет
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(v1Config), 0o600); err != nil {
		t.Fatal(err)
	}
	clk := clock.NewVirtual(time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC))

	res, err := MigrateFromYAML(dbPath, cfgPath, "2.0.0", clk)
	if err != nil {
		t.Fatalf("миграция на свежей установке: %v", err)
	}
	if res.Skipped {
		t.Fatal("миграция не должна быть пропущена")
	}
	if res.MigratedKeys != 13 {
		t.Fatalf("перенесено ключей = %d, ждём 13", res.MigratedKeys)
	}
	// Копия БД не создаётся (файла не было); копия конфига — да.
	if res.DBBackup != "" {
		t.Fatalf("копия БД на свежей установке: %q", res.DBBackup)
	}
	if !fileExists(t, res.ConfigBackup) {
		t.Fatal("нет копии конфига")
	}
	// Значения в БД, сервер перенесён.
	st, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	if err := st.LoadWorkingInto(cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Scheduler.AgingSec != 1234 {
		t.Errorf("aging_sec = %d, ждём 1234", cfg.Scheduler.AgingSec)
	}
	servers, err := st.ListServers()
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 1 || servers[0].Name != "srv1" {
		t.Fatalf("серверы: %+v", servers)
	}
	_ = st.Close()

	// Повторный запуск — no-op.
	res2, err := MigrateFromYAML(dbPath, cfgPath, "2.0.0", clk)
	if err != nil || !res2.Skipped {
		t.Fatalf("повторный запуск: res=%+v err=%v, ждём no-op", res2, err)
	}
}

// 4.8: свежая установка + ошибка — созданные файлы БД убираются, конфиг
// восстанавливается.
func TestMigrateRollbackFreshDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "runpilot.db")
	cfgPath := filepath.Join(dir, "config.yaml")
	badCfg := `coordinator:
  bind: 192.0.2.10
scheduler:
  tick_ms: "не число"
`
	if err := os.WriteFile(cfgPath, []byte(badCfg), 0o600); err != nil {
		t.Fatal(err)
	}
	origCfg, _ := os.ReadFile(cfgPath)
	clk := clock.NewVirtual(time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC))

	if _, err := MigrateFromYAML(dbPath, cfgPath, "2.0.0", clk); err == nil {
		t.Fatal("миграция с невалидным конфигом должна быть отклонена")
	}
	// Конфиг восстановлен.
	gotCfg, _ := os.ReadFile(cfgPath)
	if string(gotCfg) != string(origCfg) {
		t.Fatal("конфиг не восстановлен после ошибки")
	}
	// Файл БД не остался (созданное убрано).
	if fileExists(t, dbPath) {
		t.Fatal("созданный файл БД остался после отката")
	}
}

// Роллбек при ошибке: исходные файлы остаются нетронутыми.
func TestMigrateRollbackOnError(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "runpilot.db")
	cfgPath := filepath.Join(dir, "config.yaml")
	createV1DB(t, dbPath)
	// Конфиг с рабочими секциями, но невалидным значением (тип) — config.Load
	// упадёт после создания копий и Open → роллбек.
	badCfg := `coordinator:
  bind: 192.0.2.10
scheduler:
  tick_ms: "не число"
`
	if err := os.WriteFile(cfgPath, []byte(badCfg), 0o600); err != nil {
		t.Fatal(err)
	}
	origCfg, _ := os.ReadFile(cfgPath)
	origDB, _ := os.ReadFile(dbPath)
	clk := clock.NewVirtual(time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC))

	if _, err := MigrateFromYAML(dbPath, cfgPath, "2.0.0", clk); err == nil {
		t.Fatal("миграция с невалидным конфигом должна быть отклонена")
	}
	// Конфиг восстановлен.
	gotCfg, _ := os.ReadFile(cfgPath)
	if string(gotCfg) != string(origCfg) {
		t.Fatal("конфиг не восстановлен после ошибки")
	}
	// БД восстановлена: таблицы setting нет.
	if exists, _ := settingTableExists(dbPath); exists {
		t.Fatal("таблица setting осталась после отката")
	}
	gotDB, _ := os.ReadFile(dbPath)
	if string(gotDB) != string(origDB) {
		t.Fatal("БД не восстановлена после ошибки")
	}
}
