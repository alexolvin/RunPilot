package client

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Event — событие SSE (приложение Б ТЗ). ID — из заголовка `id:` (для after).
// Неизвестный kind TUI обязан показать как сырой JSON и не падать.
type Event struct {
	ID      int64           `json:"-"`
	Kind    string          `json:"kind"`
	SID     string          `json:"sid,omitempty"`
	Server  string          `json:"server,omitempty"`
	TS      string          `json:"ts"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// StreamEvents — подписка на /api/v1/events с автопереподключением.
// Каждое прочитанное событие уходит в ch (буфер у вызывающего).
// lastID — указатель на последний полученный id (0 — начальная лента);
// обновляется по мере чтения, повторное подключение идёт строго после
// него — без дублей. Функция завершается по ctx.Done().
func (c *Client) StreamEvents(ctx context.Context, lastID *int64, ch chan<- Event) error {
	for {
		c.readEvents(ctx, lastID, ch)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Соединение оборвалось — пауза и повтор с последним id.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(reconnectDelay):
		}
	}
}

// reconnectDelay — пауза перед повторным подключением SSE.
const reconnectDelay = time.Second

// readEvents — один SSE-поток; возвращает ошибку при обрыве или ctx.Done.
// lastID — обновляется после каждого кадра (для переподключения без дублей).
func (c *Client) readEvents(ctx context.Context, lastID *int64, ch chan<- Event) error {
	path := "/api/v1/events"
	if *lastID > 0 {
		path += "?after=" + strconv.FormatInt(*lastID, decimalBase)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return err
	}
	c.decorate(req)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Del("Content-Type")
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("SSE: HTTP %d", resp.StatusCode)
	}

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, sibInitialBuf), sibMaxBuf)
	var id int64
	var kind, data string
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			// Конец кадра.
			if data != "" {
				e := Event{ID: id, Kind: kind}
				_ = json.Unmarshal([]byte(data), &e)
				if e.ID == 0 {
					e.ID = id
				}
				if e.ID > *lastID {
					*lastID = e.ID
				}
				select {
				case ch <- e:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			id, kind, data = 0, "", ""
		case strings.HasPrefix(line, ":"):
			// keep-alive комментарий — игнор.
		case strings.HasPrefix(line, "id:"):
			id, _ = strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(line, "id:")), decimalBase, int64Bits)
		case strings.HasPrefix(line, "event:"):
			kind = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			data += strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		}
	}
	if err := sc.Err(); err != nil && ctx.Err() == nil {
		return err
	}
	return ctx.Err()
}
