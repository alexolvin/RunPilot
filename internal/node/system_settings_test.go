package node

// W15 этап 3: per-сессии system-settings.json (модальности пула + контекст).
// Тесты: генерация файла (intersection, min, merge, 0600), удаление на GONE.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"runpilot/internal/config"
)

// testNodeForSettings — узел с state_dir в temp-каталоге.
func testNodeForSettings(t *testing.T) (*Node, string) {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Defaults()
	cfg.Node.StateDir = dir
	n := &Node{cfg: cfg}
	return n, dir
}

// S1: modalitiesObject — ["video"] → {"image":false,"video":true}.
func TestModalitiesObject(t *testing.T) {
	got := modalitiesObject([]string{"video"})
	if got["image"] || !got["video"] {
		t.Fatalf("modalitiesObject([video]) = %v, хочу image=false video=true", got)
	}
	got = modalitiesObject([]string{"image", "video"})
	if !got["image"] || !got["video"] {
		t.Fatalf("modalitiesObject([image,video]) = %v, хочу оба true", got)
	}
	got = modalitiesObject(nil)
	if got["image"] || got["video"] {
		t.Fatalf("modalitiesObject(nil) = %v, хочу оба false", got)
	}
}

// S2: systemSettingsDoc — modalities + contextWindowSize (0 — не писать).
func TestSystemSettingsDoc(t *testing.T) {
	doc := systemSettingsDoc([]string{"video"}, 262144)
	model, _ := doc["model"].(map[string]any)
	gc, _ := model["generationConfig"].(map[string]any)
	if gc["contextWindowSize"] != 262144 {
		t.Fatalf("contextWindowSize = %v, хочу 262144", gc["contextWindowSize"])
	}
	mod, _ := gc["modalities"].(map[string]bool)
	if !mod["video"] || mod["image"] {
		t.Fatalf("modalities = %v, хочу video=true image=false", mod)
	}
	// contextWindow=0 → не писать.
	doc = systemSettingsDoc(nil, 0)
	model, _ = doc["model"].(map[string]any)
	gc, _ = model["generationConfig"].(map[string]any)
	if _, ok := gc["contextWindowSize"]; ok {
		t.Fatalf("contextWindowSize=0 не должен писаться: %v", gc)
	}
}

// S3: writeSystemSettings — файл mode 0600, путь <state_dir>/qwen/<sid>/,
// содержимое = override (modalities + contextWindowSize).
func TestWriteSystemSettings(t *testing.T) {
	n, dir := testNodeForSettings(t)
	sid := "SS00000001"
	path, err := n.writeSystemSettings(sid, []string{"video"}, 262144)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "qwen", sid, "system-settings.json")
	if path != want {
		t.Fatalf("путь = %q, хочу %q", path, want)
	}
	// mode 0600.
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, хочу 0600", fi.Mode().Perm())
	}
	// Содержимое.
	var doc map[string]any
	if err := json.Unmarshal(readFileBytes(t, path), &doc); err != nil {
		t.Fatal(err)
	}
	model, _ := doc["model"].(map[string]any)
	gc, _ := model["generationConfig"].(map[string]any)
	if v, _ := gc["contextWindowSize"].(float64); int(v) != 262144 {
		t.Fatalf("contextWindowSize = %v, хочу 262144", gc["contextWindowSize"])
	}
}

// S4: deleteSystemSettings — GONE → каталог сессии удалён.
func TestDeleteSystemSettings(t *testing.T) {
	n, dir := testNodeForSettings(t)
	sid := "SS00000001"
	if _, err := n.writeSystemSettings(sid, []string{"video"}, 262144); err != nil {
		t.Fatal(err)
	}
	n.deleteSystemSettings(sid)
	if _, err := os.Stat(filepath.Join(dir, "qwen", sid)); !os.IsNotExist(err) {
		t.Fatalf("каталог сессии не удалён: %v", err)
	}
}

// S5: applySystemSettingsEnv — env получает QWEN_CODE_SYSTEM_SETTINGS_PATH.
func TestApplySystemSettingsEnv(t *testing.T) {
	n, _ := testNodeForSettings(t)
	env := map[string]string{}
	n.applySystemSettingsEnv(env, "SS00000001", []string{"video"}, 262144)
	if env["QWEN_CODE_SYSTEM_SETTINGS_PATH"] == "" {
		t.Fatalf("QWEN_CODE_SYSTEM_SETTINGS_PATH не установлен: %v", env)
	}
	// Файл существует.
	if _, err := os.Stat(env["QWEN_CODE_SYSTEM_SETTINGS_PATH"]); err != nil {
		t.Fatalf("файл не существует: %v", err)
	}
}

func readFileBytes(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
