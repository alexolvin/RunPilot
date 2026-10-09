package node

// Эндпоинт qwen code (W9 доп-3c, политика пересмотрена 2026-10-02).
// runpilot запускает кодера с окружением OPENAI_BASE_URL=<шлюз>/s/<sid>/v1 +
// OPENAI_MODEL=<model_alias>. Qwen Code резолвит baseUrl слоями
// (документация model-providers, «Resolution Layers», first present wins):
//   - модель, ВЫБРАННАЯ из modelProviders (её id совпал с текущей моделью),
//     — sealed-слой: baseUrl берётся из записи провайдера;
//   - иначе модель — Runtime Model: baseUrl из env (OPENAI_BASE_URL) →
//     settings → defaults.
//
// Отсюда политика EnsureQwenSettings:
//   - security.auth.selectedType = "openai" (обязательно: иначе OPENAI_*-env
//     не участвует в резолвинге);
//   - modelProviders НЕ удаляются (операторское решение 2026-10-02): чужие
//     эндпоинты остаются живыми, в /model кодера они видны и доступны —
//     оператор может переключить кодера на прямой бэкенд, когда нужно.
//     Шлюз по умолчанию: model_alias из env не совпадает с id записей →
//     Runtime Model → OPENAI_BASE_URL (шлюз) выигрывает.
//   - КОЛЛИЗИЯ (id записи == model_alias) — единственный опасный случай:
//     текущая модель становится Provider Model и идёт на прямой baseUrl в
//     обход шлюза. Узел записи не чинит и не удаляет (данные оператора);
//     возвращает Warning — узел логирует WARN, веб-отчёт показывает, doctor
//     ставит FAIL. Оператор решает: сменить model_alias (рабочая настройка)
//     или id записи.
//
// Идемпотентно; при первом изменении — резервная копия
// settings.json.runpilot.bak; каждое изменение — в журнал.
// Ошибки не фатальны (WARN) — узел работает, и только Warning о коллизии
// говорит, что сессии могут ходить мимо шлюза.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const qwenSettingsBackupSuffix = ".runpilot.bak"

// QwenSettingsResult — исход приведения ~/.qwen/settings.json в порядок
// (W9 доп-3i): Changed — файл создан/изменён, Changes — что именно,
// Warnings — проблемы, которые узел НЕ чинит (коллизия model_alias с id
// записи modelProviders: кодер пойдёт на прямой baseUrl в обход шлюза),
// Err — ошибка (в этом случае файл не тронут либо изменён частично).
type QwenSettingsResult struct {
	Changed  bool
	Changes  []string
	Warnings []string
	Err      error
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
// (до запуска кодера) и из операции qwen_settings (явная проверка с отчётом
// в веб). modelAlias — alias шлюза из env сессии: используется только для
// проверки коллизии с id записей modelProviders (см. политику вверху);
// записи не удаляются. contextWindow (item 3) — размер контекста (min по
// серверам): пишется в model.generationConfig.contextWindowSize (для
// Runtime Model — это шлюзовой alias); 0 — не менять. Возвращает результат.
func (n *Node) EnsureQwenSettings(modelAlias string, contextWindow int) QwenSettingsResult {
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
			return n.createQwenSettings(path, contextWindow)
		}
		n.log.Warn("node: qwen settings: чтение", "err", err.Error())
		return QwenSettingsResult{Err: fmt.Errorf("чтение: %w", err)}
	}
	return n.patchQwenSettings(path, data, modelAlias, contextWindow)
}

// applyContextWindow — model.generationConfig.contextWindowSize (item 3).
// Возвращает строку изменения ("" — не задано (0) или уже совпадает).
func applyContextWindow(doc map[string]any, contextWindow int) string {
	if contextWindow <= 0 {
		return ""
	}
	model, _ := doc["model"].(map[string]any)
	if model == nil {
		model = map[string]any{}
		doc["model"] = model
	}
	gc, _ := model["generationConfig"].(map[string]any)
	if gc == nil {
		gc = map[string]any{}
		model["generationConfig"] = gc
	}
	// после json.Unmarshal число — float64; сверяем с ним.
	if f, ok := gc["contextWindowSize"].(float64); ok && int(f) == contextWindow {
		return ""
	}
	gc["contextWindowSize"] = contextWindow
	return fmt.Sprintf("model.generationConfig.contextWindowSize: %d", contextWindow)
}

// createQwenSettings — файла нет: создаём минимальный правильный конфиг.
func (n *Node) createQwenSettings(path string, contextWindow int) QwenSettingsResult {
	doc := map[string]any{
		"security": map[string]any{
			"auth": map[string]any{"selectedType": "openai"},
		},
	}
	changes := []string{"файл создан: security.auth.selectedType=openai"}
	if cw := applyContextWindow(doc, contextWindow); cw != "" {
		changes = append(changes, cw)
	}
	if err := n.writeQwenSettings(path, doc, strings.Join(changes, "; ")); err != nil {
		return QwenSettingsResult{Err: err}
	}
	return QwenSettingsResult{Changed: true, Changes: changes}
}

