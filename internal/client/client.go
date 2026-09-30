// Package client — HTTP-клиент координатора для CLI и TUI (раздел 11 ТЗ).
//
// Токен — из env (client.token_env в конфиге); пустой токен — локальный
// режим. Клиент не делает опроса: TUI живёт на SSE, CLI — разовые запросы.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// Error — ошибка координатора: HTTP-статус + код + детали из тела.
type Error struct {
	Status int
	Code   string
	Detail string
}

func (e *Error) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("координатор %d %s: %s", e.Status, e.Code, e.Detail)
	}
	return fmt.Sprintf("координатор: HTTP %d", e.Status)
}

// Client — клиент координатора.
type Client struct {
	base   string
	token  string
	hc     *http.Client
}

// New создаёт клиент; tokenEnv — имя env-переменной с Bearer-токеном.
func New(base, tokenEnv string) *Client {
	return &Client{
		base:   base,
		token:  os.Getenv(tokenEnv),
		hc:     &http.Client{Timeout: httpTimeoutSec * time.Second},
	}
}

// Base — адрес координатора (для вывода/отладки).
func (c *Client) Base() string { return c.base }

// Do — запрос к координатору; out — куда декодировать тело (nil — не нужно).
// Возвращает HTTP-статус. Нон-2xx — *Error с кодом из тела.
func (c *Client) Do(method, path string, body, out any) (int, error) {
	var rd io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		rd = bytes.NewReader(data)
	}
	return c.do(method, path, rd, out)
}

// DoRaw — запрос с сырым телом и сырым ответом (для text/plain peek).
func (c *Client) DoRaw(method, path string, body any, out *[]byte) (int, error) {
	var rd io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		rd = bytes.NewReader(data)
	}
	return c.do(method, path, rd, out)
}

// DoContext — запрос с контекстом (SSE-переподключения, peek с таймаутом).
func (c *Client) DoContext(ctx context.Context, method, path string, body, out any) (int, error) {
	var rd io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		rd = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return 0, err
	}
	c.decorate(req)
	resp, err := c.hc.Do(req)
	if err != nil {
		return 0, fmt.Errorf("координатор %s: %w", c.base, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, err
	}
	if out != nil && resp.StatusCode < httpNonOK {
		if len(data) > 0 {
			if err := json.Unmarshal(data, out); err != nil {
				return resp.StatusCode, fmt.Errorf("разбор ответа: %w", err)
			}
		}
		return resp.StatusCode, nil
	}
	if resp.StatusCode >= httpNonOK {
		return resp.StatusCode, errorFromBody(resp.StatusCode, data)
	}
	return resp.StatusCode, nil
}

func (c *Client) do(method, path string, rd io.Reader, out any) (int, error) {
	req, err := http.NewRequest(method, c.base+path, rd)
	if err != nil {
		return 0, err
	}
	c.decorate(req)
	resp, err := c.hc.Do(req)
	if err != nil {
		return 0, fmt.Errorf("координатор %s: %w", c.base, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, err
	}
	switch v := out.(type) {
	case *[]byte:
		*v = data
		return resp.StatusCode, nil
	case nil:
		if resp.StatusCode >= httpNonOK {
			return resp.StatusCode, errorFromBody(resp.StatusCode, data)
		}
		return resp.StatusCode, nil
	default:
		if out != nil && resp.StatusCode < httpNonOK && len(data) > 0 {
			if err := json.Unmarshal(data, out); err != nil {
				return resp.StatusCode, fmt.Errorf("разбор ответа: %w", err)
			}
		}
		if resp.StatusCode >= httpNonOK {
			return resp.StatusCode, errorFromBody(resp.StatusCode, data)
		}
		return resp.StatusCode, nil
	}
}

func (c *Client) decorate(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
}

// errorFromBody — *Error из тела {code, detail}; пустое тело — голый статус.
func errorFromBody(status int, data []byte) error {
	var b struct {
		Code   string `json:"code"`
		Detail string `json:"detail"`
	}
	_ = json.Unmarshal(data, &b)
	return &Error{Status: status, Code: b.Code, Detail: b.Detail}
}

// IsNotFound — 404 от координатора.
func IsNotFound(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Status == http.StatusNotFound
}
