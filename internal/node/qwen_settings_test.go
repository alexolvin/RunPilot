package node

// W9 доп-3c (политика 2026-10-02): EnsureQwenSettings приводит
// ~/.qwen/settings.json в порядок (selectedType=openai), НО modelProviders
// оператора не удаляет — эндпоинты остаются в /model кодера. Опасна только
// коллизия id == model_alias (кодер уходит на прямой baseUrl): Warning.
// Идемпотентно, с резервной копией.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"runpilot/internal/proto"
)

const qwenTestAlias = "qwen3.6-27b"

func writeQwenFixture(t *testing.T, doc string) (dir, path string) {
	t.Helper()
	dir = t.TempDir()
	t.Setenv("HOME", dir)
	path = filepath.Join(dir, ".qwen", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if doc != "" {
		if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir, path
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("не JSON: %v", err)
	}
	return doc
}

func TestEnsureQwenSettingsCreatesWhenMissing(t *testing.T) {
	_, path := writeQwenFixture(t, "")
	n := newTestNode(t, &fakeExec{})
	n.EnsureQwenSettings(qwenTestAlias, 0)

	doc := readJSON(t, path)
	sec, _ := doc["security"].(map[string]any)
	auth, _ := sec["auth"].(map[string]any)
	if auth["selectedType"] != "openai" {
		t.Fatalf("selectedType=%v, хочу openai", auth["selectedType"])
	}
}

func TestEnsureQwenSettingsPatches(t *testing.T) {
	fixture := `{
		"model": {"name": "` + qwenTestAlias + `"},
		"security": {"auth": {"selectedType": "qwen"}},
		"modelProviders": {
			"openai": [
				{"id": "` + qwenTestAlias + `", "name": "local", "baseUrl": "http://127.0.0.1:8004/v1"},
				{"id": "other-model", "name": "keep", "baseUrl": "http://example/v1"}
			]
		}
	}`
	_, path := writeQwenFixture(t, fixture)
	n := newTestNode(t, &fakeExec{})
	n.EnsureQwenSettings(qwenTestAlias, 0)

	res := n.EnsureQwenSettings(qwenTestAlias, 0)
	doc := readJSON(t, path)
	// selectedType → openai.
	sec, _ := doc["security"].(map[string]any)
	auth, _ := sec["auth"].(map[string]any)
	if auth["selectedType"] != "openai" {
		t.Fatalf("selectedType=%v, хочу openai", auth["selectedType"])
	}
	// modelProviders НЕ удалён: записи оператора остаются живыми (в /model
	// кодера доступны), даже если одна из них совпадает с alias.
	mp, ok := doc["modelProviders"].(map[string]any)
	if !ok {
		t.Fatal("modelProviders удалён, а должен остаться")
	}
	entries, _ := mp["openai"].([]any)
	if len(entries) != 2 {
		t.Fatalf("записей = %d, хочу 2 (обе сохранены)", len(entries))
	}
	// Коллизия id == alias — Warning, файл записями не тронут.
	if len(res.Warnings) != 1 {
		t.Fatalf("Warnings = %v, хочу 1 (коллизия id == alias)", res.Warnings)
	}
	if !strings.Contains(res.Warnings[0], qwenTestAlias) {
		t.Errorf("Warning = %q, должен называть alias", res.Warnings[0])
	}
	// Чужие поля не тронуты.
	model, _ := doc["model"].(map[string]any)
	if model["name"] != qwenTestAlias {
		t.Fatalf("model.name=%v, не должен меняться", model["name"])
	}
	// Резервная копия создана.
	bak := path + qwenSettingsBackupSuffix
	if _, err := os.Stat(bak); err != nil {
		t.Fatalf("нет резервной копии: %v", err)
	}
}

// TestEnsureQwenSettingsKeepsProvidersNoCollision — записи modelProviders с
// чужими id остаются, Warning нет: Runtime Model (alias из env) ходит через
// OPENAI_BASE_URL (шлюз), provider-эндпоинты доступны в /model.
func TestEnsureQwenSettingsKeepsProvidersNoCollision(t *testing.T) {
	fixture := `{
		"security": {"auth": {"selectedType": "openai"}},
		"modelProviders": {
			"openai": [
				{"id": "qwen3.8-27b", "name": "local", "baseUrl": "http://127.0.0.1:8004/v1"}
			],
			"openai-vzu5": [
				{"id": "qwen3.8-27b", "name": "vzu5", "baseUrl": "http://100.126.222.23:8004/v1"}
			]
		}
	}`
	_, path := writeQwenFixture(t, fixture)
	n := newTestNode(t, &fakeExec{})
	res := n.EnsureQwenSettings(qwenTestAlias, 0) // alias ≠ qwen3.8-27b
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("Warnings = %v, хочу пусто (нет коллизии)", res.Warnings)
	}
	if res.Changed {
		t.Fatalf("файл изменён без необходимости: %v", res.Changes)
	}
	doc := readJSON(t, path)
	mp, _ := doc["modelProviders"].(map[string]any)
	if len(mp) != 2 {
		t.Fatalf("провайдеров = %d, хочу 2", len(mp))
	}
}

func TestEnsureQwenSettingsIdempotent(t *testing.T) {
	fixture := `{
		"security": {"auth": {"selectedType": "qwen"}},
		"modelProviders": {"openai": [{"id": "` + qwenTestAlias + `"}]}
	}`
	_, path := writeQwenFixture(t, fixture)
	n := newTestNode(t, &fakeExec{})
	n.EnsureQwenSettings(qwenTestAlias, 0)
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Второй вызов — без изменений.
	n.EnsureQwenSettings(qwenTestAlias, 0)
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("второй вызов изменил файл, а должен быть идемпотентным")
	}
}

