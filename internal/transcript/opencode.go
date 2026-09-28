package transcript

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/atqamz/hand/internal/harness"
)

const reexport = 5 * time.Second

func (r *Reader) opencode(ctx context.Context, s *session, id, dir string) error {
	if r.Paths.Opencode == "" {
		return errors.New("opencode is not on PATH")
	}
	stamp := dbStamp(r.Paths.OpencodeData)
	switch {
	case s.exported.IsZero():
	case stamp.IsZero() && time.Since(s.exported) < reexport:
		return nil
	case !stamp.IsZero() && stamp.Equal(s.stamp):
		return nil
	}
	out, err := harness.Opencode(r.Paths.Opencode, dir, "session", "export", "--standalone", id)
	if err != nil {
		return err
	}
	var export struct {
		Messages []struct {
			Type   string `json:"type"`
			Text   string `json:"text"`
			Finish string `json:"finish"`
			Time   struct {
				Created int64 `json:"created"`
			} `json:"time"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"messages"`
	}
	if json.Unmarshal(out, &export) != nil || export.Messages == nil {
		return ErrUnreadable
	}
	fresh := session{offsets: map[string]int64{}, exported: time.Now(), stamp: stamp}
	for _, m := range export.Messages {
		fresh.lines++
		if m.Type == "" {
			continue
		}
		fresh.known++
		at := time.UnixMilli(m.Time.Created).UTC().Format("2006-01-02T15:04:05.000Z")
		switch m.Type {
		case "user":
			fresh.user(m.Text, at)
		case "assistant":
			var texts []string
			for _, c := range m.Content {
				if c.Type == "text" {
					texts = append(texts, c.Text)
				}
			}
			for i, t := range texts {
				fresh.reply(t, at, m.Finish == "stop" && i == len(texts)-1)
			}
		}
	}
	*s = fresh
	return nil
}

func dbStamp(dir string) time.Time {
	var newest time.Time
	for _, name := range []string{"opencode.db", "opencode.db-wal"} {
		if fi, err := os.Stat(filepath.Join(dir, name)); err == nil && fi.ModTime().After(newest) {
			newest = fi.ModTime()
		}
	}
	return newest
}
