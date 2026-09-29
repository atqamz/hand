package transcript

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
)

var claudeInjected = regexp.MustCompile(`^<(task-notification|command-[a-z-]+|local-command-[a-z-]+|system-reminder|bash-[a-z-]+)>`)

func (r *Reader) claude(s *session, id string) error {
	paths, err := filepath.Glob(filepath.Join(r.Paths.Claude, "projects", "*", id+".jsonl"))
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return ErrNoSession
	}
	return s.follow(paths[0], s.claudeLine)
}

func (s *session) claudeLine(line []byte) {
	var rec struct {
		Type         string `json:"type"`
		Timestamp    string `json:"timestamp"`
		IsMeta       bool   `json:"isMeta"`
		IsSidechain  bool   `json:"isSidechain"`
		IsCompact    bool   `json:"isCompactSummary"`
		OnlyInRecord bool   `json:"isVisibleInTranscriptOnly"`
		IsAPIError   bool   `json:"isApiErrorMessage"`
		Message      struct {
			Model      string          `json:"model"`
			StopReason string          `json:"stop_reason"`
			Content    json.RawMessage `json:"content"`
			Usage      *struct {
				Input         int64 `json:"input_tokens"`
				CacheCreation int64 `json:"cache_creation_input_tokens"`
				CacheRead     int64 `json:"cache_read_input_tokens"`
			} `json:"usage"`
		} `json:"message"`
		Attachment struct {
			Type        string `json:"type"`
			CommandMode string `json:"commandMode"`
			Prompt      string `json:"prompt"`
		} `json:"attachment"`
	}
	if json.Unmarshal(line, &rec) != nil || rec.Type == "" {
		return
	}
	s.known++
	switch {
	case rec.IsCompact:
		s.status.Compactions++
		s.add(Entry{Role: "hand", Text: "context compacted", At: rec.Timestamp})
		return
	case rec.IsMeta, rec.IsSidechain, rec.OnlyInRecord:
		return
	case rec.Message.Model == "<synthetic>" && !rec.IsAPIError:
		return
	}
	switch rec.Type {
	case "user":
		if text, ok := claudeUserText(rec.Message.Content); ok {
			s.user(text, rec.Timestamp)
		}
	case "attachment":
		if rec.Attachment.Type == "queued_command" && rec.Attachment.CommandMode == "prompt" {
			s.user(rec.Attachment.Prompt, rec.Timestamp)
		}
	case "assistant":
		if u := rec.Message.Usage; u != nil && u.Input+u.CacheCreation+u.CacheRead > 0 {
			s.status.Context = u.Input + u.CacheCreation + u.CacheRead
		}
		var blocks []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(rec.Message.Content, &blocks) != nil {
			return
		}
		final := rec.Message.StopReason == "end_turn" || rec.Message.StopReason == "stop_sequence"
		for _, b := range blocks {
			if b.Type == "text" {
				s.reply(b.Text, rec.Timestamp, final)
			}
		}
	}
}

func claudeUserText(content json.RawMessage) (string, bool) {
	var text string
	if json.Unmarshal(content, &text) == nil {
		return text, !claudeInjected.MatchString(strings.TrimSpace(text))
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(content, &blocks) != nil {
		return "", false
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && !claudeInjected.MatchString(strings.TrimSpace(b.Text)) {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n"), len(parts) > 0
}
