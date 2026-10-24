package mockllm

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
)

type Step struct {
	Shell string
	Text  string
}

const sideTitle = `{"title":"Mock session"}`

type Request struct {
	Seq    int
	At     time.Time
	Method string
	Path   string
	Header http.Header
	Body   []byte
	Turn   int
	Reply  Step
}

type Server struct {
	URL   string
	steps []Step
	srv   *http.Server
	mu    sync.Mutex
	log   []Request
}

func Start(steps ...Step) (*Server, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s := &Server{URL: "http://" + ln.Addr().String(), steps: steps}
	s.srv = &http.Server{Handler: s}
	go s.srv.Serve(ln)
	return s, nil
}

func (s *Server) Close() error { return s.srv.Close() }

func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.log)
}

func (s *Server) Dump() []byte {
	var b strings.Builder
	for _, r := range s.Requests() {
		fmt.Fprintf(&b, "=== #%d %s %s %s turn=%d\n", r.Seq, r.At.Format("15:04:05.000"), r.Method, r.Path, r.Turn)
		keys := make([]string, 0, len(r.Header))
		for k := range r.Header {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "%s: %s\n", k, strings.Join(r.Header[k], ", "))
		}
		fmt.Fprintf(&b, "\n%s\n\n", r.Body)
	}
	return []byte(b.String())
}

func (s *Server) step(turn int) Step {
	if turn < len(s.steps) {
		return s.steps[turn]
	}
	return Step{Text: "Nothing more to do."}
}

func (s *Server) record(r *http.Request, body []byte, turn int, reply Step) {
	h := r.Header.Clone()
	for _, k := range []string{"Authorization", "X-Api-Key", "Api-Key"} {
		if h.Get(k) != "" {
			h.Set(k, "<redacted>")
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log = append(s.log, Request{Seq: len(s.log) + 1, At: time.Now(), Method: r.Method, Path: r.URL.RequestURI(), Header: h, Body: body, Turn: turn, Reply: reply})
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	path := r.URL.Path
	switch {
	case r.Method == http.MethodPost && path == "/v1/messages":
		s.anthropic(w, r, body)
	case r.Method == http.MethodPost && path == "/v1/messages/count_tokens":
		s.record(r, body, -1, Step{})
		writeJSON(w, 200, map[string]any{"input_tokens": 1000})
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/responses"):
		s.responses(w, r, body)
	default:
		s.record(r, body, -1, Step{})
		writeJSON(w, 404, map[string]any{"error": map[string]any{"type": "not_found_error", "message": "mockllm: no such route " + path}})
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

type sse struct {
	w http.ResponseWriter
	f http.Flusher
}

func newSSE(w http.ResponseWriter) sse {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(200)
	f, _ := w.(http.Flusher)
	return sse{w, f}
}

func (s sse) event(name string, v any) {
	b, _ := json.Marshal(v)
	fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", name, b)
	if s.f != nil {
		s.f.Flush()
	}
}