// gatewayModelAlias — запасной alias шлюза, не совпадающий с id записей
// modelProviders: если сконфигурированный model_alias коллидирует с id
// записи, узел запускает кодера с этим alias (Runtime Model →
// OPENAI_BASE_URL = шлюз). Шлюз подставит реальную модель (transformBody),
// поэтому alias может быть любым; операторский прямой эндпоинт остаётся
// доступным в /model (id записи не меняется).
const gatewayModelAlias = "runpilot"

// providerIDCollision — есть ли в modelProviders запись с id == modelAlias.
// Такая запись превращает текущую (шлюзовую) модель в Provider Model:
// qwen берёт baseUrl из записи, а не из OPENAI_BASE_URL — сессия идёт мимо
// шлюза. Возвращает список описаний коллизий ("" — нет).
func providerIDCollision(doc map[string]any, modelAlias string) []string {
	var out []string
	mp, ok := doc["modelProviders"].(map[string]any)
	if !ok {
		return nil
	}
	for prov, modelsAny := range mp {
		models, ok := modelsAny.([]any)
		if !ok {
			continue
		}
		for _, entryAny := range models {
			entry, ok := entryAny.(map[string]any)
			if !ok {
				continue
			}
			if id, _ := entry["id"].(string); id != "" && id == modelAlias {
				out = append(out, fmt.Sprintf(
					"modelProviders.%s: id %q совпадает с model_alias — кодер пойдёт на прямой baseUrl (в обход шлюза); смените model_alias или id записи",
					prov, id))
			}
		}
	}
	return out
}

// safeModelAlias — alias для запуска кодера, гарантированно не совпадающий
// с id записей modelProviders (W13 этап 3: закрыть обход шлюза). Если
// modelAlias коллидирует с id записи, qwen сочтёт текущую модель Provider
// Model и возьмёт прямой baseUrl провайдера (в обход шлюза) — даже при
// явном --openai-base-url. Возвращает gatewayModelAlias (Runtime Model →
// OPENAI_BASE_URL = шлюз); шлюз подставит реальную модель. Без коллизии —
// возвращает modelAlias как есть. Ошибки чтения конфига — modelAlias (нет
// данных о коллизии → не хуже прежнего).
func (n *Node) safeModelAlias(modelAlias string) string {
	if modelAlias == "" {
		return modelAlias
	}
	path, err := qwenSettingsPath()
	if err != nil {
		return modelAlias
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return modelAlias
	}
	var doc map[string]any
	if json.Unmarshal(data, &doc) != nil {
		return modelAlias
	}
	if len(providerIDCollision(doc, modelAlias)) == 0 {
		return modelAlias
	}
	n.log.Warn("node: qwen settings: model_alias совпадает с id modelProviders — запускаю кодера с alias шлюза (обход закрыт)",
		"alias", modelAlias, "safe", gatewayModelAlias)
	return gatewayModelAlias
}

// patchQwenSettings — внести изменения; Changed=false, если они не нужны.
// modelProviders не удаляются (политика см. вверху): только проверка
// коллизии model_alias с id записей → Warnings (без правки файла).
func (n *Node) patchQwenSettings(path string, data []byte, modelAlias string, contextWindow int) QwenSettingsResult {
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		n.log.Warn("node: qwen settings: не JSON", "path", path, "err", err.Error())
		return QwenSettingsResult{Err: fmt.Errorf("файл не JSON: %w", err)}
	}
	var changes []string
	// 1) security.auth.selectedType = "openai" — без этого OPENAI_*-env не
	//    участвует в резолвинге Runtime Model.
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
	// 2) modelProviders — не трогаем (операторские эндпоинты остаются
	//    доступными в /model). Коллизия id == model_alias — Warning:
	//    именно она уводит текущую сессию на прямой baseUrl.
	warnings := providerIDCollision(doc, modelAlias)
	for _, w := range warnings {
		n.log.Warn("node: qwen settings: "+w)
	}
	// 3) model.generationConfig.contextWindowSize (item 3) — для Runtime
	//    Model (шлюзовый alias) top-level generationConfig и участвует в
	//    резолвинге (provider-записи sealed и свой не трогаем).
	if cw := applyContextWindow(doc, contextWindow); cw != "" {
		changes = append(changes, cw)
	}
	if len(changes) == 0 {
		return QwenSettingsResult{Warnings: warnings}
	}
	if err := n.writeQwenSettings(path, doc, strings.Join(changes, "; ")); err != nil {
		return QwenSettingsResult{Changes: changes, Warnings: warnings, Err: err}
	}
	return QwenSettingsResult{Changed: true, Changes: changes, Warnings: warnings}
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
