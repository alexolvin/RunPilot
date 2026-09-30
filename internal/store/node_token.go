package store

// Токены узлов (раздел 14.2 v2). Узел загружает дистрибутив по Bearer-токену;
// отзыв при удалении узла делает повторную загрузку 401 (CONTROL 4).

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"
)

// NodeToken — токен узла для загрузки дистрибутива.
type NodeToken struct {
	Host      string
	Token     string
	CreatedAt time.Time
	RevokedAt time.Time
}

// Active — токен не отозван.
func (t NodeToken) Active() bool { return t.RevokedAt.IsZero() }

// NewNodeToken — сгенерировать токен узла (host, now).
func NewNodeToken(host string, now time.Time) NodeToken {
	buf := make([]byte, nodeTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand не может реально не сработать; фолбэк по времени.
		for i := range buf {
			buf[i] = byte(now.UnixNano() >> (i * bitsPerByte))
		}
	}
	return NodeToken{Host: host, Token: hex.EncodeToString(buf), CreatedAt: now}
}

// CreateNodeToken — вставить токен (существующий для host перезаписывается).
func (s *Store) CreateNodeToken(t NodeToken) error {
	return s.DoWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO node_token (host, token, created_at, revoked_at)
			VALUES (?, ?, ?, '')
			ON CONFLICT(host) DO UPDATE SET token=excluded.token,
			    created_at=excluded.created_at, revoked_at=''`,
			t.Host, t.Token, ts(t.CreatedAt))
		return err
	})
}

// GetNodeToken — токен по host (ErrNotFound, если нет).
func (s *Store) GetNodeToken(host string) (NodeToken, error) {
	var t NodeToken
	var created, revoked string
	err := s.db.QueryRow(`SELECT token, created_at, revoked_at FROM node_token WHERE host = ?`, host).
		Scan(&t.Token, &created, &revoked)
	if err != nil {
		if err == sql.ErrNoRows {
			return NodeToken{}, ErrNotFound
		}
		return NodeToken{}, err
	}
	t.Host = host
	t.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	if revoked != "" {
		if rt, perr := time.Parse(time.RFC3339Nano, revoked); perr == nil {
			t.RevokedAt = rt
		}
	}
	return t, nil
}

// VerifyNodeToken — host по токену (CONTROL 4): совпадение и не отозван.
// Возвращает (host, true), если токен валиден.
func (s *Store) VerifyNodeToken(token string) (string, bool) {
	if token == "" {
		return "", false
	}
	rows, err := s.db.Query(`SELECT host, revoked_at FROM node_token WHERE token = ?`, token)
	if err != nil {
		return "", false
	}
	defer rows.Close()
	for rows.Next() {
		var host, revoked string
		if err := rows.Scan(&host, &revoked); err != nil {
			return "", false
		}
		if revoked == "" {
			return host, true
		}
	}
	return "", false
}

// RevokeNodeToken — отозвать токен узла (при удалении узла).
func (s *Store) RevokeNodeToken(host string, at time.Time) error {
	return s.DoWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE node_token SET revoked_at = ? WHERE host = ? AND revoked_at = ''`, ts(at), host)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// RevokeNodeTokenByValue — отозвать токен по значению (enroll-токен может не
// быть ещё привязан к host; CONTROL 4: отозванный токен → 401).
func (s *Store) RevokeNodeTokenByValue(token string, at time.Time) error {
	return s.DoWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE node_token SET revoked_at = ? WHERE token = ? AND revoked_at = ''`, ts(at), token)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// ListNodeTokens — все токены (для /api/v1/nodes и отладки).
func (s *Store) ListNodeTokens() ([]NodeToken, error) {
	rows, err := s.db.Query(`SELECT host, token, created_at, revoked_at FROM node_token ORDER BY host`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NodeToken
	for rows.Next() {
		var t NodeToken
		var created, revoked string
		if err := rows.Scan(&t.Host, &t.Token, &created, &revoked); err != nil {
			return nil, err
		}
		t.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		if revoked != "" {
			if rt, perr := time.Parse(time.RFC3339Nano, revoked); perr == nil {
				t.RevokedAt = rt
			}
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// GetTokenByValue — запись по значению токена (enroll: проверить, существует
// ли токен; host может быть пуст — токен ещё не привязан).
func (s *Store) GetTokenByValue(token string) (NodeToken, error) {
	var t NodeToken
	var created, revoked string
	err := s.db.QueryRow(`SELECT host, token, created_at, revoked_at FROM node_token WHERE token = ?`, token).
		Scan(&t.Host, &t.Token, &created, &revoked)
	if err != nil {
		if err == sql.ErrNoRows {
			return NodeToken{}, ErrNotFound
		}
		return NodeToken{}, err
	}
	t.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	if revoked != "" {
		if rt, perr := time.Parse(time.RFC3339Nano, revoked); perr == nil {
			t.RevokedAt = rt
		}
	}
	return t, nil
}

// BindTokenHost — привязать (enroll) токен к host (claim, раздел 14.2).
// Токен должен существовать; отозванный привязать нельзя.
//
// Схема: host — PRIMARY KEY, т.е. один ряд на host; «ожидает» = ряд с
// host=''. Повторная регистрация (новый токен, тот же host): старый активный
// токен узла отзывается, а новый выдаётся в ряду host — «команда из
// интерфейса» остаётся повторять (W9 доп-3c: re-claim падал в
// UNIQUE(node_token.host) → 500 + ложный SAFE_MODE). Повтор claim тем же
// токеном и host — идемпотентен.
func (s *Store) BindTokenHost(token, host string, at time.Time) error {
	// Состояние токена — параллельным чтением (не через write-путь:
	// логический ErrNotFound не должен проходить как «ошибка записи»).
	var boundHost string
	err := s.db.QueryRow(`SELECT host FROM node_token
		WHERE token = ? AND revoked_at = ''`, token).Scan(&boundHost)
	if err == sql.ErrNoRows {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	// Уже привязан к этому host — идемпотентно.
	if boundHost == host {
		return nil
	}
	var haveHostRow int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM node_token WHERE host = ?`, host).
		Scan(&haveHostRow); err != nil {
		return err
	}
	if haveHostRow > 0 {
		// Re-enroll: ряд host существует — отозвать старый активный токен
		// и выдать новый в этом ряду; дубликат в pending-ряду отозвать.
		return s.DoWrite(func(tx *sql.Tx) error {
			if _, err := tx.Exec(`UPDATE node_token
				SET revoked_at = ?
				WHERE host = ? AND revoked_at = '' AND token <> ?`, ts(at), host, token); err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE node_token
				SET token = ?, created_at = ?, revoked_at = ''
				WHERE host = ?`, token, ts(at), host); err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE node_token
				SET revoked_at = ?
				WHERE token = ? AND revoked_at = '' AND host <> ?`, ts(at), token, host); err != nil {
				return err
			}
			return nil
		})
	}
	// Ряд host нет — перенести ряд токена (с pending '' или с другого host).
	return s.DoWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE node_token SET host = ?
			WHERE token = ? AND revoked_at = ''`, host, token)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound // гонка: токен отозвали между чтением и записью
		}
		return nil
	})
}

// ErrNoActiveNodeToken — токен отозван (используется как явная ошибка).
var ErrNoActiveNodeToken = errors.New("store: node token revoked")
