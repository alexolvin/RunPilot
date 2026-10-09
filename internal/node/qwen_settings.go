package node

// Маршрут кодера (W13 этап 7) — только аргументы процесса, без правки
// settings.json. runpilot запускает кодера с явными аргументами
// (CoderArgsFromEnv: --auth-type openai, --model <alias>, --openai-base-url
// <шлюз>/s/<sid>/v1) и окружением (OPENAI_API_KEY, QWEN_CODE_API_TIMEOUT_MS).
// Аргументы имеют высший приоритет над settings.json:
//   - модель: --model > settings.model.name > env OPENAI_MODEL;
//   - auth: --auth-type > security.auth.selectedType.
// Поэтому settings.json НЕ правится: ни selectedType, ни contextWindowSize,
// ни modelProviders. Оператор управляет ~/.qwen/settings.json сам (в т.ч.
// окно контекста: model.generationConfig.contextWindowSize для Runtime Model).
//
// Единственный опасный случай — КОЛЛИЗИЯ: id записи modelProviders ==
// model_alias. Тогда alias становится Provider Model (sealed) и qwen берёт
// baseUrl из записи провайдера в обход шлюза — даже при явном --model и
// --openai-base-url. Узел записи не чинит и не удаляет (данные оператора);
// предпроверка PreCheckCoder отклоняет создание сессии (409) при коллизии.
// Окно контекста RunPilot не пишет — его оператор ставит в своём
// settings.json (для Runtime Model действует top-level
// model.generationConfig.contextWindowSize).

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// qwenSettingsPath — ~/.qwen/settings.json (пользовательский).
func qwenSettingsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".qwen", "settings.json"), nil
}

// projectSettingsPath — <dir>/.qwen/settings.json (проектный).
func projectSettingsPath(dir string) string {
	return filepath.Join(dir, ".qwen", "settings.json")
}

// readSettingsDoc — читает settings.json по пути; (nil, nil) — файла нет.
func readSettingsDoc(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("%s: не JSON: %w", path, err)
	}
	return doc, nil
}

// providerIDCollision — есть ли в modelProviders запись с id == modelAlias.
// Такая запись превращает текущую (шлюзовую) модель в Provider Model:
// qwen берёт baseUrl из записи, а не из --openai-base-url — сессия идёт
// мимо шлюза. Возвращает список описаний коллизий ("" — нет).
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
				out = append(out, fmt.Sprintf("modelProviders.%s: id %q", prov, id))
			}
		}
	}
	return out
}

// settingsCollision — коллизия model_alias с id modelProviders в
// пользовательском И проектном settings.json (W13 этап 7: предпроверка
// смотрит оба файла). Возвращает описания ("" — нет).
func settingsCollision(modelAlias, dir string) []string {
	var out []string
	if userPath, err := qwenSettingsPath(); err == nil {
		if doc, err := readSettingsDoc(userPath); err != nil {
			out = append(out, userPath+": "+err.Error())
		} else if doc != nil {
			for _, c := range providerIDCollision(doc, modelAlias) {
				out = append(out, userPath+": "+c)
			}
		}
	}
	if dir != "" {
		projPath := projectSettingsPath(dir)
		if doc, err := readSettingsDoc(projPath); err != nil {
			out = append(out, projPath+": "+err.Error())
		} else if doc != nil {
			for _, c := range providerIDCollision(doc, modelAlias) {
				out = append(out, projPath+": "+c)
			}
		}
	}
	return out
}

// qwenSupportFlags — аргументы/env, которые обязан поддерживать
// установленный qwen для маршрута через аргументы (W13 этап 7).
var qwenSupportFlags = []string{"--auth-type", "--openai-base-url"}

// qwenSupportCache — результат проверки поддержки qwen (однократно):
// qwen-версия не меняется в ходе работы узла.
var (
	qwenSupportMu    sync.Mutex
	qwenSupportOK    bool
	qwenSupportChecked bool
)

// checkQwenSupport — установленный qwen поддерживает --auth-type,
// --openai-base-url (и QWEN_CODE_API_TIMEOUT_MS — env, проверяем флаг
// --auth-type и --openai-base-url в --help). Результат кэшируется.
// qwenCmd — путь к qwen ("" — qwen из PATH).
func checkQwenSupport(qwenCmd string) error {
	qwenSupportMu.Lock()
	defer qwenSupportMu.Unlock()
	if qwenSupportChecked {
		if qwenSupportOK {
			return nil
		}
		return fmt.Errorf("установленный qwen не поддерживает требуемые аргументы")
	}
	if qwenCmd == "" {
		qwenCmd = "qwen"
	}
	out, err := exec.Command(qwenCmd, "--help").Output()
	if err != nil {
		qwenSupportChecked = true
		return fmt.Errorf("qwen --help: %w", err)
	}
	help := string(out)
	var missing []string
	for _, f := range qwenSupportFlags {
		if !strings.Contains(help, f) {
			missing = append(missing, f)
		}
	}
	qwenSupportChecked = true
	if len(missing) > 0 {
		qwenSupportOK = false
		return fmt.Errorf("установленный qwen не поддерживает: %s", strings.Join(missing, ", "))
	}
	qwenSupportOK = true
	return nil
}

// PreCheckCoder — предпроверка на узле ПЕРЕД созданием сессии (W13 этап 7):
// только проверяет, ничего не правит. Возвращает ошибку (→ 409) при:
//   - model_alias совпадает с id modelProviders в пользовательском/проектном
//     settings.json (коллизия → кодер пойдёт на прямой baseUrl в обход шлюза);
//   - установленный qwen не поддерживает --auth-type / --openai-base-url.
//
// alias — model_alias (OPENAI_MODEL); dir — каталог проекта; qwenCmd — путь
// к qwen ("" — из PATH).
func (n *Node) PreCheckCoder(alias, dir, qwenCmd string) error {
	if alias == "" {
		return fmt.Errorf("пустой model_alias")
	}
	if coll := settingsCollision(alias, dir); len(coll) > 0 {
		return fmt.Errorf("model_alias %q совпадает с id modelProviders — кодер пойдёт в обход шлюза: %s",
			alias, strings.Join(coll, "; "))
	}
	if err := checkQwenSupport(qwenCmd); err != nil {
		return err
	}
	return nil
}
