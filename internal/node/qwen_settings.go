package node

// Эндпоинт qwen code (W9 доп-3c). runpilot запускает кодера с окружением
// OPENAI_BASE_URL=<шлюз>/s/<sid>/v1 + OPENAI_MODEL=<model_alias>, но qwen
// code может перебить его собственным ~/.qwen/settings.json:
//   - security.auth.selectedType != "openai" → OPENAI_*-окружение не
//     используется (auth по умолчанию);
//   - modelProviders с id = model_alias → qwen берёт baseUrl из записи,
//     а не из окружения.
//
// ensureQwenSettings приводит файл в порядок (то, что проверяет doctor
// qwen_settings, раздел 7 ТЗ): selectedType="openai" и ни одной записи
// modelProviders с id = model_alias. Идемпотентно; при первом изменении —
// резервная копия settings.json.runpilot.bak; каждое изменение — в журнал.
// Ошибки не фатальны (WARN) — узел работает, аqm-сессии просто могут
// ходить мимо шлюза, и оператор видит это по doctor/мониторингу.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const qwenSettingsBackupSuffix = ".runpilot.bak"

// QwenSettingsResult — исход приведения ~/.qwen/settings.json в порядок
// (W9 доп-3i): Changed — файл создан/изменён, Changes — что именно, Err —
// ошибка (в этом случае файл не тронут либо изменён частично).
type QwenSettingsResult struct {
	Changed bool
	Changes []string
	Err     error
}

// qwenSettingsPath — ~/.qwen/settings.json.
func qwenSettingsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".qwen", "settings.json"), nil
}

// EnsureQwenSettings — см. комментарий вверху. Вызывается из config
// (hello-ответ координатора) — на старте и при каждом reconnect, из spawn
// (автоматическое подключение кодера к шлюзу перед запуском) и из операции
// qwen_settings (явная проверка с отчётом в веб). Возвращает результат.
func (n *Node) EnsureQwenSettings(modelAlias string) QwenSettingsResult {
	if modelAlias == "" {
		return QwenSettingsResult{Err: fmt.Errorf("пустой model_alias")}
	}
	path, err := qwenSettingsPath()
	if err != nil {
		n.log.Warn("node: qwen settings: HOME не определён")
		return QwenSettingsResult{Err: fmt.Errorf("HOME не определён: %w", err)}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return n.createQwenSettings(path, modelAlias)
		}
		n.log.Warn("node: qwen settings: чтение", "err", err.Error())
		return QwenSettingsResult{Err: fmt.Errorf("чтение: %w", err)}
	}
	return n.patchQwenSettings(path, data, modelAlias)
}

// createQwenSettings — файла нет: создаём минимальный правильный конфиг.
func (n *Node) createQwenSettings(path, modelAlias string) QwenSettingsResult {
	doc := map[string]any{
		"security": map[string]any{
			"auth": map[string]any{"selectedType": "openai"},
		},
	}
	if err := n.writeQwenSettings(path, doc, "создан (security.auth.selectedType=openai)"); err != nil {
		return QwenSettingsResult{Err: err}
	}
	return QwenSettingsResult{Changed: true,
		Changes: []string{"файл создан: security.auth.selectedType=openai"}}
}

// patchQwenSettings — внести изменения; Changed=false, если они не нужны.
func (n *Node) patchQwenSettings(path string, data []byte, modelAlias string) QwenSettingsResult {
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		n.log.Warn("node: qwen settings: не JSON", "path", path, "err", err.Error())
		return QwenSettingsResult{Err: fmt.Errorf("файл не JSON: %w", err)}
	}
	var changes []string
	// 1) security.auth.selectedType = "openai".
	sec, _ := doc["security"].(map[string]any)
	if sec == nil {
		sec = map[string]any{}
		doc["security"] = sec
	}
	auth, _ := sec["auth"].(map[string]any)
	if auth == nil {
		auth = map[string]any{}
		sec["auth"] = auth
	}
	if sel, _ := auth["selectedType"].(string); sel != "openai" {
		auth["selectedType"] = "openai"
		changes = append(changes, "security.auth.selectedType: "+sel+" → openai")
	}
	// 2) modelProviders: записи с id = model_alias (перебивают OPENAI_*).
	if mp, ok := doc["modelProviders"].(map[string]any); ok {
		for prov, listAny := range mp {
			list, ok := listAny.([]any)
			if !ok {
				continue
			}
			var kept []any
			removed := 0
			for _, item := range list {
				m, _ := item.(map[string]any)
				id, _ := m["id"].(string)
				if id == modelAlias {
					removed++
					continue
				}
				kept = append(kept, item)
			}
			if removed > 0 {
				mp[prov] = kept
				changes = append(changes, "modelProviders."+prov+": убраны записи с id="+modelAlias)
			}
		}
	}
	if len(changes) == 0 {
		return QwenSettingsResult{}
	}
	if err := n.writeQwenSettings(path, doc, strings.Join(changes, "; ")); err != nil {
		return QwenSettingsResult{Changes: changes, Err: err}
	}
	return QwenSettingsResult{Changed: true, Changes: changes}
}

// writeQwenSettings — резервная копия (однократно) + атомарная запись.
func (n *Node) writeQwenSettings(path string, doc map[string]any, note string) error {
	bak := path + qwenSettingsBackupSuffix
	if _, err := os.Stat(bak); os.IsNotExist(err) {
		if b, rerr := os.ReadFile(path); rerr == nil && len(b) > 0 {
			if werr := os.WriteFile(bak, b, qwenSettingsFileMode); werr != nil {
				n.log.Warn("node: qwen settings: резервная копия", "err", werr.Error())
			}
		}
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("JSON: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, qwenSettingsFileMode); err != nil {
		return fmt.Errorf("запись: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	n.log.Info("node: qwen settings обновлён: "+note, "path", path, "backup", bak)
	return nil
}
