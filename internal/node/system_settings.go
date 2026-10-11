package node

// system-settings.json per-сессии (W15 этап 3): узел объявляет кодеру
// входные модальности пула (пересечение input_modalities всех серверов) и
// окно контекста (min по серверам), чтобы qwen разрешал read_file медиа
// (image_url/video_url) и объявлял реальный контекст, а не гадание по имени.
//
// Файл — ТОЛЬКО override: model.generationConfig.modalities + contextWindowSize.
// Базовые унаследованные настройки (~/.qwen/settings.json) — через
// QWEN_CODE_SYSTEM_DEFAULTS_PATH; override — через QWEN_CODE_SYSTEM_SETTINGS_PATH
// (высший приоритет; qwen мержит override поверх defaults). Файл mode 0600,
// удаляется при GONE.

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// systemSettingsPath — <state_dir>/qwen/<sid>/system-settings.json.
func (n *Node) systemSettingsPath(sid string) (string, error) {
	dir, err := n.stateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "qwen", sid, "system-settings.json"), nil
}

// stateDir — каталог состояния узла (тильда раскрыта).
func (n *Node) stateDir() (string, error) {
	raw := n.cfg.Node.StateDir
	if raw == "" {
		return "", os.ErrInvalid
	}
	return expandTilde(raw), nil
}

// modalitiesObject — ["image","video"] → {"image":bool,"video":bool}
// (опущенная модальность = false).
func modalitiesObject(modalities []string) map[string]bool {
	out := map[string]bool{"image": false, "video": false}
	for _, m := range modalities {
		if m == "image" || m == "video" {
			out[m] = true
		}
	}
	return out
}

// systemSettingsDoc — override-документ: model.generationConfig.modalities +
// contextWindowSize (0 — не писать, оставить наследованный).
func systemSettingsDoc(modalities []string, contextWindow int) map[string]any {
	gc := map[string]any{"modalities": modalitiesObject(modalities)}
	if contextWindow > 0 {
		gc["contextWindowSize"] = contextWindow
	}
	return map[string]any{"model": map[string]any{"generationConfig": gc}}
}

// writeSystemSettings — пишет per-сессии system-settings.json (mode 0600).
// Возвращает путь файла.
func (n *Node) writeSystemSettings(sid string, modalities []string, contextWindow int) (string, error) {
	path, err := n.systemSettingsPath(sid)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), systemSettingsDirPerm); err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(systemSettingsDoc(modalities, contextWindow), "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, data, systemSettingsFilePerm); err != nil {
		return "", err
	}
	return path, nil
}

// deleteSystemSettings — удалить per-сессии system-settings.json (GONE).
// Удаляет каталог сессии целиком (только system-settings.json внутри).
func (n *Node) deleteSystemSettings(sid string) {
	if path, err := n.systemSettingsPath(sid); err == nil {
		_ = os.RemoveAll(filepath.Dir(path))
	}
}

// applySystemSettingsEnv — пишет per-сессии system-settings.json и ставит в
// env кодера QWEN_CODE_SYSTEM_SETTINGS_PATH (override) + QWEN_CODE_SYSTEM_
// DEFAULTS_PATH (унаследованные). Ошибка записи не блокирует spawn (кодер
// работает без override — как раньше, без медиа).
func (n *Node) applySystemSettingsEnv(env map[string]string, sid string, modalities []string, contextWindow int) {
	if env == nil {
		return
	}
	path, err := n.writeSystemSettings(sid, modalities, contextWindow)
	if err != nil {
		return
	}
	env["QWEN_CODE_SYSTEM_SETTINGS_PATH"] = path
	if d := systemDefaultsPath(); d != "" {
		env["QWEN_CODE_SYSTEM_DEFAULTS_PATH"] = d
	}
}

// systemDefaultsPath — унаследованные системные настройки
// (~/.qwen/settings.json); "" — файла нет (qwen берёт платформенный дефолт).
func systemDefaultsPath() string {
	p, err := qwenSettingsPath()
	if err != nil {
		return ""
	}
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}
