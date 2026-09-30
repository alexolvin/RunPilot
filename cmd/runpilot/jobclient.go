// Раздел 8.1 ТЗ: протокол runpilot exec (HTTP-клиент к /api/v1/jobs/*).
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// errNoCoordinator — координатор не отвечает (→ код 69).
var errNoCoordinator = errors.New("coordinator unavailable")

// errDenied — отказ в доступе (токен/IP, HTTP 403 → код 77).
var errDenied = errors.New("access denied")

// jobEnv — окружение шлюза из /wait.
type jobEnv struct {
	SID    string            `json:"sid"`
	Server string            `json:"server"`
	Slot   int               `json:"slot"`
	Env    map[string]string `json:"env"`
}

// jobAPI — HTTP-клиент аqm exec.
type jobAPI struct {
	base   string
	token  string
	client *http.Client
}

// jobHTTP — один запрос к координатору. Классификация ошибок:
// сеть/таймаут → errNoCoordinator; 403 → errDenied; 4xx/5xx → error.
func (j *jobAPI) do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		rd = bytes.NewReader(data)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, err := http.NewRequestWithContext(ctx, method, j.base+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if j.token != "" {
		req.Header.Set("Authorization", "Bearer "+j.token)
	}
	resp, err := j.client.Do(req)
	if err != nil {
		return errNoCoordinator
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	switch {
	case resp.StatusCode == http.StatusForbidden:
		return errDenied
	case resp.StatusCode >= http.StatusMultipleChoices:
		return fmt.Errorf("jobs %s: HTTP %d: %s", path, resp.StatusCode, truncate(data))
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("jobs %s: разбор: %w", path, err)
		}
	}
	return nil
}

// create — POST /api/v1/jobs.
func (j *jobAPI) create(ctx context.Context, name, prio, pin, prefer string) (string, error) {
	body := map[string]any{"name": name, "prio": prio, "pin": pin, "prefer": prefer}
	var out struct {
		SID string `json:"sid"`
	}
	if err := j.do(ctx, http.MethodPost, "/api/v1/jobs", body, &out); err != nil {
		return "", err
	}
	return out.SID, nil
}

// wait — GET /api/v1/jobs/{sid}/wait?wait_sec=N. granted=false при 204
// (ещё нет аренды); 410 — сессия завершена (granted=false, done=true).
func (j *jobAPI) wait(ctx context.Context, sid string, waitSec int) (env jobEnv, granted, done bool, err error) {
	q := url.Values{}
	q.Set("wait_sec", fmt.Sprintf("%d", waitSec))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		j.base+"/api/v1/jobs/"+sid+"/wait?"+q.Encode(), nil)
	if err != nil {
		return jobEnv{}, false, false, err
	}
	if j.token != "" {
		req.Header.Set("Authorization", "Bearer "+j.token)
	}
	resp, err := j.client.Do(req)
	if err != nil {
		return jobEnv{}, false, false, errNoCoordinator
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	switch resp.StatusCode {
	case http.StatusOK:
		_ = json.Unmarshal(data, &env)
		return env, true, false, nil
	case http.StatusNoContent:
		return jobEnv{}, false, false, nil // ещё ждём
	case http.StatusGone:
		return jobEnv{}, false, true, nil // сессия завершена
	case http.StatusForbidden:
		return jobEnv{}, false, false, errDenied
	default:
		return jobEnv{}, false, false, fmt.Errorf("jobs wait: HTTP %d: %s", resp.StatusCode, truncate(data))
	}
}

// heartbeat — POST /api/v1/jobs/{sid}/heartbeat. cancel=true → SIGTERM.
func (j *jobAPI) heartbeat(ctx context.Context, sid string) (cancel bool, err error) {
	var out struct {
		Cancel bool `json:"cancel"`
	}
	if err := j.do(ctx, http.MethodPost, "/api/v1/jobs/"+sid+"/heartbeat", nil, &out); err != nil {
		return false, err
	}
	return out.Cancel, nil
}

// finish — POST /api/v1/jobs/{sid}/finish {exit_code, signal}.
func (j *jobAPI) finish(ctx context.Context, sid string, exitCode int, signal string) error {
	body := map[string]any{"exit_code": exitCode, "signal": signal}
	return j.do(ctx, http.MethodPost, "/api/v1/jobs/"+sid+"/finish", body, nil)
}

// truncate — обрезать тело для сообщения об ошибке.
func truncate(b []byte) string {
	if len(b) <= jobErrBodyLimit {
		return string(b)
	}
	return string(b[:jobErrBodyLimit]) + "…"
}
