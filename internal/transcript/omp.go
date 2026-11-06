package transcript

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func (r *Reader) omp(s *session, id, dir string) error {
	roots := []string{r.Paths.Omp}
	if dir != "" && dir != r.Paths.Omp {
		roots = append(roots, dir)
	}
	var files []string
	var readErr error
	for _, root := range roots {
		if root == "" {
			continue
		}
		base := filepath.Join(root, "sessions")
		dirs, err := os.ReadDir(base)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			readErr = err
			continue
		}
		for _, folder := range dirs {
			entries, err := os.ReadDir(filepath.Join(base, folder.Name()))
			if err != nil {
				continue
			}
			for _, entry := range entries {
				if !entry.IsDir() && strings.HasSuffix(entry.Name(), "_"+id+".jsonl") {
					files = append(files, filepath.Join(base, folder.Name(), entry.Name()))
				}
			}
		}
	}
	if len(files) == 0 {
		if readErr != nil {
			return readErr
		}
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
				Input      int64 `json:"input"`
				CacheWrite int64 `json:"cacheWrite"`
				CacheRead  int64 `json:"cacheRead"`
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
			if u := m.Usage; u != nil && u.Input+u.CacheRead+u.CacheWrite > 0 {
				s.status.Context = u.Input + u.CacheRead + u.CacheWrite
			}
		}
	case "thinking_level_change":
		if rec.ThinkingLevel != "" {
			s.status.Effort = rec.ThinkingLevel
		}
	}
}
