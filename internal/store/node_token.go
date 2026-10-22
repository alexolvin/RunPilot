package store

// Токены узлов (раздел 14.2 v2). Узел загружает дистрибутив по Bearer-токену;
// отзыв при удалении узла делает повторную загрузку 401 (CONTROL 4).
//
// Токен ключится по идентичности узла (host, os_user), а не по host: два узла
// на одном host (разные os_user) имеют независимые токены, re-enroll одного не
// отзывает токен другого (дефект 2026-10-21, инцидент vzu5-big). Legacy-строки
// (os_user='') остаются валидными: совпадение по токену с os_user='' — только
// по host (совместимость со старыми узлами).

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
	OsUser    string
	Token     string
	CreatedAt time.Time
	RevokedAt time.Time
}

// Active — токен не отозван.
func (t NodeToken) Active() bool { return t.RevokedAt.IsZero() }

// NewNodeToken — сгенерировать токен узла (host, os_user, now).
func NewNodeToken(host, osUser string, now time.Time) NodeToken {
	buf := make([]byte, nodeTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand не может реально не сработать; фолбэк по времени.
		for i := range buf {
			buf[i] = byte(now.UnixNano() >> (i * bitsPerByte))
		}
	}
	return NodeToken{Host: host, OsUser: osUser, Token: hex.EncodeToString(buf), CreatedAt: now}
}

// CreateNodeToken — вставить токен (существующий для (host, os_user)
// перезаписывается).
func (s *Store) CreateNodeToken(t NodeToken) error {
	return s.DoWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO node_token (host, os_user, token, created_at, revoked_at)
			VALUES (?, ?, ?, ?, '')
			ON CONFLICT(host, os_user) DO UPDATE SET token=excluded.token,
			    created_at=excluded.created_at, revoked_at=''`,
			t.Host, t.OsUser, t.Token, ts(t.CreatedAt))
		return err
	})
}

// GetNodeToken — токен по (host, os_user) (ErrNotFound, если нет).
func (s *Store) GetNodeToken(host, osUser string) (NodeToken, error) {
	var t NodeToken
	var created, revoked string
	err := s.db.QueryRow(`SELECT token, created_at, revoked_at FROM node_token WHERE host = ? AND os_user = ?`,
		host, osUser).Scan(&t.Token, &created, &revoked)
	if err != nil {
		if err == sql.ErrNoRows {
			return NodeToken{}, ErrNotFound
		}
		return NodeToken{}, err
	}
	t.Host = host
	t.OsUser = osUser
	t.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	if revoked != "" {
		if rt, perr := time.Parse(time.RFC3339Nano, revoked); perr == nil {
			t.RevokedAt = rt
		}
	}
	return t, nil
}

// VerifyNodeToken — идентичность узла по токену (CONTROL 4): совпадение и не
// отозван. Возвращает (host, os_user, true), если токен валиден.
func (s *Store) VerifyNodeToken(token string) (string, string, bool) {
	if token == "" {
		return "", "", false
	}
	rows, err := s.db.Query(`SELECT host, os_user, revoked_at FROM node_token WHERE token = ?`, token)
	if err != nil {
		return "", "", false
	}
	defer rows.Close()
	for rows.Next() {
		var host, osUser, revoked string
		if err := rows.Scan(&host, &osUser, &revoked); err != nil {
			return "", "", false
		}
		if revoked == "" {
			return host, osUser, true
		}
	}
	return "", "", false
}

// RevokeNodeToken — отозвать токен узла (при удалении узла) по (host, os_user).
func (s *Store) RevokeNodeToken(host, osUser string, at time.Time) error {
	return s.DoWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE node_token SET revoked_at = ? WHERE host = ? AND os_user = ? AND revoked_at = ''`,
			ts(at), host, osUser)
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
	rows, err := s.db.Query(`SELECT host, os_user, token, created_at, revoked_at FROM node_token ORDER BY host, os_user`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NodeToken
	for rows.Next() {
		var t NodeToken
		var created, revoked string
		if err := rows.Scan(&t.Host, &t.OsUser, &t.Token, &created, &revoked); err != nil {
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
	err := s.db.QueryRow(`SELECT host, os_user, token, created_at, revoked_at FROM node_token WHERE token = ?`, token).
		Scan(&t.Host, &t.OsUser, &t.Token, &created, &revoked)
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

// BindTokenHost — привязать (enroll) токен к узлу (host, os_user) (claim,
// раздел 14.2). Токен должен существовать; отозванный привязать нельзя.
//
// Схема: PRIMARY KEY (host, os_user), т.е. один ряд на идентичность узла;
// «ожидает» = ряд с host='', os_user=''. Повторная регистрация (новый токен,
// тот же узел host:os_user): старый активный токен узла отзывается, а новый
// выдаётся в ряду узла — «команда из интерфейса» остаётся повторять (W9 доп-3c:
// re-claim падал в UNIQUE(node_token.host) → 500 + ложный SAFE_MODE). Другой
// узел на том же host (другой os_user) не затрагивается. Повтор claim тем же
// токеном и узлом — идемпотентен.
func (s *Store) BindTokenHost(token, host, osUser string, at time.Time) error {
	// Состояние токена — параллельным чтением (не через write-путь:
	// логический ErrNotFound не должен проходить как «ошибка записи»).
	var boundHost, boundOsUser string
	err := s.db.QueryRow(`SELECT host, os_user FROM node_token
		WHERE token = ? AND revoked_at = ''`, token).Scan(&boundHost, &boundOsUser)
	if err == sql.ErrNoRows {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	// Уже привязан к этому узлу (host, os_user) — идемпотентно.
	if boundHost == host && boundOsUser == osUser {
		return nil
	}
	var haveRow int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM node_token WHERE host = ? AND os_user = ?`, host, osUser).
		Scan(&haveRow); err != nil {
		return err
	}
	if haveRow > 0 {
		// Re-enroll: ряд узла (host, os_user) существует — отозвать старый
		// активный токен узла и выдать новый в этом ряду; дубликат в
		// pending-ряду отозвать. Другие узлы на том же host не затрагиваются.
		return s.DoWrite(func(tx *sql.Tx) error {
			if _, err := tx.Exec(`UPDATE node_token
				SET revoked_at = ?
				WHERE host = ? AND os_user = ? AND revoked_at = '' AND token <> ?`,
				ts(at), host, osUser, token); err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE node_token
				SET token = ?, created_at = ?, revoked_at = ''
				WHERE host = ? AND os_user = ?`, token, ts(at), host, osUser); err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE node_token
				SET revoked_at = ?
				WHERE token = ? AND revoked_at = '' AND (host <> ? OR os_user <> ?)`,
				ts(at), token, host, osUser); err != nil {
				return err
			}
			return nil
		})
	}
	// Ряд узла (host, os_user) нет — перенести ряд токена (с pending ''
	// или с другого узла).
	return s.DoWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE node_token SET host = ?, os_user = ?
			WHERE token = ? AND revoked_at = ''`, host, osUser, token)
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
