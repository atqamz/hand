package transcript

import (
	"cmp"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
)

const (
	copilotKeep = 200
	copilotText = 4000
)

func (r *Reader) copilot(s *session, id string) error {
	if r.Paths.Copilot == "" {
		return errors.New("copilot home unknown")
	}
	return s.follow(filepath.Join(r.Paths.Copilot, "session-state", id, "events.jsonl"), s.copilotLine)
}

func (s *session) copilotLine(line []byte) {
	var rec struct {
		Type      string          `json:"type"`
		Timestamp string          `json:"timestamp"`
		Data      json.RawMessage `json:"data"`
	}
	if json.Unmarshal(line, &rec) != nil || rec.Type == "" {
		return
	}
	s.known++
	var d struct {
		SelectedModel string          `json:"selectedModel"`
		Model         string          `json:"model"`
		Content       string          `json:"content"`
		TurnID        string          `json:"turnId"`
		ToolName      string          `json:"toolName"`
		Arguments     json.RawMessage `json:"arguments"`
	}
	if json.Unmarshal(rec.Data, &d) != nil {
		return
	}
	switch rec.Type {
	case "session.start":
		s.status.Selected = d.SelectedModel
	case "user.message":
		if d.TurnID != "" && d.TurnID != "0" {
			s.user(d.Content, rec.Timestamp)
		}
	case "assistant.message":
		if d.Model != "" {
			s.status.Used = d.Model
		}
		if t := strings.TrimSpace(d.Content); t != "" {
			if len(t) > copilotText {
				t = strings.ToValidUTF8(t[:copilotText], "") + "…"
			}
			s.add(Entry{Role: "worker", Text: t, At: rec.Timestamp})
		}
	case "tool.execution_start":
		s.add(Entry{Role: "tool", Text: toolLine(d.ToolName, d.Arguments), At: rec.Timestamp})
	}
	if n := len(s.entries) - copilotKeep; n > 0 {
		s.entries = slices.Delete(s.entries, 0, n)
	}
}

func toolLine(name string, raw json.RawMessage) string {
	var a struct {
		Command     string `json:"command"`
		Path        string `json:"path"`
		Description string `json:"description"`
	}
	_ = json.Unmarshal(raw, &a)
	what, _, _ := strings.Cut(strings.TrimSpace(cmp.Or(a.Command, a.Path, a.Description)), "\n")
	if what == "" {
		return name
	}
	return clip(name + ": " + what)
}
