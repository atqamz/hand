package transcript

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var codexInjected = regexp.MustCompile(`^(<[a-z_]+[ >]|# AGENTS\.md instructions)`)

func (r *Reader) codex(s *session, id string) error {
	files, err := r.codexFiles(id)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return ErrNoSession
	}
	for _, f := range files {
		if err := s.follow(f, s.codexLine); err != nil {
			return err
		}
	}
	return nil
}

func (r *Reader) codexFiles(id string) ([]string, error) {
	paths, err := filepath.Glob(filepath.Join(r.Paths.Codex, "sessions", "*", "*", "*", "rollout-*.jsonl"))
	if err != nil {
		return nil, err
	}
	if r.rollout == nil {
		r.rollout = map[string]string{}
	}
	var out []string
	for _, p := range paths {
		got, ok := r.rollout[p]
		if !ok {
			got = rolloutID(p)
			r.rollout[p] = got
		}
		if got == id {
			out = append(out, p)
		}
	}
	return out, nil
}

func rolloutID(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	var rec struct {
		Type    string `json:"type"`
		Payload struct {
			ID string `json:"id"`
		} `json:"payload"`
	}
	if json.NewDecoder(f).Decode(&rec) != nil || rec.Type != "session_meta" {
		return ""
	}
	return rec.Payload.ID
}

func (s *session) codexLine(line []byte) {
	var rec struct {
		Type      string `json:"type"`
		Timestamp string `json:"timestamp"`
		Payload   struct {
			Type    string `json:"type"`
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &rec) != nil || rec.Type == "" {
		return
	}
	s.known++
	p := rec.Payload
	switch {
	case rec.Type == "response_item" && p.Type == "message" && p.Role == "user":
		for _, c := range p.Content {
			if c.Type == "input_text" && !codexInjected.MatchString(strings.TrimSpace(c.Text)) {
				s.user(c.Text, rec.Timestamp)
			}
		}
	case rec.Type == "response_item" && p.Type == "message" && p.Role == "assistant":
		for _, c := range p.Content {
			if c.Type == "output_text" {
				s.turn, s.shown = &Entry{Role: "supervisor", Text: strings.TrimSpace(c.Text), At: rec.Timestamp}, calm(c.Text, false)
				if s.shown {
					s.add(*s.turn)
				}
			}
		}
	case rec.Type == "event_msg" && p.Type == "task_complete":
		if s.turn != nil && !s.shown && calm(s.turn.Text, true) {
			s.add(*s.turn)
		}
		s.turn, s.shown = nil, false
	}
}
