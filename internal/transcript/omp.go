package transcript

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

func (r *Reader) omp(s *session, id string) error {
	if r.Paths.Omp == "" {
		return ErrNoSession
	}
	files, err := filepath.Glob(filepath.Join(r.Paths.Omp, "sessions", "*", "*_"+id+".jsonl"))
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return ErrNoSession
	}
	for _, f := range files {
		if fi, err := os.Stat(f); err == nil && fi.Size() < s.offsets[f] {
			*s = session{offsets: map[string]int64{}}
			break
		}
	}
	for _, f := range files {
		if err := s.follow(f, s.ompLine); err != nil {
			return err
		}
	}
	return nil
}

func (s *session) ompLine(line []byte) {
	var rec struct {
		Type      string `json:"type"`
		Timestamp string `json:"timestamp"`
		Message   struct {
			Role       string `json:"role"`
			Provider   string `json:"provider"`
			Model      string `json:"model"`
			StopReason string `json:"stopReason"`
			Content    []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			Usage *struct {
				Input     int64 `json:"input"`
				CacheRead int64 `json:"cacheRead"`
			} `json:"usage"`
		} `json:"message"`
		ThinkingLevel string `json:"thinkingLevel"`
	}
	if json.Unmarshal(line, &rec) != nil || rec.Type == "" {
		return
	}
	s.known++
	m := rec.Message
	switch rec.Type {
	case "message":
		var texts []string
		for _, c := range m.Content {
			if c.Type == "text" && c.Text != "" {
				texts = append(texts, c.Text)
			}
		}
		text := strings.Join(texts, "\n")
		switch m.Role {
		case "user":
			s.user(text, rec.Timestamp)
		case "assistant":
			s.reply(text, rec.Timestamp, m.StopReason == "stop")
			if m.Provider != "" && m.Model != "" {
				s.status.Model, s.status.ModelAt = m.Provider+"/"+m.Model, rec.Timestamp
			}
			if m.Usage != nil && m.Usage.Input+m.Usage.CacheRead > 0 {
				s.status.Context = m.Usage.Input + m.Usage.CacheRead
			}
		}
	case "thinking_level_change":
		if rec.ThinkingLevel != "" {
			s.status.Effort = rec.ThinkingLevel
		}
	}
}
