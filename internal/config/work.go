// Рабочие настройки (v2 раздел 4): отражение по yaml-путям.
//
// Единственный источник значений по умолчанию — defaults.go. Этот файл не
// содержит значений: только механика чтения/записи ключей по пути и
// перечисление рабочих листов Config (для схемы и миграции).
package config

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// isNonWorking — лист не является рабочей настройкой: bootstrap-ключ
// (раздел 4.1) или коллекция серверов (таблица server, раздел 3.4).
func isNonWorking(path string) bool {
	if strings.HasPrefix(path, "coordinator") || strings.HasPrefix(path, "client") {
		return true
	}
	switch path {
	case "servers",
		"web.bind", "web.port", "web.public_url",
		"web.probe_timeout_sec", "web.node_op_timeout_sec", "web.update_timeout_sec",
		"node.host", "node.tmux_sockets", "node.gpu", "node.gpu_server",
		"notify.telegram.bot_token_env":
		return true
	}
	return false
}

// walkLeaves — рекурсивно собирает yaml-пути листов структуры (скаляры и
// списки). Структурные узлы не выдаются, только листья.
func walkLeaves(t reflect.Type, prefix string, out *[]string) {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		if tag == "-" {
			continue
		}
		path := tag
		if prefix != "" {
			path = prefix + "." + tag
		}
		ft := f.Type
		for ft.Kind() == reflect.Ptr {
			ft = ft.Elem()
		}
		if ft.Kind() == reflect.Slice {
			*out = append(*out, path)
			continue
		}
		if ft.Kind() == reflect.Struct {
			walkLeaves(ft, path, out)
			continue
		}
		*out = append(*out, path)
	}
}

// WorkingLeafPaths — все рабочие листы Config (раздел 4.2): листья
// структуры минус bootstrap-ключи и коллекция серверов. Авторитетный
// список рабочих ключей для сверки со схемой (CONTROL 6).
func WorkingLeafPaths() []string {
	var all []string
	walkLeaves(reflect.TypeOf(Config{}), "", &all)
	var out []string
	for _, p := range all {
		if !isNonWorking(p) {
			out = append(out, p)
		}
	}
	return out
}

// fieldByTag — поле структуры по yaml-тегу.
func fieldByTag(st reflect.Type, tag string) (reflect.StructField, bool) {
	for i := 0; i < st.NumField(); i++ {
		f := st.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		if name == tag {
			return f, true
		}
	}
	return reflect.StructField{}, false
}

// walkFieldValue — доходит до значения поля по точечному пути.
// Возвращает адресуемый reflect.Value листа.
func walkFieldValue(v reflect.Value, parts []string) (reflect.Value, error) {
	for i, part := range parts {
		if v.Kind() != reflect.Struct {
			return reflect.Value{}, fmt.Errorf("config: %s: сегмент %d не struct", strings.Join(parts, "."), i)
		}
		f, ok := fieldByTag(v.Type(), part)
		if !ok {
			return reflect.Value{}, fmt.Errorf("config: путь %s: поле %q не найдено", strings.Join(parts, "."), part)
		}
		v = v.FieldByName(f.Name)
	}
	if !v.CanAddr() {
		return reflect.Value{}, fmt.Errorf("config: %s: поле не адресуемо", strings.Join(parts, "."))
	}
	return v, nil
}

// GetField читает значение рабочего ключа по точечному пути.
func (c *Config) GetField(path string) (any, error) {
	parts := strings.Split(path, ".")
	v, err := walkFieldValue(reflect.ValueOf(c).Elem(), parts)
	if err != nil {
		return nil, err
	}
	return v.Interface(), nil
}

// SetFieldJSON пишет значение рабочего ключа по точечному пути из JSON.
func (c *Config) SetFieldJSON(path string, data []byte) error {
	parts := strings.Split(path, ".")
	v, err := walkFieldValue(reflect.ValueOf(c).Elem(), parts)
	if err != nil {
		return err
	}
	// Разбор в адресable значение нужного типа: числа → int, строки →
	// string, булевы → bool, списки → []T.
	target := reflect.New(v.Type()).Interface()
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("config: %s: разбор значения: %w", path, err)
	}
	v.Set(reflect.ValueOf(target).Elem())
	return nil
}

// WorkingSnapshot — все рабочие настройки конфига как путь→JSON (полный
// снимок: все рабочие листья, CONTROL 4/5).
func (c *Config) WorkingSnapshot() (map[string]json.RawMessage, error) {
	out := make(map[string]json.RawMessage)
	for _, p := range WorkingLeafPaths() {
		v, err := c.GetField(p)
		if err != nil {
			return nil, err
		}
		data, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("config: %s: сериализация: %w", p, err)
		}
		out[p] = data
	}
	return out, nil
}

// ApplyWorking применяет рабочий снимок (путь→JSON) поверх текущих значений
// конфига. Ключей в конфиге, которых нет в снимке, не трогает.
func (c *Config) ApplyWorking(snap map[string]json.RawMessage) error {
	for p, data := range snap {
		if err := c.SetFieldJSON(p, data); err != nil {
			return err
		}
	}
	return nil
}

