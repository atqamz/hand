package luvus

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"time"
)

type Event struct {
	Event    string          `json:"event"`
	Sequence int64           `json:"sequence"`
	Data     json.RawMessage `json:"data"`
}

type Stream struct {
	Sequence int64
	conn     net.Conn
	lines    *bufio.Reader
}

func (c Client) Subscribe(ctx context.Context) (*Stream, error) {
	timeout := c.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	dialCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(dialCtx, "unix", c.Socket)
	if err != nil {
		return nil, fmt.Errorf("%w at %s: %w", ErrUnreachable, c.Socket, err)
	}
	s, err := subscribe(conn, timeout)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	context.AfterFunc(ctx, func() { _ = conn.Close() })
	return s, nil
}

func subscribe(conn net.Conn, timeout time.Duration) (*Stream, error) {
	req, err := json.Marshal(map[string]any{"id": "hand-" + strconv.FormatUint(requests.Add(1), 10), "method": "events.subscribe", "params": struct{}{}})
	if err != nil {
		return nil, err
	}
	_ = conn.SetDeadline(time.Now().Add(timeout))
	if _, err := conn.Write(append(req, '\n')); err != nil {
		return nil, err
	}
	lines := bufio.NewReader(conn)
	line, err := lines.ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("luvus events.subscribe: no ack: %w", err)
	}
	var resp struct {
		Result struct {
			Sequence int64 `json:"sequence"`
		} `json:"result"`
		Error *Error `json:"error"`
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, fmt.Errorf("luvus events.subscribe: bad ack: %w", err)
	}
	if resp.Error != nil {
		return nil, resp.Error
	}
	_ = conn.SetDeadline(time.Time{})
	return &Stream{Sequence: resp.Result.Sequence, conn: conn, lines: lines}, nil
}

func (s *Stream) Next() (Event, error) {
	line, err := s.lines.ReadBytes('\n')
	if err != nil {
		return Event{}, err
	}
	var e Event
	if err := json.Unmarshal(line, &e); err != nil {
		return Event{}, fmt.Errorf("luvus event: %w", err)
	}
	return e, nil
}

func (s *Stream) Close() error { return s.conn.Close() }
