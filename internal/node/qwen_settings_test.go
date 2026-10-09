package node

// W13 этап 7: маршрут кодера — только аргументы процесса, без правки
// settings.json. PreCheckCoder — предпроверка перед созданием сессии:
//   - коллизия model_alias с id modelProviders (пользовательский + проектный
//     settings.json) → ошибка (409);
//   - установленный qwen поддерживает --auth-type / --openai-base-url.
// Записей в settings.json НЕТ — данные оператора не трогаем.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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

// writeProjectSettings — проектный .qwen/settings.json в dir.
func writeProjectSettings(t *testing.T, dir, doc string) string {
	t.Helper()
	path := projectSettingsPath(dir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// --- providerIDCollision ---

func TestProviderIDCollisionMatch(t *testing.T) {
	doc := map[string]any{
		"modelProviders": map[string]any{
			"openai": []any{
				map[string]any{"id": qwenTestAlias, "name": "test", "baseUrl": "http://x"},
			},
		},
	}
	coll := providerIDCollision(doc, qwenTestAlias)
	if len(coll) != 1 {
		t.Fatalf("коллизий %d, хочу 1: %v", len(coll), coll)
	}
}

func TestProviderIDCollisionNoMatch(t *testing.T) {
	doc := map[string]any{
		"modelProviders": map[string]any{
			"openai": []any{
				map[string]any{"id": "other-model", "name": "test", "baseUrl": "http://x"},
			},
		},
	}
	if coll := providerIDCollision(doc, qwenTestAlias); len(coll) != 0 {
		t.Fatalf("коллизий %d, хочу 0: %v", len(coll), coll)
	}
}

func TestProviderIDCollisionNoProviders(t *testing.T) {
	if coll := providerIDCollision(map[string]any{}, qwenTestAlias); len(coll) != 0 {
		t.Fatalf("коллизий %d, хочу 0", len(coll))
	}
}

// --- settingsCollision (user + project) ---

func TestSettingsCollisionUser(t *testing.T) {
	_, _ = writeQwenFixture(t, `{"modelProviders":{"openai":[{"id":"`+qwenTestAlias+`"}]}}`)
	coll := settingsCollision(qwenTestAlias, "")
	if len(coll) != 1 {
		t.Fatalf("коллизий %d, хочу 1: %v", len(coll), coll)
	}
	if !strings.Contains(coll[0], "modelProviders.openai") {
		t.Fatalf("описание %q не содержит modelProviders.openai", coll[0])
	}
}

func TestSettingsCollisionProject(t *testing.T) {
	_, _ = writeQwenFixture(t, "") // пользовательский — нет коллизии
	dir := t.TempDir()
	writeProjectSettings(t, dir, `{"modelProviders":{"openai":[{"id":"`+qwenTestAlias+`"}]}}`)
	coll := settingsCollision(qwenTestAlias, dir)
	if len(coll) != 1 {
		t.Fatalf("коллизий %d, хочу 1: %v", len(coll), coll)
	}
}

func TestSettingsCollisionNone(t *testing.T) {
	_, _ = writeQwenFixture(t, `{"modelProviders":{"openai":[{"id":"other"}]}}`)
	dir := t.TempDir()
	writeProjectSettings(t, dir, `{"modelProviders":{"openai":[{"id":"other2"}]}}`)
	if coll := settingsCollision(qwenTestAlias, dir); len(coll) != 0 {
		t.Fatalf("коллизий %d, хочу 0: %v", len(coll), coll)
	}
}

func TestSettingsCollisionBoth(t *testing.T) {
	_, _ = writeQwenFixture(t, `{"modelProviders":{"openai":[{"id":"`+qwenTestAlias+`"}]}}`)
	dir := t.TempDir()
	writeProjectSettings(t, dir, `{"modelProviders":{"openai-vzu5":[{"id":"`+qwenTestAlias+`"}]}}`)
	coll := settingsCollision(qwenTestAlias, dir)
	if len(coll) != 2 {
		t.Fatalf("коллизий %d, хочу 2: %v", len(coll), coll)
	}
}

// --- PreCheckCoder ---

func TestPreCheckCoderCollision(t *testing.T) {
	_, _ = writeQwenFixture(t, `{"modelProviders":{"openai":[{"id":"`+qwenTestAlias+`"}]}}`)
	n := newTestNode(t, &fakeExec{})
	err := n.PreCheckCoder(qwenTestAlias, "", "qwen")
	if err == nil {
		t.Fatal("ожидал ошибку (коллизия)")
	}
	if !strings.Contains(err.Error(), "обход шлюза") {
		t.Fatalf("ошибка %q не содержит 'обход шлюза'", err.Error())
	}
}

func TestPreCheckCoderEmptyAlias(t *testing.T) {
	n := newTestNode(t, &fakeExec{})
	if err := n.PreCheckCoder("", "", "qwen"); err == nil {
		t.Fatal("ожидал ошибку (пустой alias)")
	}
}

func TestPreCheckCoderNoCollision(t *testing.T) {
	_, _ = writeQwenFixture(t, `{"modelProviders":{"openai":[{"id":"other"}]}}`)
	// checkQwenSupport: кэшируем OK (не вызываем qwen --help в тесте).
	qwenSupportMu.Lock()
	qwenSupportOK = true
	qwenSupportChecked = true
	qwenSupportMu.Unlock()
	n := newTestNode(t, &fakeExec{})
	if err := n.PreCheckCoder(qwenTestAlias, "", "qwen"); err != nil {
		t.Fatalf("не ожидал ошибку: %v", err)
	}
}
