package api

// Рабочие настройки (v2 раздел 4): GET/PATCH, схема, ревизии, откат,
// экспорт и импорт. Ошибки валидации (раздел 4.5) — 400 со списком
// {path, code, message}.

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"runpilot/internal/config"
	"runpilot/internal/store"
)

// valProblem — одна ошибка валидации (раздел 4.5).
type valProblem struct {
	Path    string `json:"path"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// validationError — тело 400: код + список проблем (раздел 4.5).
type validationError struct {
	Code     string       `json:"code"`
	Problems []valProblem `json:"problems"`
}

func writeValidationError(w http.ResponseWriter, problems []valProblem) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	json.NewEncoder(w).Encode(validationError{Code: "VALIDATION_ERROR", Problems: problems})
}

// workingView — текущие рабочие настройки как путь→значение.
func (s *Server) workingView() (map[string]any, error) {
	cfg := config.Defaults()
	if err := s.store.LoadWorkingInto(cfg); err != nil {
		return nil, err
	}
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
	return m, nil
}

// settingsVersion — ETag рабочих настроек: id последней ревизии (O1).
func (s *Server) settingsVersion() string {
	id, err := s.store.LatestSettingsRevisionID()
	if err != nil {
		return "0"
	}
	return strconv.FormatInt(id, decimalBase)
}

// handleGetSettings — GET /api/v1/settings.
func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	m, err := s.workingView()
	if err != nil {
		httpError(w, http.StatusInternalServerError, "STORE", err.Error())
		return
	}
	// O1: ETag = id последней ревизии (основа If-Match для 409).
	w.Header().Set("ETag", `"`+s.settingsVersion()+`"`)
	json.NewEncoder(w).Encode(m)
}

// handleSettingsSchema — GET /api/v1/settings/schema.
func (s *Server) handleSettingsSchema(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(config.Schema())
}

// handlePatchSettings — PATCH /api/v1/settings {path: value, ...}.
func (s *Server) handlePatchSettings(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		httpError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	var changes map[string]any
	if err := json.Unmarshal(body, &changes); err != nil {
		httpError(w, http.StatusBadRequest, "BAD_REQUEST", "тело — объект {path: value}")
		return
	}
	if len(changes) == 0 {
		httpError(w, http.StatusBadRequest, "BAD_REQUEST", "нет изменений")
		return
	}

	// O1: оптимистичная блокировка — If-Match должен совпадать с текущей
	// версией (id последней ревизии). Не совпал → объект меняли в другом
	// окне → 409 VERSION_CONFLICT (веб подтянет свежие данные).
	if match := r.Header.Get("If-Match"); match != "" {
		if cur := s.settingsVersion(); match != cur && match != `"`+cur+`"` {
			httpError(w, http.StatusConflict, "VERSION_CONFLICT",
				"настройки изменены в другом окне: If-Match "+match+", текущая версия "+cur)
			return
		}
	}

	problems := validateWorkingChanges(changes, s)
	if len(problems) > 0 {
		writeValidationError(w, problems)
		return
	}

	// Применяем и сохраняем (новая ревизия, source=web).
	cfg := config.Defaults()
	if err := s.store.LoadWorkingInto(cfg); err != nil {
		httpError(w, http.StatusInternalServerError, "STORE", err.Error())
		return
	}
	for p, v := range changes {
		data, err := json.Marshal(v)
		if err != nil {
			httpError(w, http.StatusBadRequest, "BAD_REQUEST", "значение "+p)
			return
		}
		if err := cfg.SetFieldJSON(p, data); err != nil {
			httpError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
			return
		}
	}
	snap, err := cfg.WorkingSnapshot()
	if err != nil {
		httpError(w, http.StatusInternalServerError, "STORE", err.Error())
		return
	}
	if _, err := s.store.SaveWorkingSettings(snap, "operator", "web", s.clk.Now()); err != nil {
		httpError(w, http.StatusInternalServerError, "STORE", err.Error())
		return
	}
	// Возвращаем новое состояние.
	m, err := s.workingView()
	if err != nil {
		httpError(w, http.StatusInternalServerError, "STORE", err.Error())
		return
	}
	json.NewEncoder(w).Encode(m)
}

// validateWorkingChanges — проверка изменений (раздел 4.5): схема
// (тип/диапазон/перечень/неизвестный ключ) + перекрёстные правила.
func validateWorkingChanges(changes map[string]any, s *Server) []valProblem {
	var problems []valProblem

	// 1. Схема: тип, диапазон, перечень, неизвестный ключ.
	for p, v := range changes {
		f, ok := config.ByPath(p)
		if !ok {
			problems = append(problems, valProblem{Path: p, Code: "UNKNOWN_KEY",
				Message: "ключ не является рабочей настройкой"})
			continue
		}
		switch f.Type {
		case config.TypeInt:
			n, ok := v.(float64)
			if !ok {
				problems = append(problems, valProblem{Path: p, Code: "TYPE_MISMATCH",
					Message: "ожидается целое число"})
				continue
			}
			if f.Min != nil && int(n) < *f.Min {
				problems = append(problems, valProblem{Path: p, Code: "OUT_OF_RANGE",
					Message: "меньше минимума " + strconv.Itoa(*f.Min)})
			}
			if f.Max != nil && int(n) > *f.Max {
				problems = append(problems, valProblem{Path: p, Code: "OUT_OF_RANGE",
					Message: "больше максимума " + strconv.Itoa(*f.Max)})
			}
		case config.TypeString:
			if _, ok := v.(string); !ok {
				problems = append(problems, valProblem{Path: p, Code: "TYPE_MISMATCH",
					Message: "ожидается строка"})
			}
		case config.TypeBool:
			if _, ok := v.(bool); !ok {
				problems = append(problems, valProblem{Path: p, Code: "TYPE_MISMATCH",
					Message: "ожидается логическое значение"})
			}
		case config.TypeList:
			if _, ok := v.([]any); !ok {
				problems = append(problems, valProblem{Path: p, Code: "TYPE_MISMATCH",
					Message: "ожидается список"})
			}
		}
	}
	if len(problems) > 0 {
		return problems
	}

	// 2. Перекрёстные правила (раздел 4.5) на слиянии «текущее + изменения».
	cfg := config.Defaults()
	_ = s.store.LoadWorkingInto(cfg)
	for p, v := range changes {
		data, _ := json.Marshal(v)
		_ = cfg.SetFieldJSON(p, data)
	}
	if cfg.Turn.DoneQuietSec >= cfg.Dispatch.StartConfirmSec {
		problems = append(problems, valProblem{Path: "turn.done_quiet_sec",
			Code: "CROSS_FIELD", Message: "должен быть меньше dispatch.start_confirm_sec"})
	}
	if cfg.Turn.DoneStableSec >= cfg.Dispatch.StartConfirmSec {
		problems = append(problems, valProblem{Path: "turn.done_stable_sec",
			Code: "CROSS_FIELD", Message: "должен быть меньше dispatch.start_confirm_sec"})
	}
	return problems
}

// handleSettingsRevisions — GET /api/v1/settings/revisions.
func (s *Server) handleSettingsRevisions(w http.ResponseWriter, r *http.Request) {
	revs, err := s.store.ListRevisions(settingsRevisionsLimit)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "STORE", err.Error())
		return
	}
	type revView struct {
		ID     int64  `json:"id"`
		TS     string `json:"ts"`
		Author string `json:"author"`
		Source string `json:"source"`
		Diff   string `json:"diff"`
	}
	out := make([]revView, 0, len(revs))
	for _, r := range revs {
		out = append(out, revView{ID: r.ID, TS: r.TS.Format("2006-01-02T15:04:05Z07:00"),
			Author: r.Author, Source: r.Source, Diff: r.Diff})
	}
	json.NewEncoder(w).Encode(out)
}

// handleSettingsRevert — POST /api/v1/settings/revert {id}.
func (s *Server) handleSettingsRevert(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ID == 0 {
		httpError(w, http.StatusBadRequest, "BAD_REQUEST", "нужен id ревизии")
		return
	}
	newID, err := s.store.RevertToRevision(body.ID, "operator", s.clk.Now())
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			httpError(w, http.StatusNotFound, "NOT_FOUND", "нет такой ревизии")
			return
		}
		httpError(w, http.StatusInternalServerError, "STORE", err.Error())
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"revision_id": newID})
}

// handleSettingsExport — GET /api/v1/settings/export (YAML).
func (s *Server) handleSettingsExport(w http.ResponseWriter, r *http.Request) {
	data, err := s.store.ExportSettings()
	if err != nil {
		httpError(w, http.StatusInternalServerError, "STORE", err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/yaml")
	w.Write(data)
}

// handleSettingsImport — POST /api/v1/settings/import?dry_run=.
func (s *Server) handleSettingsImport(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		httpError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	dryRun := r.URL.Query().Get("dry_run") == "true"
	res, err := s.store.ImportSettings(body, dryRun, "operator", s.clk.Now())
	if err != nil {
		httpError(w, http.StatusBadRequest, "IMPORT_INVALID", err.Error())
		return
	}
	json.NewEncoder(w).Encode(res)
}
