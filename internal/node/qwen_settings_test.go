package node

// W9 доп-3c: EnsureQwenSettings приводит ~/.qwen/settings.json в порядок,
// чтобы окружение сессии шлюза (OPENAI_BASE_URL/OPENAI_MODEL) не перебивалось
// конфигом qwen code. Идемпотентно, с резервной копией.

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
	n.EnsureQwenSettings(qwenTestAlias)

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
	n.EnsureQwenSettings(qwenTestAlias)

	doc := readJSON(t, path)
	// selectedType → openai.
	sec, _ := doc["security"].(map[string]any)
	auth, _ := sec["auth"].(map[string]any)
	if auth["selectedType"] != "openai" {
		t.Fatalf("selectedType=%v, хочу openai", auth["selectedType"])
	}
	// Запись с id=model_alias убрана, чужая модель сохранена.
	mp, _ := doc["modelProviders"].(map[string]any)
	list, _ := mp["openai"].([]any)
	if len(list) != 1 {
		t.Fatalf("modelProviders.openai = %v, хочу 1 запись", list)
	}
	m0, _ := list[0].(map[string]any)
	if m0["id"] != "other-model" {
		t.Fatalf("осталась запись id=%v, хочу other-model", m0["id"])
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

func TestEnsureQwenSettingsIdempotent(t *testing.T) {
	fixture := `{
		"security": {"auth": {"selectedType": "qwen"}},
		"modelProviders": {"openai": [{"id": "` + qwenTestAlias + `"}]}
	}`
	_, path := writeQwenFixture(t, fixture)
	n := newTestNode(t, &fakeExec{})
	n.EnsureQwenSettings(qwenTestAlias)
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Второй вызов — без изменений.
	n.EnsureQwenSettings(qwenTestAlias)
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("второй вызов изменил файл, а должен быть идемпотентным")
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