// TestEnsureQwenSettingsContextWindow — item 3: contextWindow пишется в
// model.generationConfig.contextWindowSize и идемпотентен (повтор тем же
// значением — без изменений); 0 — не трогает.
func TestEnsureQwenSettingsContextWindow(t *testing.T) {
	// Файла нет: создаётся с contextWindowSize.
	_, path := writeQwenFixture(t, "")
	n := newTestNode(t, &fakeExec{})
	res := n.EnsureQwenSettings(qwenTestAlias, 262144)
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	doc := readJSON(t, path)
	model, _ := doc["model"].(map[string]any)
	gc, _ := model["generationConfig"].(map[string]any)
	if int(gc["contextWindowSize"].(float64)) != 262144 {
		t.Fatalf("contextWindowSize=%v, хочу 262144", gc["contextWindowSize"])
	}
	// Повтор тем же значением — идемпотентно (Changed=false).
	res2 := n.EnsureQwenSettings(qwenTestAlias, 262144)
	if res2.Changed {
		t.Fatalf("повтор тем же contextWindow изменил файл: %v", res2.Changes)
	}
	// 0 — не трогает существующее значение.
	res3 := n.EnsureQwenSettings(qwenTestAlias, 0)
	if res3.Changed {
		t.Fatalf("contextWindow=0 изменил файл: %v", res3.Changes)
	}
	doc = readJSON(t, path)
	gc = doc["model"].(map[string]any)["generationConfig"].(map[string]any)
	if int(gc["contextWindowSize"].(float64)) != 262144 {
		t.Fatalf("после 0 contextWindowSize=%v, должен остаться 262144", gc["contextWindowSize"])
	}
	// Изменение значения — вносит изменение.
	res4 := n.EnsureQwenSettings(qwenTestAlias, 131072)
	if !res4.Changed {
		t.Fatal("смена contextWindow не изменила файл")
	}
	doc = readJSON(t, path)
	gc = doc["model"].(map[string]any)["generationConfig"].(map[string]any)
	if int(gc["contextWindowSize"].(float64)) != 131072 {
		t.Fatalf("contextWindowSize=%v, хочу 131072", gc["contextWindowSize"])
	}
}

// TestQwenSettingsOp — операция qwen_settings (доп-3i): исход → код result.
func TestQwenSettingsOp(t *testing.T) {
	writeQwenFixture(t, `{"security": {"auth": {"selectedType": "qwen"}}}`)
	n := newTestNode(t, &fakeExec{})
	m := proto.New(proto.KindQwenSettings)
	m.CmdID = "c1"
	m.ModelAlias = qwenTestAlias
	rep, err := n.Handle(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Result != proto.ResQwenSettingsChanged {
		t.Fatalf("Result = %q, хочу QWEN_SETTINGS_CHANGED", rep.Result)
	}
	if !strings.Contains(rep.Detail, "selectedType") {
		t.Errorf("Detail = %q, хочу упоминание selectedType", rep.Detail)
	}
	// Повтор — без изменений.
	rep2, err := n.Handle(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	if rep2.Result != proto.ResQwenSettingsOK {
		t.Fatalf("повтор: Result = %q, хочу QWEN_SETTINGS_OK", rep2.Result)
	}
	// Пустой model_alias — ошибка.
	m2 := proto.New(proto.KindQwenSettings)
	m2.CmdID = "c2"
	rep3, err := n.Handle(context.Background(), m2)
	if err != nil {
		t.Fatal(err)
	}
	if rep3.Result != proto.ResQwenSettingsError {
		t.Fatalf("пустой alias: Result = %q, хочу QWEN_SETTINGS_ERROR", rep3.Result)
	}
}

// TestSpawnEnsuresQwenSettings — auto-подключение к шлюзу (доп-3i): spawn
// приводит ~/.qwen/settings.json в порядок ДО запуска кодера.
func TestSpawnEnsuresQwenSettings(t *testing.T) {
	home, path := writeQwenFixture(t, `{"security": {"auth": {"selectedType": "qwen"}}}`)
	proj := filepath.Join(home, "proj")
	if err := os.Mkdir(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	ex := &fakeExec{respond: func(name string, args ...string) (string, error) {
		joined := strings.Join(args, " ")
		switch {
		case strings.Contains(joined, "has-session"):
			return "", os.ErrNotExist // сессия ещё не существует
		case strings.Contains(joined, "list-panes"):
			return "%10\n", nil
		default:
			return "", nil
		}
	}}
	n := newTestNode(t, ex)
	n.SetProjectRoots([]string{home})
	m := proto.New(proto.KindSpawn)
	m.CmdID = "c1"
	m.Name = "spawntest"
	m.Dir = proj
	m.SID = "NEWSID"
	m.Env = map[string]string{"OPENAI_MODEL": qwenTestAlias, "OPENAI_API_KEY": "runpilot"}
	rep, err := n.Handle(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Result != proto.ResSpawned {
		t.Fatalf("Result = %q, хочу SPAWNED (detail=%s)", rep.Result, rep.Detail)
	}
	doc := readJSON(t, path)
	sec, _ := doc["security"].(map[string]any)
	auth, _ := sec["auth"].(map[string]any)
	if auth["selectedType"] != "openai" {
		t.Fatalf("selectedType = %v, хочу openai (spawn не привёл settings.json)", auth["selectedType"])
	}
}
