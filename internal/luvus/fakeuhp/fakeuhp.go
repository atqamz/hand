package fakeuhp

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"net"
	"slices"
	"sync"
	"testing"

	"github.com/atqamz/hand/internal/luvus"
)

type Fail struct{ Code, Message string }

func (f Fail) Error() string { return f.Code + ": " + f.Message }

var Drop = errors.New("drop the connection without a reply")

type Server struct {
	Socket     string
	mu         sync.Mutex
	generation string
	seq        int64
	subs       []net.Conn
	handlers   map[string]func(json.RawMessage) (any, error)
	calls      map[string][]json.RawMessage
}

func Start(t testing.TB, socket string) *Server {
	t.Helper()
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Socket: socket, generation: "gen-1", handlers: map[string]func(json.RawMessage) (any, error){}, calls: map[string][]json.RawMessage{}}
	s.Handle("uhp.capabilities", func(json.RawMessage) (any, error) {
		return map[string]any{
			"type":              "uhp_capabilities",
			"protocol":          map[string]any{"name": "luvus-uhp", "major": 1, "minor": 0},
			"methods":           luvus.Required,
			"server_generation": s.Generation(),
		}, nil
	})
	s.Handle("events.subscribe", func(json.RawMessage) (any, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		return map[string]any{"type": "subscription_started", "sequence": s.seq, "replayed": 0}, nil
	})
	go s.serve(ln)
	t.Cleanup(func() { _ = ln.Close(); s.DropSubscribers() })
	return s
}

func (s *Server) Handle(method string, fn func(json.RawMessage) (any, error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[method] = fn
}

func (s *Server) Calls(method string) []json.RawMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.calls[method])
}

func (s *Server) SetGeneration(g string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.generation = g
}

func (s *Server) Generation() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.generation
}

func (s *Server) serve(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go s.reply(conn)
	}
}

func (s *Server) reply(conn net.Conn) {
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		_ = conn.Close()
		return
	}
	id, method, result, err := s.dispatch(line)
	if errors.Is(err, Drop) {
		_ = conn.Close()
		return
	}
	resp := map[string]any{"id": id}
	var f Fail
	switch {
	case errors.As(err, &f):
		resp["error"] = map[string]string{"code": f.Code, "message": f.Message}
	case err != nil:
		resp["error"] = map[string]string{"code": "internal", "message": err.Error()}
	default:
		resp["result"] = result
	}
	b, _ := json.Marshal(resp)
	_, _ = conn.Write(append(b, '\n'))
	if method != "events.subscribe" || err != nil {
		_ = conn.Close()
		return
	}
	s.mu.Lock()
	s.subs = append(s.subs, conn)
	s.mu.Unlock()
	go func() {
		_, _ = io.Copy(io.Discard, conn)
		s.mu.Lock()
		s.subs = slices.DeleteFunc(s.subs, func(c net.Conn) bool { return c == conn })
		s.mu.Unlock()
	}()
}

func (s *Server) dispatch(line []byte) (string, string, any, error) {
	var req map[string]json.RawMessage
	if err := json.Unmarshal(line, &req); err != nil {
		return "0", "", nil, Fail{"invalid_request", "bad json"}
	}
	var id, method string
	_ = json.Unmarshal(req["id"], &id)
	_ = json.Unmarshal(req["method"], &method)
	for k := range req {
		if k != "id" && k != "method" && k != "params" && k != "auth" {
			return id, method, nil, Fail{"invalid_request", "invalid versioned API request envelope"}
		}
	}
	s.mu.Lock()
	fn, ok := s.handlers[method]
	s.calls[method] = append(s.calls[method], req["params"])
	s.mu.Unlock()
	if !ok {
		return id, method, nil, Fail{"invalid_request", "unknown method: " + method}
	}
	result, err := fn(req["params"])
	return id, method, result, err
}

func (s *Server) Publish(event string, data any) {
	s.mu.Lock()
	s.seq++
	b, _ := json.Marshal(map[string]any{"event": event, "sequence": s.seq, "data": data})
	subs := slices.Clone(s.subs)
	s.mu.Unlock()
	for _, c := range subs {
		_, _ = c.Write(append(b, '\n'))
	}
}

func (s *Server) Subscribers() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.subs)
}

func (s *Server) DropSubscribers() {
	s.mu.Lock()
	subs := s.subs
	s.subs = nil
	s.mu.Unlock()
	for _, c := range subs {
		_ = c.Close()
	}
}
