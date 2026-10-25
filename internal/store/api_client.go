package store

// API-клиенты шлюза (безсессионные OpenAI-запросы в общую очередь).
//
// Ключ показывается один раз при создании/перевыпуске и нигде не хранится:
// в БД только key_hash (SHA-256). Ключ не попадает в логи и события.

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"runpilot/internal/model"
)

// ApiClient — запись api_client.
type ApiClient struct {
	Name           string
	KeyHash        string
	Class          model.QueueClass
	Priority       int
	Enabled        bool
	AllowedServers []string
	MaxHoldSec     int
	StickyIdleSec  int
	AgingSec       int
	// AllowStream — stream:true для клиента (учёт токенов — из usage-чанка;
	// см. 026_api_client_allow_stream).
	AllowStream bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// NewAPIKey — сгенерировать ключ клиента: "rp_" + 64 hex.
func NewAPIKey() (string, error) {
	buf := make([]byte, apiClientKeyBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("api_client: ключ: %w", err)
	}
	return "rp_" + hex.EncodeToString(buf), nil
}

// APIKeyHash — SHA-256 ключа (hex).
func APIKeyHash(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// CreateApiClient — новый клиент. Возвращает ключ (показывается один раз).
func (s *Store) CreateApiClient(c ApiClient) (string, error) {
	key, err := NewAPIKey()
	if err != nil {
		return "", err
	}
	c.KeyHash = APIKeyHash(key)
	now := time.Now().UTC()
	c.CreatedAt = now
	c.UpdatedAt = now
	if c.Class == 0 {
		c.Class = model.ClassBatch
	}
	if err := s.DoWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(`
INSERT INTO api_client
	(name, key_hash, class, priority, enabled, allowed_servers,
	 max_hold_sec, sticky_idle_sec, aging_sec, allow_stream, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			c.Name, c.KeyHash, int(c.Class), c.Priority, boolInt(c.Enabled),
			strings.Join(c.AllowedServers, ","), c.MaxHoldSec, c.StickyIdleSec,
			c.AgingSec, boolInt(c.AllowStream), ts(c.CreatedAt), ts(c.UpdatedAt))
		return err
	}); err != nil {
		return "", err
	}
	return key, nil
}

// GetApiClient — по имени (ErrNotFound, если нет).
func (s *Store) GetApiClient(name string) (ApiClient, error) {
	var c ApiClient
	var enabled int
	var servers string
	var allowStream int
	var created, updated string
	err := s.db.QueryRow(`
SELECT name, key_hash, class, priority, enabled, allowed_servers,
	max_hold_sec, sticky_idle_sec, aging_sec, allow_stream, created_at, updated_at
FROM api_client WHERE name = ?`, name).
		Scan(&c.Name, &c.KeyHash, &c.Class, &c.Priority, &enabled, &servers,
			&c.MaxHoldSec, &c.StickyIdleSec, &c.AgingSec, &allowStream, &created, &updated)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ApiClient{}, ErrNotFound
		}
		return ApiClient{}, err
	}
	c.Class = model.QueueClass(int(c.Class))
	c.Enabled = enabled != 0
	c.AllowedServers = splitCSV(servers)
	c.AllowStream = allowStream != 0
	c.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	c.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return c, nil
}

// GetApiClientByKey — по ключу (для аутентификации запроса). ErrNotFound.
func (s *Store) GetApiClientByKey(key string) (ApiClient, error) {
	if key == "" {
		return ApiClient{}, ErrNotFound
	}
	hash := APIKeyHash(key)
	var c ApiClient
	var enabled int
	var servers string
	var allowStream int
	var created, updated string
	err := s.db.QueryRow(`
SELECT name, key_hash, class, priority, enabled, allowed_servers,
	max_hold_sec, sticky_idle_sec, aging_sec, allow_stream, created_at, updated_at
FROM api_client WHERE key_hash = ?`, hash).
		Scan(&c.Name, &c.KeyHash, &c.Class, &c.Priority, &enabled, &servers,
			&c.MaxHoldSec, &c.StickyIdleSec, &c.AgingSec, &allowStream, &created, &updated)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ApiClient{}, ErrNotFound
		}
		return ApiClient{}, err
	}
	c.Class = model.QueueClass(int(c.Class))
	c.Enabled = enabled != 0
	c.AllowedServers = splitCSV(servers)
	c.AllowStream = allowStream != 0
	c.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	c.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return c, nil
}

// ListApiClients — все клиенты (для /state и веба).
func (s *Store) ListApiClients() ([]ApiClient, error) {
	rows, err := s.db.Query(`
SELECT name, key_hash, class, priority, enabled, allowed_servers,
	max_hold_sec, sticky_idle_sec, aging_sec, allow_stream, created_at, updated_at
FROM api_client ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ApiClient
	for rows.Next() {
		var c ApiClient
		var enabled int
		var servers string
		var allowStream int
		var created, updated string
		var class int
		if err := rows.Scan(&c.Name, &c.KeyHash, &class, &c.Priority, &enabled,
			&servers, &c.MaxHoldSec, &c.StickyIdleSec, &c.AgingSec, &allowStream,
			&created, &updated); err != nil {
			return nil, err
		}
		c.Class = model.QueueClass(class)
		c.Enabled = enabled != 0
		c.AllowedServers = splitCSV(servers)
		c.AllowStream = allowStream != 0
		c.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		c.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
		out = append(out, c)
	}
	return out, rows.Err()
}

// SetApiClientEnabled — включить/отключить клиента.
func (s *Store) SetApiClientEnabled(name string, enabled bool) error {
	return s.DoWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE api_client SET enabled = ?, updated_at = ? WHERE name = ?`,
			boolInt(enabled), ts(time.Now().UTC()), name)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// SetApiClient — обновить настройки клиента (приоритет, класс, allowed_servers,
// таймауты, allow_stream). Ключ и enabled не меняются (у них свои эндпоинты:
// reissue / enable|disable). Возвращает обновлённого клиента.
func (s *Store) SetApiClient(c ApiClient) (ApiClient, error) {
	now := time.Now().UTC()
	if err := s.DoWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`
UPDATE api_client
	SET class = ?, priority = ?, allowed_servers = ?,
	    max_hold_sec = ?, sticky_idle_sec = ?, aging_sec = ?, allow_stream = ?,
	    updated_at = ?
	WHERE name = ?`,
			int(c.Class), c.Priority, strings.Join(c.AllowedServers, ","),
			c.MaxHoldSec, c.StickyIdleSec, c.AgingSec, boolInt(c.AllowStream),
			ts(now), c.Name)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return ErrNotFound
		}
		return nil
	}); err != nil {
		return ApiClient{}, err
	}
	return s.GetApiClient(c.Name)
}

// DeleteApiClient — удалить клиента и его псевдо-сессию (api:<name>).
// Псевдо-сессию снимаем с очереди/аренды и ставим GONE, затем удаляем запись
// сессии и клиента. Idempotent на клиенте: ErrNotFound, если клиента нет.
func (s *Store) DeleteApiClient(name string) error {
	sid := APISessionSID(name)
	if err := s.DoWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`DELETE FROM api_client WHERE name = ?`, name)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return ErrNotFound
		}
		return nil
	}); err != nil {
		return err
	}
	// Псевдо-сессия: снять с очереди и удалить запись (не GONE — ставим GONE).
	if rec, err := s.GetSession(sid); err == nil {
		now := time.Now().UTC()
		_ = s.QueueDelete(sid)
		_ = s.SetSessionState(sid, model.SessionGone, "", now)
		_ = s.DeleteSession(sid)
		_ = rec
	}
	return nil
}

// ReissueAPIKey — перевыпустить ключ (новый ключ показывается один раз).
func (s *Store) ReissueAPIKey(name string) (string, error) {
	key, err := NewAPIKey()
	if err != nil {
		return "", err
	}
	hash := APIKeyHash(key)
	if err := s.DoWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE api_client SET key_hash = ?, updated_at = ? WHERE name = ?`,
			hash, ts(time.Now().UTC()), name)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return ErrNotFound
		}
		return nil
	}); err != nil {
		return "", err
	}
	return key, nil
}

// APISessionSID — sid псевдо-сессии API-клиента.
func APISessionSID(name string) string { return "api:" + name }

// EnsureAPISession — псевдо-сессия клиента (sid=api:<name>, Kind=API, IDLE).
// Идемпотентно: существующая сессия не перезаписывается. Видна в очереди и
// в списке сессий; закрыть её нельзя (Kind=API — операторские действия
// сессии не применяются).
func (s *Store) EnsureAPISession(name string, class model.QueueClass) error {
	sid := APISessionSID(name)
	if _, err := s.GetSession(sid); err == nil {
		return nil // уже есть
	}
	now := time.Now().UTC()
	r := SessionRecord{
		SID:            sid,
		Name:           name,
		Kind:           model.KindAPI,
		State:          model.SessionIdle,
		StateChangedAt: now,
		Class:          class,
		CreatedAt:      now,
	}
	return s.CreateSession(r)
}

// splitCSV — "a,b" → ["a","b"]; пусто → nil.
func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
