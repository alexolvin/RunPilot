package config

import (
	"fmt"
	"sort"
	"testing"
)

// CONTROL 6: схема покрывает 100% рабочих ключей defaults.go.
// Сравнивает список путей схемы с авторитетным списком рабочих листьев
// WorkingLeafPaths() (производным от структуры Config / defaults.go).
func TestSchemaCoversAllWorkingKeys(t *testing.T) {
	want := WorkingLeafPaths()
	got := SchemaPaths()

	wantSet := map[string]bool{}
	for _, p := range want {
		wantSet[p] = true
	}
	gotSet := map[string]bool{}
	for _, p := range got {
		gotSet[p] = true
	}

	var missing, extra []string
	for _, p := range want {
		if !gotSet[p] {
			missing = append(missing, p)
		}
	}
	for _, p := range got {
		if !wantSet[p] {
			extra = append(extra, p)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 || len(extra) > 0 {
		t.Fatalf("схема не совпадает с рабочими ключами: отсутствуют %v, лишние %v",
			missing, extra)
	}
}

// Схема не содержит дубликатов путей (внутренний контроль целостности).
func TestSchemaNoDuplicatePaths(t *testing.T) {
	if err := validateSchemaIntegrity(); err != nil {
		t.Fatal(err)
	}
}

// У каждого поля схемы заполнены обязательные метаданные, а Default
// совпадает с DefaultValue из defaults.go (R2: единый источник).
func TestSchemaMetadataComplete(t *testing.T) {
	d := Defaults()
	for _, f := range Schema() {
		if f.Label == "" || f.Help == "" || f.Group == "" || f.ApplyWhen == "" || f.Type == "" {
			t.Fatalf("поле %s: неполные метаданные %+v", f.Path, f)
		}
		want, err := d.GetField(f.Path)
		if err != nil {
			t.Fatalf("поле %s: нет такого ключа в Config: %v", f.Path, err)
		}
		if !equalValue(f.Default, want) {
			t.Fatalf("поле %s: default %v != defaults.go %v", f.Path, f.Default, want)
		}
	}
}

func equalValue(a, b any) bool {
	return formatVal(a) == formatVal(b)
}

func formatVal(v any) string {
	switch x := v.(type) {
	case []string:
		s := make([]string, len(x))
		copy(s, x)
		sort.Strings(s)
		return "list:" + fmt.Sprint(s)
	default:
		return fmt.Sprintf("%T:%v", v, v)
	}
}
