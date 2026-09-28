package transcript

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
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
		Messages []exportMessage `json:"messages"`
	}
	if json.Unmarshal(out, &export) != nil || export.Messages == nil {
		return ErrUnreadable
	}
	fresh := session{offsets: map[string]int64{}, exported: time.Now(), stamp: stamp}
	for _, m := range export.Messages {
		fresh.lines++
		role, created, finish, texts := m.flat()
		if role == "" {
			continue
		}
		fresh.known++
		at := time.UnixMilli(created).UTC().Format("2006-01-02T15:04:05.000Z")
		switch role {
		case "user":
			if m.Text != "" {
				texts = []string{m.Text}
			}
			fresh.user(strings.Join(texts, "\n"), at)
		case "assistant":
			for i, t := range texts {
				fresh.reply(t, at, finish == "stop" && i == len(texts)-1)
			}
		}
	}
	*s = fresh
	return nil
}

type exportPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type exportStamp struct {
	Created int64 `json:"created"`
}

type exportMessage struct {
	Type    string       `json:"type"`
	Text    string       `json:"text"`
	Finish  string       `json:"finish"`
	Time    exportStamp  `json:"time"`
	Content []exportPart `json:"content"`
	Info    struct {
		Role   string      `json:"role"`
		Finish string      `json:"finish"`
		Time   exportStamp `json:"time"`
	} `json:"info"`
	Parts []exportPart `json:"parts"`
}

func (m exportMessage) flat() (role string, created int64, finish string, texts []string) {
	role, created, finish = m.Type, m.Time.Created, m.Finish
	if role == "" {
		role, created, finish = m.Info.Role, m.Info.Time.Created, m.Info.Finish
	}
	for _, p := range append(m.Content, m.Parts...) {
		if p.Type == "text" {
			texts = append(texts, p.Text)
		}
	}
	return role, created, finish, texts
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
