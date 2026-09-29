package transcript

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

type Entry struct {
	Role   string
	Text   string
	At     string
	Queued bool
}

var (
	ErrNoSession  = errors.New("waiting for the session…")
	ErrUnreadable = errors.New("transcript not readable (format may have changed)")
)

type Paths struct {
	Claude       string
	Codex        string
	Opencode     string
	OpencodeData string
}

type Reader struct {
	Paths   Paths
	mu      sync.Mutex
	logs    map[string]*session
	rollout map[string]string
}

type session struct {
	offsets  map[string]int64
	entries  []Entry
	lines    int
	known    int
	turn     *Entry
	shown    bool
	exported time.Time
	stamp    time.Time
}

const (
	wakeHeader = "[hand v1 wake]"
	noteLimit  = 200
	substance  = 240
)

var (
	sessionID = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	pasteOpen = regexp.MustCompile(`^<pasted_content id="([A-Za-z0-9]{1,32})">$`)
	launch    = regexp.MustCompile(`^You are supervisor (s[0-9]+) of the Hand fleet `)
)

func (r *Reader) Read(ctx context.Context, harness, id, dir string) ([]Entry, error) {
	if !sessionID.MatchString(id) {
		return nil, ErrNoSession
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.logs == nil {
		r.logs = map[string]*session{}
	}
	key := harness + ":" + id
	s := r.logs[key]
	if s == nil {
		s = &session{offsets: map[string]int64{}}
		r.logs[key] = s
	}
	var err error
	switch harness {
	case "claude":
		err = r.claude(s, id)
	case "codex":
		err = r.codex(s, id)
	case "opencode":
		err = r.opencode(ctx, s, id, dir)
	default:
		err = fmt.Errorf("transcript: harness %q has no reader", harness)
	}
	if err != nil {
		return nil, err
	}
	if s.lines > 0 && s.known == 0 {
		return nil, ErrUnreadable
	}
	return slices.Clone(s.entries), nil
}

func (s *session) follow(path string, line func([]byte)) error {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return ErrNoSession
	}
	if err != nil {
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	off := s.offsets[path]
	if fi.Size() < off {
		*s = session{offsets: map[string]int64{}}
		off = 0
	}
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return err
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return err
	}
	end := bytes.LastIndexByte(b, '\n')
	if end < 0 {
		return nil
	}
	for _, l := range bytes.Split(b[:end], []byte{'\n'}) {
		if len(bytes.TrimSpace(l)) == 0 {
			continue
		}
		s.lines++
		line(l)
	}
	s.offsets[path] = off + int64(end) + 1
	return nil
}

func (s *session) add(e Entry) { s.entries = append(s.entries, e) }

func (s *session) user(text, at string) {
	if e, ok := classify(text); ok {
		e.At = at
		s.add(e)
	}
}

func (s *session) reply(text, at string, final bool) {
	if calm(text, final) {
		s.add(Entry{Role: "supervisor", Text: strings.TrimSpace(text), At: at})
	}
}

func calm(text string, final bool) bool {
	t := strings.TrimSpace(text)
	switch {
	case t == "":
		return false
	case final:
		return true
	}
	return strings.Contains(t, "\n") || utf8.RuneCountInString(t) >= substance
}

func unwrap(text string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	start, closing := -1, ""
	for _, l := range lines {
		switch {
		case start < 0:
			if m := pasteOpen.FindStringSubmatch(l); m != nil {
				start, closing = len(out), `</pasted_content id="`+m[1]+`">`
			}
		case l == closing:
			out = append(out[:start], out[start+1:]...)
			start = -1
			continue
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

func classify(text string) (Entry, bool) {
	t := strings.TrimSpace(unwrap(text))
	if t == "" {
		return Entry{}, false
	}
	lines := strings.Split(t, "\n")
	switch {
	case strings.TrimSpace(lines[0]) == wakeHeader:
		return Entry{Role: "hand", Text: clip("wake: " + strings.Join(lines[1:], "; "))}, true
	case strings.HasPrefix(t, "[Request interrupted"):
		return Entry{Role: "hand", Text: "interrupted"}, true
	}
	if m := launch.FindStringSubmatch(t); m != nil {
		return Entry{Role: "hand", Text: "supervisor " + m[1] + " started"}, true
	}
	return Entry{Role: "operator", Text: t}, true
}

func clip(s string) string {
	if utf8.RuneCountInString(s) <= noteLimit {
		return s
	}
	return string([]rune(s)[:noteLimit]) + "…"
}
