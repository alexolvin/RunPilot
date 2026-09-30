package api

// Настройки (v2 раздел 4): API-проверки GET/PATCH, схемы, ревизий, отката,
// экспорта/импорта и валидации (раздел 4.5: 400 {code, problems[]}).

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// doRaw — запрос с произвольным телом; возвращает статус и сырое тело.
func doRaw(t *testing.T, method, url string, body []byte) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	out, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Body.Close()
	raw, _ := io.ReadAll(out.Body)
	return out.StatusCode, raw
}

// getSettings — текущие рабочие настройки (GET /api/v1/settings).
func getSettings(t *testing.T, ts *httptest.Server) map[string]any {
	t.Helper()
	status, raw := doRaw(t, "GET", ts.URL+"/api/v1/settings", nil)
	if status != http.StatusOK {
		t.Fatalf("GET settings: %d %s", status, raw)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("GET settings: не JSON: %v", err)
	}
	return m
}

// patchSettings — PATCH /api/v1/settings {path: value}.
func patchSettings(t *testing.T, ts *httptest.Server, changes map[string]any) (int, []byte) {
	t.Helper()
	data, _ := json.Marshal(changes)
	return doRaw(t, "PATCH", ts.URL+"/api/v1/settings", data)
}

// valBody — тело 400 валидации (раздел 4.5).
type valBody struct {
	Code     string `json:"code"`
	Problems []struct {
		Path    string `json:"path"`
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"problems"`
}

// expectValidation400 — 400 VALIDATION_ERROR с проблемой (path, code).
func expectValidation400(t *testing.T, raw []byte, path, code string) {
	t.Helper()
	var v valBody
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("400 не JSON: %v: %s", err, raw)
	}
	if v.Code != "VALIDATION_ERROR" {
		t.Fatalf("code = %q, хочу VALIDATION_ERROR: %s", v.Code, raw)
	}
	for _, p := range v.Problems {
		if p.Path == path && p.Code == code {
			if p.Message == "" {
				t.Fatalf("проблема %s/%s без сообщения", path, code)
			}
			return
		}
	}
	t.Fatalf("нет проблемы %s/%s в списке: %s", path, code, raw)
}

// 4.5: GET /api/v1/settings — все 93 рабочих ключа с действующими значениями.
func TestSettingsGetReturnsWorkingValues(t *testing.T) {
	ts, st, _ := testServer(t)
	// Пустая БД → значения из defaults.
	m := getSettings(t, ts)
	if len(m) != 93 {
		t.Fatalf("ключей = %d, хочу 93", len(m))
	}
	if v, ok := m["scheduler.aging_sec"].(float64); !ok || int(v) != 900 {
		t.Fatalf("scheduler.aging_sec = %v (defaults 900)", m["scheduler.aging_sec"])
	}
	// Изменение в БД → отражается в GET.
	if status, raw := patchSettings(t, ts, map[string]any{"scheduler.aging_sec": 1234}); status != http.StatusOK {
		t.Fatalf("PATCH: %d %s", status, raw)
	}
	m = getSettings(t, ts)
	if v, ok := m["scheduler.aging_sec"].(float64); !ok || int(v) != 1234 {
		t.Fatalf("scheduler.aging_sec = %v после PATCH, хочу 1234", m["scheduler.aging_sec"])
	}
	// Ревизия создана (source=web).
	revs, err := st.ListRevisions(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(revs) != 1 || revs[0].Source != "web" {
		t.Fatalf("ревизии = %+v, хочу одну с source=web", revs)
	}
}

// 4.2: GET /api/v1/settings/schema — все рабочие ключи с типом, группой,
// подписью и моментом применения.
func TestSettingsSchemaEndpoint(t *testing.T) {
	ts, _, _ := testServer(t)
	status, raw := doRaw(t, "GET", ts.URL+"/api/v1/settings/schema", nil)
	if status != http.StatusOK {
		t.Fatalf("GET schema: %d %s", status, raw)
	}
	var fields []map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("schema не JSON: %v", err)
	}
	if len(fields) != 93 {
		t.Fatalf("полей схемы = %d, хочу 93", len(fields))
	}
	for _, f := range fields {
		if f["path"] == nil || f["type"] == nil || f["group"] == nil ||
			f["label"] == nil || f["apply_when"] == nil {
			t.Fatalf("поле без обязательной метадаты: %v", f)
		}
	}
}

// 4.5: PATCH с неверными значениями → 400 со списком проблем (правила 1
// и 6: тип, диапазон, неизвестный ключ, формат строки, перекрёстные).
func TestSettingsPatchValidation(t *testing.T) {
	ts, st, _ := testServer(t)
	cases := []struct {
		name    string
		changes map[string]any
		path    string
		code    string
	}{
		{"неизвестный ключ (bootstrap)", map[string]any{"web.public_url": "http://x"}, "web.public_url", "UNKNOWN_KEY"},
		{"тип: int ← строка", map[string]any{"scheduler.aging_sec": "abc"}, "scheduler.aging_sec", "TYPE_MISMATCH"},
		{"диапазон: int < min", map[string]any{"scheduler.aging_sec": 0}, "scheduler.aging_sec", "OUT_OF_RANGE"},
		{"тип: string ← число", map[string]any{"backup.daily_at_utc": 5}, "backup.daily_at_utc", "TYPE_MISMATCH"},
		{"тип: bool ← строка", map[string]any{"external.detect": "yes"}, "external.detect", "TYPE_MISMATCH"},
		{"тип: list ← строка", map[string]any{"notify.telegram.chat_ids": "abc"}, "notify.telegram.chat_ids", "TYPE_MISMATCH"},
		{"перекрёстное: done_quiet >= start_confirm", map[string]any{"turn.done_quiet_sec": 100}, "turn.done_quiet_sec", "CROSS_FIELD"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			status, raw := patchSettings(t, ts, c.changes)
			if status != http.StatusBadRequest {
				t.Fatalf("PATCH: %d %s, хочу 400", status, raw)
			}
			expectValidation400(t, raw, c.path, c.code)
		})
	}
	// Ничего не сохранено: БД пуста, ревизий нет.
	revs, err := st.ListRevisions(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(revs) != 0 {
		t.Fatalf("отклонённые PATCH создали ревизии: %+v", revs)
	}
}

// 4.6: откат к ревизии — новое значение равнo снимку целевой ревизии.
func TestSettingsRevert(t *testing.T) {
	ts, st, _ := testServer(t)
	if status, raw := patchSettings(t, ts, map[string]any{"scheduler.aging_sec": 111}); status != http.StatusOK {
		t.Fatalf("PATCH A: %d %s", status, raw)
	}
	if status, raw := patchSettings(t, ts, map[string]any{"scheduler.aging_sec": 222}); status != http.StatusOK {
		t.Fatalf("PATCH B: %d %s", status, raw)
	}
	revs, err := st.ListRevisions(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(revs) != 2 {
		t.Fatalf("ревизий = %d, хочу 2", len(revs))
	}
	// Ревизии по убыванию id: [B(2), A(1)]. Откат к A.
	status, raw := doRaw(t, "POST", ts.URL+"/api/v1/settings/revert", []byte(`{"id":1}`))
	if status != http.StatusOK {
		t.Fatalf("revert: %d %s", status, raw)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("revert: не JSON: %v", err)
	}
	if int(out["revision_id"].(float64)) != 3 {
		t.Fatalf("новая ревизия = %v, хочу 3", out["revision_id"])
	}
	// Значение — как в ревизии A.
	m := getSettings(t, ts)
	if v, ok := m["scheduler.aging_sec"].(float64); !ok || int(v) != 111 {
		t.Fatalf("aging_sec = %v после revert к A, хочу 111", m["scheduler.aging_sec"])
	}
	// Ревизия source=revert.
	revs, _ = st.ListRevisions(10)
	if len(revs) != 3 || revs[0].Source != "revert" {
		t.Fatalf("после revert ревизии = %+v, хочу сверху source=revert", revs)
	}
	// Неревизии → 404.
	status, _ = doRaw(t, "POST", ts.URL+"/api/v1/settings/revert", []byte(`{"id":999}`))
	if status != http.StatusNotFound {
		t.Fatalf("revert к несуществующей: %d, хочу 404", status)
	}
}


// 4.7: экспорт → импорт: dry_run показывает разницу и не меняет состояние,
// применение — одна ревизия source=import.
func TestSettingsImport(t *testing.T) {
	ts, st, _ := testServer(t)
	if status, raw := patchSettings(t, ts, map[string]any{"scheduler.aging_sec": 111}); status != http.StatusOK {
		t.Fatalf("PATCH: %d %s", status, raw)
	}
	// Экспорт текущего состояния.
	status, export := doRaw(t, "GET", ts.URL+"/api/v1/settings/export", nil)
	if status != http.StatusOK {
		t.Fatalf("export: %d %s", status, export)
	}
	// dry_run того же состояния: пустая разница, ничего не применено.
	status, raw := doRaw(t, "POST", ts.URL+"/api/v1/settings/import?dry_run=true", export)
	if status != http.StatusOK {
		t.Fatalf("import dry_run: %d %s", status, raw)
	}
	var res struct {
		Diff     []any `json:"diff"`
		Applied  bool  `json:"applied"`
		RevID    int64 `json:"revision_id"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("import: не JSON: %v", err)
	}
	if len(res.Diff) != 0 || res.Applied {
		t.Fatalf("dry_run: diff=%d applied=%v, хочу пусто/нет", len(res.Diff), res.Applied)
	}
	// Меняем состояние, dry_run старого экспорта показывает разницу.
	if status, raw := patchSettings(t, ts, map[string]any{"scheduler.aging_sec": 333}); status != http.StatusOK {
		t.Fatalf("PATCH 333: %d %s", status, raw)
	}
	status, raw = doRaw(t, "POST", ts.URL+"/api/v1/settings/import?dry_run=true", export)
	if status != http.StatusOK {
		t.Fatalf("import dry_run 2: %d %s", status, raw)
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Diff) != 1 || res.Applied {
		t.Fatalf("dry_run после смены: diff=%d applied=%v, хочу 1/нет", len(res.Diff), res.Applied)
	}
	// Состояние не изменилось.
	if v, _ := getSettings(t, ts)["scheduler.aging_sec"].(float64); int(v) != 333 {
		t.Fatalf("dry_run изменил состояние: aging_sec=%v", v)
	}
	// Применение — одна ревизия source=import, значение 111.
	status, raw = doRaw(t, "POST", ts.URL+"/api/v1/settings/import", export)
	if status != http.StatusOK {
		t.Fatalf("import apply: %d %s", status, raw)
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatal(err)
	}
	if !res.Applied || res.RevID == 0 {
		t.Fatalf("apply: applied=%v rev_id=%d", res.Applied, res.RevID)
	}
	revs, _ := st.ListRevisions(10)
	if len(revs) == 0 || revs[0].Source != "import" {
		t.Fatalf("свежая ревизия = %+v, хочу source=import", revs)
	}
	if v, _ := getSettings(t, ts)["scheduler.aging_sec"].(float64); int(v) != 111 {
		t.Fatalf("aging_sec = %v после import, хочу 111", v)
	}
}

// 4.5: импорт невалидного экспорта отклоняется (400 IMPORT_INVALID).
func TestSettingsImportInvalid(t *testing.T) {
	ts, st, _ := testServer(t)
	invalid := []byte("settings:\n  scheduler.aging_sec: 0\n")
	status, raw := doRaw(t, "POST", ts.URL+"/api/v1/settings/import", invalid)
	if status != http.StatusBadRequest {
		t.Fatalf("import invalid: %d %s, хочу 400", status, raw)
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(raw, &body); err != nil || body.Code != "IMPORT_INVALID" {
		t.Fatalf("code = %+v (%s), хочу IMPORT_INVALID", body, raw)
	}
	// Ничего не применено.
	revs, _ := st.ListRevisions(10)
	if len(revs) != 0 {
		t.Fatalf("невалидный импорт создал ревизии: %+v", revs)
	}
}
